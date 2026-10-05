package smartdns

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/morain/5gws/internal/rules"
)

// Run explicitly with SMARTDNS_TEST_BINARY pointing to a verified smartdns-rs release.
func TestRuntimeSeparatesGatewayAndPublicDNS(t *testing.T) {
	binary := os.Getenv("SMARTDNS_TEST_BINARY")
	if binary == "" {
		t.Skip("set SMARTDNS_TEST_BINARY to run the real SmartDNS integration test")
	}
	root := t.TempDir()
	upstream := runtimeUpstream(t, "192.0.2.10")
	cnUpstream := runtimeUpstream(t, "192.0.2.20")
	cfg := testConfig()
	cfg.DNS.ListenUDP = runtimeAddress(t)
	cfg.DNS.ListenTCP = cfg.DNS.ListenUDP
	cfg.DNS.ListenDOT, cfg.DNS.ListenPublicDOT = runtimeAddress(t), runtimeAddress(t)
	cfg.DNS.UpstreamsCN, cfg.DNS.UpstreamsOverseasPrivate, cfg.DNS.UpstreamsOverseasPublic = []string{cnUpstream}, []string{upstream}, []string{upstream}
	cfg.DNS.CustomPools = nil
	cfg.DNS.CertFile, cfg.DNS.KeyFile = filepath.Join(root, "cert.pem"), filepath.Join(root, "key.pem")
	tlsConfig := runtimeCertificate(t, cfg.DNS.CertFile, cfg.DNS.KeyFile)
	out, err := GenerateAt(cfg, rules.Normalized{Rules: []rules.Rule{{Name: "gateway", Exit: "direct", DomainSuffix: []string{"gateway.test"}}, {Name: "cn", DNSPool: "cn", DomainSuffix: []string{"pool.test"}}}}, root)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range out.Files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configuration := strings.Replace(out.Config, "cache-file "+cacheFile, "cache-file "+filepath.Join(root, "cache"), 1) + "log-file " + filepath.Join(root, "smartdns.log") + "\n"
	path := filepath.Join(root, "smartdns.conf")
	if err := os.WriteFile(path, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	startRuntime(t, binary, path)
	client := &dns.Client{Net: "tcp-tls", TLSConfig: tlsConfig, Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		message := new(dns.Msg)
		message.SetQuestion("gateway.test.", dns.TypeA)
		if _, _, err := client.Exchange(message, cfg.DNS.ListenDOT); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SmartDNS did not become ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for range 3 {
		checkRuntimeListener(t, client, runtimeExpectation{address: cfg.DNS.ListenPublicDOT, domain: "gateway.test.", gateway: false, ipv4: "192.0.2.10"})
		checkRuntimeListener(t, client, runtimeExpectation{address: cfg.DNS.ListenDOT, domain: "gateway.test.", gateway: true, ipv4: cfg.Network.GatewayIP})
		checkRuntimeListener(t, client, runtimeExpectation{address: cfg.DNS.ListenPublicDOT, domain: "pool.test.", gateway: false, ipv4: "192.0.2.10"})
		checkRuntimeListener(t, client, runtimeExpectation{address: cfg.DNS.ListenDOT, domain: "pool.test.", gateway: true, ipv4: "192.0.2.20"})
	}
}

type runtimeExpectation struct {
	address string
	domain  string
	gateway bool
	ipv4    string
}

func checkRuntimeListener(t *testing.T, client *dns.Client, expected runtimeExpectation) {
	t.Helper()
	conn, err := client.Dial(expected.address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, typ := range []uint16{dns.TypeA, dns.TypeAAAA, dns.TypeSVCB, dns.TypeHTTPS} {
		message := new(dns.Msg)
		message.SetQuestion(expected.domain, typ)
		response, _, err := client.ExchangeWithConn(message, conn)
		if err != nil {
			t.Fatal(err)
		}
		want := typ
		if expected.gateway && typ != dns.TypeA {
			want = dns.TypeSOA
		}
		if response.Rcode != dns.RcodeSuccess || len(response.Answer) == 0 || response.Answer[0].Header().Rrtype != want {
			t.Fatalf("listener %s type %d: want %d, response %v", expected.address, typ, want, response)
		}
		if typ == dns.TypeA && response.Answer[0].(*dns.A).A.String() != expected.ipv4 {
			t.Fatalf("crossed DNS policy: %v", response)
		}
	}
}

func runtimeUpstream(t *testing.T, ipv4 string) string {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		q := request.Question[0]
		header := dns.RR_Header{Name: q.Name, Rrtype: q.Qtype, Class: dns.ClassINET, Ttl: 300}
		switch q.Qtype {
		case dns.TypeA:
			response.Answer = []dns.RR{&dns.A{Hdr: header, A: net.ParseIP(ipv4)}}
		case dns.TypeAAAA:
			response.Answer = []dns.RR{&dns.AAAA{Hdr: header, AAAA: net.ParseIP("2001:db8::10")}}
		case dns.TypeSVCB:
			response.Answer = []dns.RR{&dns.SVCB{Hdr: header, Priority: 1, Target: "."}}
		case dns.TypeHTTPS:
			response.Answer = []dns.RR{&dns.HTTPS{SVCB: dns.SVCB{Hdr: header, Priority: 1, Target: "."}}}
		}
		_ = w.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { server.Shutdown() })
	return packet.LocalAddr().String()
}

func runtimeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}

func startRuntime(t *testing.T, binary, path string) {
	t.Helper()
	log, err := os.Create(filepath.Join(filepath.Dir(path), "console.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "run", "-c", path)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("SmartDNS exited: %v", err)
			}
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Error("SmartDNS shutdown timed out")
		}
		log.Close()
		if t.Failed() {
			data, _ := os.ReadFile(log.Name())
			t.Log(string(data))
		}
	})
}

func runtimeCertificate(t *testing.T, certPath, keyPath string) *tls.Config {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"dot.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{certPath: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyPath: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &tls.Config{RootCAs: roots, ServerName: "dot.example.com", MinVersion: tls.VersionTLS12}
}
