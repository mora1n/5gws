package diagnostics

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/morain/5gws/internal/config"
)

func TestProbeDNSUpstream(t *testing.T) {
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: packetConn, Handler: dns.HandlerFunc(answerHandler)}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	result := probeDNSUpstream(context.Background(), "cn", "example.com.", packetConn.LocalAddr().String())
	if result.Status != "ok" || len(result.Answers) != 1 || result.Answers[0] != "192.0.2.10" {
		t.Fatalf("DNS result = %+v", result)
	}
}

func TestQueryDOH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/dns-message" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		data, _ := io.ReadAll(r.Body)
		request := new(dns.Msg)
		if err := request.Unpack(data); err != nil {
			t.Error(err)
			return
		}
		response := new(dns.Msg)
		response.SetReply(request)
		response.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.11")}}
		body, _ := response.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	message := new(dns.Msg)
	message.SetQuestion("example.com.", dns.TypeA)
	response, err := queryDOH(context.Background(), server.URL, message, server.Client())
	if err != nil || len(answerAddresses(response)) != 1 {
		t.Fatalf("DoH response = %+v, %v", response, err)
	}
}

func TestProbeDirectExit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "203.0.113.20\n") }))
	defer server.Close()
	result := (Runner{EgressURL: server.URL}).probeExit(context.Background(), config.ExitConfig{Name: "direct", Type: "direct"})
	if result.Status != "ok" || result.EgressIP != "203.0.113.20" {
		t.Fatalf("exit result = %+v", result)
	}
}

func TestProbeExitReportsUpstreamFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().(*net.TCPAddr)
	listener.Close()
	result := (Runner{}).probeExit(context.Background(), config.ExitConfig{
		Name: "ss", Type: "shadowsocks-rust", Server: address.IP.String(), ServerPort: address.Port,
	})
	if result.Status != "error" || result.UpstreamStatus != "error" || result.Error == "" {
		t.Fatalf("exit result = %+v", result)
	}
}

func TestProbeDOT(t *testing.T) {
	certificate, roots, leafPEM := testCertificate(t, "dot.example.com")
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go serveOneDOT(listener)
	certFile := filepath.Join(t.TempDir(), "fullchain.pem")
	if err := os.WriteFile(certFile, leafPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DNS: config.DNSConfig{DOTDomain: "dot.example.com", ListenPublicDOT: listener.Addr().String(), CertFile: certFile}}
	result := probeDOT(context.Background(), cfg, roots)
	if result.Status != "ok" || !result.DomainMatch || result.CertificateStatus != "ok" {
		t.Fatalf("DoT result = %+v", result)
	}
	cfg.DNS.DOTDomain = "wrong.example.com"
	mismatch := probeDOT(context.Background(), cfg, roots)
	if mismatch.Status != "error" || mismatch.DomainMatch || mismatch.Error == "" {
		t.Fatalf("DoT mismatch = %+v", mismatch)
	}
}

func answerHandler(w dns.ResponseWriter, request *dns.Msg) {
	response := new(dns.Msg)
	response.SetReply(request)
	response.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("192.0.2.10")}}
	_ = w.WriteMsg(response)
}

func serveOneDOT(listener net.Listener) {
	connection, err := listener.Accept()
	if err != nil {
		return
	}
	defer connection.Close()
	dnsConnection := &dns.Conn{Conn: connection}
	message, err := dnsConnection.ReadMsg()
	if err != nil {
		return
	}
	response := new(dns.Msg)
	response.SetReply(message)
	_ = dnsConnection.WriteMsg(response)
}

func testCertificate(t *testing.T, domain string) (tls.Certificate, *x509.CertPool, []byte) {
	t.Helper()
	_, caKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	_, leafKey, _ := ed25519.GenerateKey(rand.Reader)
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(60 * 24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caTemplate, leafKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	keyData, _ := x509.MarshalPKCS8PrivateKey(leafKey)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyData})
	certificate, err := tls.X509KeyPair(leafPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	ca, _ := x509.ParseCertificate(caDER)
	roots.AddCert(ca)
	return certificate, roots, leafPEM
}
