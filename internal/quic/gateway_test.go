package quic

import (
	"context"
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

func TestUDPProxyRelaysDirectTarget(t *testing.T) {
	upstream, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		buf := make([]byte, 1024)
		n, addr, err := upstream.ReadFromUDP(buf)
		if err != nil {
			return
		}
		_, _ = upstream.WriteToUDP(append([]byte("echo:"), buf[:n]...), addr)
	}()

	cfg := testConfig()
	cfg.UDPProxies = []config.UDPProxyConfig{{
		Name:       "stun-test",
		ClientPort: 3478,
		ListenPort: 0,
		Target:     upstream.LocalAddr().String(),
		Exit:       "direct",
	}}
	proxy, err := listenUDPProxy(cfg, cfg.UDPProxies[0])
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- proxy.serve(ctx)
	}()

	port := proxy.listener.LocalAddr().(*net.UDPAddr).Port
	client, err := net.Dial("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
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
	if got := string(buf[:n]); got != "echo:ping" {
		t.Fatalf("udp proxy response = %q, want echo:ping", got)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("proxy serve returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy serve did not stop after context cancellation")
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
