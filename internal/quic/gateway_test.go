package quic

import (
	"testing"

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
