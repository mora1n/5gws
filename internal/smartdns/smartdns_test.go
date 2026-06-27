package smartdns

import (
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

func TestConfigRendersAddressRules(t *testing.T) {
	cfg := testConfig()
	out, err := Config(cfg, rules.Normalized{Rules: []rules.Rule{
		{Name: "openai", Exit: "ss1", DomainSuffix: []string{"openai.com"}},
		{Name: "cn", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"bind 0.0.0.0:1053",
		"bind-tcp 0.0.0.0:1053",
		"bind-tls 0.0.0.0:1853",
		"bind-tls 0.0.0.0:853 -group overseas_public -no-rule-addr",
		"bind-cert-file /etc/5gws/certs/fullchain.pem",
		"bind-cert-key-file /etc/5gws/certs/privkey.pem",
		"response-mode fastest-response",
		"force-AAAA-SOA yes",
		"force-qtype-SOA 64 65",
		"address /openai.com/10.0.0.1",
		"nameserver /example.cn/cn",
		"server 223.5.5.5 -group cn -exclude-default-group",
		"server 22.22.22.22 -group overseas_private -exclude-default-group",
		"server 22.22.22.22 -group overseas_public -exclude-default-group",
		"server https://dns.quad9.net/dns-query -group overseas_public -exclude-default-group",
		"server 9.9.9.9 -group overseas_public -exclude-default-group",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "server 1.1.1.1 -group overseas_private") {
		t.Fatalf("private overseas default must only use 22.22.22.22:\n%s", out)
	}
	if strings.Contains(out, "address /example.cn/") {
		t.Fatalf("DNS pool rule must not render address rewrite:\n%s", out)
	}
}

func TestConfigRejectsUnsupportedDNSRewriteMatchers(t *testing.T) {
	_, err := Config(testConfig(), rules.Normalized{Rules: []rules.Rule{
		{Name: "keyword", Exit: "ss1", DomainKeyword: []string{"openai"}},
	}})
	if err == nil {
		t.Fatal("expected unsupported matcher error")
	}
}

func TestConfigRejectsImportedRuleSetMatchers(t *testing.T) {
	_, err := Config(testConfig(), rules.Normalized{Rules: []rules.Rule{
		{Name: "imported", Exit: "ss1", RuleSet: []string{"remote-provider"}},
	}})
	if err == nil {
		t.Fatal("expected rule_set matcher to be rejected by smartdns renderer")
	}
}

func testConfig() config.Config {
	cfg := config.Config{
		Network: config.NetworkConfig{
			GatewayIP:    "10.0.0.1",
			InternalCIDR: "10.0.0.0/24",
			IngressIface: "eth0",
		},
		DNS: config.DNSConfig{
			DOTDomain: "dot.example.com",
		},
		Exits: []config.ExitConfig{{Name: "direct", Type: "direct"}},
	}
	cfg.ApplyDefaults()
	return cfg
}
