package engine

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"

	"github.com/morain/5gws/internal/config"
)

func TestDNSReadinessProbesUseLoopbackAndDOTDomain(t *testing.T) {
	cfg := config.Config{DNS: config.DNSConfig{
		ListenTCP:       "0.0.0.0:1053",
		ListenPublicDOT: "0.0.0.0:853",
		DOTDomain:       "dot.example.com",
		CustomPools:     []config.DNSPoolConfig{{Name: "cn_netease", ProbeDomain: "music.163.com", Upstreams: []string{"117.50.10.10"}}},
	}}
	probes := dnsReadinessProbes(cfg)
	if len(probes) != 6 || probes[0].address != "127.0.0.1:1053" || probes[1].domain != "www.baidu.com." || probes[2].domain != "music.163.com." {
		t.Fatalf("DNS readiness probes = %#v", probes)
	}
	if probes[3].network != "tcp-tls" || probes[3].address != "127.0.0.1:853" || probes[3].tlsConfig.ServerName != "dot.example.com" || probes[5].domain != "music.163.com." {
		t.Fatalf("DoT readiness probes = %#v", probes[3:])
	}
}

func TestWaitDNSReadinessRetriesSERVFAIL(t *testing.T) {
	var requests atomic.Int32
	address, shutdown := startTestDNSServer(t, nil, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		if requests.Add(1) < 3 {
			response.Rcode = dns.RcodeServerFailure
		} else {
			response.Answer = testAAnswer(request)
		}
		_ = w.WriteMsg(response)
	}))
	defer shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitDNSReadiness(ctx, dnsReadinessProbe{label: "test DNS", address: address, domain: "example.com.", network: "tcp"}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 3 {
		t.Fatalf("DNS requests = %d, want 3", requests.Load())
	}
}

func TestProbeDNSReadinessRejectsEmptyAnswer(t *testing.T) {
	address, shutdown := startTestDNSServer(t, nil, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		_ = w.WriteMsg(response)
	}))
	defer shutdown()

	err := probeDNSReadiness(context.Background(), dnsReadinessProbe{label: "test DNS", address: address, domain: "example.com.", network: "tcp"})
	if err == nil || !strings.Contains(err.Error(), "no A answer") {
		t.Fatalf("empty answer error = %v", err)
	}
}

func TestProbeDNSReadinessVerifiesDOTCertificate(t *testing.T) {
	certificate, roots := testDOTCertificate(t, "dot.example.com")
	address, shutdown := startTestDNSServer(t, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		response.Answer = testAAnswer(request)
		_ = w.WriteMsg(response)
	}))
	defer shutdown()

	probe := dnsReadinessProbe{label: "public DoT", address: address, domain: "example.com.", network: "tcp-tls", tlsConfig: &tls.Config{ServerName: "dot.example.com", RootCAs: roots, MinVersion: tls.VersionTLS12}}
	if err := probeDNSReadiness(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	probe.tlsConfig.ServerName = "wrong.example.com"
	if err := probeDNSReadiness(context.Background(), probe); err == nil {
		t.Fatal("expected DoT hostname mismatch")
	}
}

func startTestDNSServer(t *testing.T, tlsConfig *tls.Config, handler dns.Handler) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	server := &dns.Server{Listener: listener, Handler: handler}
	go func() { _ = server.ActivateAndServe() }()
	return listener.Addr().String(), func() { _ = server.Shutdown() }
}

func testAAnswer(request *dns.Msg) []dns.RR {
	return []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: request.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET},
		A:   net.ParseIP("192.0.2.10"),
	}}
}

func testDOTCertificate(t *testing.T, domain string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	_, caKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	_, leafKey, _ := ed25519.GenerateKey(rand.Reader)
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{domain}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caTemplate, leafKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}
	roots := x509.NewCertPool()
	ca, _ := x509.ParseCertificate(caDER)
	roots.AddCert(ca)
	return certificate, roots
}
