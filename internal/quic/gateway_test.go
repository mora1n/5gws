package quic

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

func TestSelectExitUsesGatewayRuleBeforeFallback(t *testing.T) {
	cfg := testConfig()
	norm := rules.Normalized{Rules: []rules.Rule{
		{Name: "cn", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
		{Name: "proxy", Exit: "ss1", DomainSuffix: []string{"openai.com"}},
	}}

	exit, matched, err := selectExit(cfg, norm, "api.openai.com")
	if err != nil {
		t.Fatal(err)
	}
	if !matched || exit.Name != "ss1" {
		t.Fatalf("exit = %q matched=%v, want ss1 matched=true", exit.Name, matched)
	}
}

func TestSelectExitFallsBackWhenOnlyDNSPoolRuleMatches(t *testing.T) {
	cfg := testConfig()
	norm := rules.Normalized{Rules: []rules.Rule{
		{Name: "cn", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
	}}

	exit, matched, err := selectExit(cfg, norm, "www.example.cn")
	if err != nil {
		t.Fatal(err)
	}
	if matched || exit.Name != "direct" {
		t.Fatalf("exit = %q matched=%v, want direct matched=false", exit.Name, matched)
	}
}

func TestTCPGatewayUsesHostAndOriginalPort(t *testing.T) {
	upstream, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write(append([]byte("echo:"), buf[:n]...))
	}()

	cfg := testConfig()
	cfg.Network.TCPRedirectPort = 0
	gateway, err := listenTCPGateway(cfg, rules.Normalized{Rules: []rules.Rule{
		{Name: "speedtest", Exit: "direct", DomainSuffix: []string{"localhost"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.close()
	gateway.originalDst = func(*net.TCPConn) (*net.TCPAddr, error) {
		return &net.TCPAddr{IP: net.ParseIP(cfg.Network.GatewayIP), Port: upstream.Addr().(*net.TCPAddr).Port}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- gateway.serve(ctx)
	}()

	port := gateway.listener.Addr().(*net.TCPAddr).Port
	client, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	req := []byte("GET / HTTP/1.1\r\nHost: localhost\r\n\r\n")
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "echo:"+string(req) {
		t.Fatalf("tcp gateway response = %q, want echo request", got)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("gateway serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway serve did not stop after context cancellation")
	}
}

func TestTCPGatewayRejectsGatewayDstWithoutHost(t *testing.T) {
	cfg := testConfig()
	cfg.Network.TCPRedirectPort = 0
	gateway, err := listenTCPGateway(cfg, rules.Normalized{})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.close()
	gateway.originalDst = func(*net.TCPConn) (*net.TCPAddr, error) {
		return &net.TCPAddr{IP: net.ParseIP(cfg.Network.GatewayIP), Port: 8080}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- gateway.serve(ctx)
	}()

	port := gateway.listener.Addr().(*net.TCPAddr).Port
	client, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("raw speedtest payload")); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, err := client.Read(make([]byte, 1)); err == nil || n != 0 {
		t.Fatalf("expected rejected gateway connection, read n=%d err=%v", n, err)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("gateway serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway serve did not stop after context cancellation")
	}
}

func TestTCPGatewayRelaysRealOriginalDestinationWithoutHost(t *testing.T) {
	upstream, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		conn, err := upstream.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		_, _ = conn.Write(append([]byte("echo:"), buf[:n]...))
	}()

	cfg := testConfig()
	cfg.Network.TCPRedirectPort = 0
	gateway, err := listenTCPGateway(cfg, rules.Normalized{})
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.close()
	gateway.originalDst = func(*net.TCPConn) (*net.TCPAddr, error) {
		return upstream.Addr().(*net.TCPAddr), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- gateway.serve(ctx)
	}()

	port := gateway.listener.Addr().(*net.TCPAddr).Port
	client, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	req := []byte("raw speedtest payload")
	if _, err := client.Write(req); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1024)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "echo:"+string(req) {
		t.Fatalf("tcp gateway response = %q, want echo request", got)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("gateway serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gateway serve did not stop after context cancellation")
	}
}

func TestSniffTCPHost(t *testing.T) {
	httpData := []byte("GET / HTTP/1.1\r\nHost: Speed.Example:8080\r\n\r\n")
	if host, source := sniffTCPHost(httpData); host != "speed.example" || source != "http_host" {
		t.Fatalf("sniffTCPHost HTTP = %q/%q, want speed.example/http_host", host, source)
	}

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	tlsClient := tls.Client(client, &tls.Config{ServerName: "SNI.Example", InsecureSkipVerify: true})
	done := make(chan struct{})
	go func() {
		_ = tlsClient.Handshake()
		close(done)
	}()
	buf := make([]byte, 4096)
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	<-done
	if host, source := sniffTCPHost(buf[:n]); host != "sni.example" || source != "tls_sni" {
		t.Fatalf("sniffTCPHost TLS = %q/%q, want sni.example/tls_sni", host, source)
	}

	if host, source := sniffTCPHost([]byte("raw speedtest payload")); host != "" || source != "" {
		t.Fatalf("sniffTCPHost raw = %q/%q, want empty", host, source)
	}
}

func testConfig() config.Config {
	cfg := config.Config{
		Network: config.NetworkConfig{
			GatewayIP:    "10.0.0.1",
			InternalCIDR: "10.0.0.0/24",
			IngressIface: "eth0",
		},
		Exits: []config.ExitConfig{
			{Name: "direct", Type: "direct"},
			{
				Name:           "ss1",
				Type:           "shadowsocks-rust",
				Server:         "198.51.100.10",
				ServerPort:     8388,
				Method:         "aes-256-gcm",
				Password:       "change-me",
				ListenAddress:  "127.0.0.1",
				ListenPort:     1080,
				TimeoutSeconds: 300,
			},
		},
	}
	cfg.ApplyDefaults()
	return cfg
}
