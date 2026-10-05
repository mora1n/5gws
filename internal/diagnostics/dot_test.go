package diagnostics

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/morain/5gws/internal/config"
)

func TestValidateDNSResponseRejectsInvalidReplies(t *testing.T) {
	request := new(dns.Msg)
	request.SetQuestion("example.com.", dns.TypeA)
	cases := []struct {
		name   string
		change func(*dns.Msg)
	}{
		{"not a reply", func(m *dns.Msg) { m.Response = false }},
		{"wrong ID", func(m *dns.Msg) { m.Id++ }},
		{"wrong opcode", func(m *dns.Msg) { m.Opcode = dns.OpcodeStatus }},
		{"wrong question", func(m *dns.Msg) { m.Question[0].Name = "other.example." }},
		{"missing question", func(m *dns.Msg) { m.Question = nil }},
		{"SERVFAIL", func(m *dns.Msg) { m.Rcode = dns.RcodeServerFailure }},
		{"NXDOMAIN", func(m *dns.Msg) { m.Rcode = dns.RcodeNameError }},
		{"empty answer", func(m *dns.Msg) { m.Answer = nil }},
		{"non-A answer", func(m *dns.Msg) {
			m.Answer = []dns.RR{&dns.TXT{Hdr: dns.RR_Header{Rrtype: dns.TypeTXT}, Txt: []string{"not an address"}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := new(dns.Msg)
			response.SetReply(request)
			response.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "example.com.", Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.10")}}
			if err := ValidateDNSResponse(request, response); err != nil {
				t.Fatal(err)
			}
			tc.change(response)
			if err := ValidateDNSResponse(request, response); err == nil {
				t.Fatal("invalid reply accepted")
			}
		})
	}
}

func TestDOTReadDeadlineAfterSuccessfulHandshake(t *testing.T) {
	certificate, roots, certPEM := testCertificate(t, "dot.example.com")
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_, err = (&dns.Conn{Conn: conn}).ReadMsg()
		if err != nil {
			done <- err
			return
		}
		_, err = io.Copy(io.Discard, conn)
		done <- err
	}()
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	result := probeDOTListener(ctx, dotProbe{DNS: config.DNSConfig{DOTDomain: "dot.example.com", CertFile: certFile}, Listen: listener.Addr().String(), Roots: roots})
	if result.Status != "error" || !strings.Contains(result.Error, "DoT response") || time.Since(started) > time.Second {
		t.Fatalf("silent DoT response was not bounded: %+v", result)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("probe connection was not closed")
	}
}

func TestDOTDiagnosticsProbeBothListeners(t *testing.T) {
	certificate, roots, certPEM := testCertificate(t, "dot.example.com")
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	addresses := []string{}
	for range 2 {
		listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		addresses = append(addresses, listener.Addr().String())
		go serveOneDOT(listener)
	}
	cfg := config.Config{DNS: config.DNSConfig{DOTDomain: "dot.example.com", CertFile: certFile, ListenDOT: addresses[0], ListenPublicDOT: addresses[1]}}
	result := (Runner{RootCAs: roots}).Run(context.Background(), cfg, ScopeDOT)
	if result.DOT.Status != "ok" || result.InternalDOT.Status != "ok" || result.DOT.Listen == result.InternalDOT.Listen {
		t.Fatalf("dual listener diagnostics = %+v / %+v", result.DOT, result.InternalDOT)
	}
}
