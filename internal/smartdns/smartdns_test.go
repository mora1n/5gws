package smartdns

import (
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

func TestConfigRendersAddressRules(t *testing.T) {
	cfg := testConfig()
	generated, err := Generate(cfg, rules.Normalized{Rules: []rules.Rule{
		{Name: "openai", Exit: "ss1", DomainSuffix: []string{"openai.com"}},
		{Name: "cn", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	out := generated.Config
	for _, want := range []string{
		"bind 0.0.0.0:1053",
		"bind-tcp 0.0.0.0:1053",
		"bind-tls 0.0.0.0:1853",
		"bind-tls 0.0.0.0:853",
		"bind-cert-file /etc/5gws/certs/fullchain.pem",
		"bind-cert-key-file /etc/5gws/certs/privkey.pem",
		"cache-file /var/log/smartdns/smartdns.cache",
		"response-mode fastest-response",
		"force-AAAA-SOA yes",
		"force-qtype-SOA 64 65",
		"address /domain-set:gateway/10.0.0.1",
		"nameserver /domain-set:pool_cn/cn",
		"server 180.76.76.76 -group cn -exclude-default-group",
		"server 101.226.4.6 -group cn -exclude-default-group",
		"server 218.30.118.6 -group cn -exclude-default-group",
		"server 114.114.114.114 -group cn -exclude-default-group",
		"server 114.114.115.115 -group cn -exclude-default-group",
		"server 117.50.10.10 -group cn -exclude-default-group",
		"server 52.80.66.66 -group cn -exclude-default-group",
		"server 223.5.5.5 -group cn -exclude-default-group",
		"server https://dns.alidns.com/dns-query -group cn -exclude-default-group",
		"server tls://dns.alidns.com -group cn -exclude-default-group",
		"server 119.29.29.29 -group cn -exclude-default-group",
		"server https://doh.pub/dns-query -group cn -exclude-default-group",
		"server tls://dot.pub -group cn -exclude-default-group",
		"server 180.184.1.1 -group cn -exclude-default-group",
		"server https://doh.360.cn/dns-query -group cn -exclude-default-group",
		"server tls://dot.360.cn -group cn -exclude-default-group",
		"server https://doh-pure.onedns.net/dns-query -group cn -exclude-default-group",
		"server tls://dot-pure.onedns.net -group cn -exclude-default-group",
		"server 1.2.4.8 -group cn -exclude-default-group",
		"server 22.22.22.22 -group overseas_private",
		"server 22.22.22.22 -group overseas_public -exclude-default-group",
		"server https://dns.quad9.net/dns-query -group overseas_public -exclude-default-group",
		"server 9.9.9.9 -group overseas_public -exclude-default-group",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if generated.Files["gateway.list"] != "openai.com\n" {
		t.Fatalf("gateway domain set = %q", generated.Files["gateway.list"])
	}
	if generated.Files["pool_cn.list"] != "example.cn\n" {
		t.Fatalf("cn domain set = %q", generated.Files["pool_cn.list"])
	}
	if strings.Contains(out, "server 1.1.1.1 -group overseas_private") {
		t.Fatalf("private overseas default must only use 22.22.22.22:\n%s", out)
	}
	if strings.Contains(out, "bind 0.0.0.0:1053 -group") ||
		strings.Contains(out, "bind-tcp 0.0.0.0:1053 -group") ||
		strings.Contains(out, "bind-tls 0.0.0.0:1853 -group") ||
		strings.Contains(out, "bind-tls 0.0.0.0:853 -group") ||
		strings.Contains(out, "bind-tls 0.0.0.0:853 -no-rule-addr") {
		t.Fatalf("internal DNS listeners must allow nameserver rules to select groups:\n%s", out)
	}
	if strings.Contains(out, "server 22.22.22.22 -group overseas_private -exclude-default-group") {
		t.Fatalf("private overseas upstream must remain in the default group for unmatched internal DNS:\n%s", out)
	}
	if strings.Contains(out, "address /example.cn/") {
		t.Fatalf("DNS pool rule must not render address rewrite:\n%s", out)
	}
	if strings.Contains(out, "/var/lib/5gws/smartdns.cache") {
		t.Fatalf("smartdns cache file must be writable by smartdns runtime user:\n%s", out)
	}
}

func TestConfigUsesFirstDomainRule(t *testing.T) {
	cases := []struct {
		name           string
		rules          []rules.Rule
		wantAddress    bool
		wantNameserver bool
	}{
		{
			name: "gateway before dns pool",
			rules: []rules.Rule{
				{Name: "manual-direct", Exit: "direct", DomainSuffix: []string{"ippure.com"}},
				{Name: "imported-cn", DNSPool: "cn", DomainSuffix: []string{"ippure.com"}},
				{Name: "imported-example", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
			},
			wantAddress: true,
		},
		{
			name: "dns pool before gateway",
			rules: []rules.Rule{
				{Name: "imported-cn", DNSPool: "cn", DomainSuffix: []string{"ippure.com"}},
				{Name: "manual-direct", Exit: "direct", DomainSuffix: []string{"ippure.com"}},
				{Name: "imported-example", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
			},
			wantNameserver: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			generated, err := Generate(testConfig(), rules.Normalized{Rules: tc.rules})
			if err != nil {
				t.Fatal(err)
			}
			out := generated.Config
			hasAddress := strings.Contains(generated.Files["gateway.list"], "ippure.com\n")
			hasNameserver := strings.Contains(generated.Files["pool_cn.list"], "ippure.com\n")
			if hasAddress != tc.wantAddress {
				t.Fatalf("gateway rewrite state for ippure.com = %t, want %t:\n%s", hasAddress, tc.wantAddress, out)
			}
			if hasNameserver != tc.wantNameserver {
				t.Fatalf("dns_pool state for ippure.com = %t, want %t:\n%s", hasNameserver, tc.wantNameserver, out)
			}
			if !strings.Contains(generated.Files["pool_cn.list"], "example.cn\n") {
				t.Fatalf("unrelated dns_pool rule must still render:\n%s", out)
			}
		})
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
