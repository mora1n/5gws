package render

import (
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

func TestNFTablesRedirectOnlyInternalCIDR(t *testing.T) {
	cfg := testConfig()
	out := NFTables(cfg)
	if !strings.Contains(out, `ip saddr 10.0.0.0/24 tcp dport 80 counter redirect`) {
		t.Fatalf("internal source redirect missing:\n%s", out)
	}
	if !strings.Contains(out, `ip saddr 10.0.0.0/24 udp dport 53 counter redirect to :1053`) {
		t.Fatalf("internal DNS redirect missing:\n%s", out)
	}
	if !strings.Contains(out, `udp dport 443 counter redirect to :18443`) {
		t.Fatalf("UDP/443 redirect counter missing:\n%s", out)
	}
	if strings.Contains(out, "flush ruleset") {
		t.Fatalf("nft output must not flush global ruleset:\n%s", out)
	}
	if !strings.Contains(out, `ip saddr != 10.0.0.0/24 udp dport { 1053, 18443 } drop`) {
		t.Fatalf("non-internal UDP backend protection missing:\n%s", out)
	}
	if !strings.Contains(out, `ip saddr != 10.0.0.0/24 tcp dport { 1053, 1853, 18080, 18443 } reject with tcp reset`) {
		t.Fatalf("non-internal TCP backend protection missing:\n%s", out)
	}
	if strings.Contains(out, `ip saddr 10.0.0.0/24 udp dport { 53, 443, 1053 } accept`) ||
		strings.Contains(out, `ip saddr 10.0.0.0/24 tcp dport { 53, 853, 1053, 1853, 18080, 18443 } accept`) {
		t.Fatalf("input chain should not render redundant internal accept rules:\n%s", out)
	}
}

func TestHAProxyUsesFallbackForUnknownHostOrSNI(t *testing.T) {
	cfg := testConfig()
	out := HAProxy(cfg, rules.Normalized{Rules: []rules.Rule{
		{Name: "openai", Exit: "ss1", DomainSuffix: []string{"openai.com"}},
		{Name: "cn", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
	}})
	if !strings.Contains(out, "tcp-request content reject") {
		t.Fatalf("TLS missing-SNI reject missing:\n%s", out)
	}
	if !strings.Contains(out, "http-request deny deny_status 403") {
		t.Fatalf("HTTP missing-Host deny missing:\n%s", out)
	}
	if !strings.Contains(out, "bind 0.0.0.0:18080") || !strings.Contains(out, "bind 0.0.0.0:18443") {
		t.Fatalf("HAProxy redirect backends must listen on high ports:\n%s", out)
	}
	if strings.Contains(out, "bind 0.0.0.0:80") || strings.Contains(out, "bind 0.0.0.0:443") {
		t.Fatalf("HAProxy must not bind public 80/443:\n%s", out)
	}
	if !strings.Contains(out, "log-format \"5gws http") || !strings.Contains(out, "log-format \"5gws tls") {
		t.Fatalf("HAProxy access log formats missing:\n%s", out)
	}
	if !strings.Contains(out, "default_backend http_direct") {
		t.Fatalf("HTTP fallback backend missing:\n%s", out)
	}
	if !strings.Contains(out, "default_backend tls_direct") {
		t.Fatalf("TLS fallback backend missing:\n%s", out)
	}
	if !strings.Contains(out, "tcp-request content do-resolve(sess.dst,realdns,ipv4) var(sess.sni)") {
		t.Fatalf("TLS frontend must resolve the captured SNI before backend routing:\n%s", out)
	}
	if !strings.Contains(out, "tcp-request content set-dst var(sess.dst) if has_sni") {
		t.Fatalf("TLS frontend must set the dynamic backend destination:\n%s", out)
	}
	if strings.Contains(out, "tcp-request content do-resolve(sess.dst,realdns,ipv4) req.ssl_sni") {
		t.Fatalf("TLS routing must not read req.ssl_sni after frontend routing:\n%s", out)
	}
	if strings.Contains(out, "backend tls_direct\n    mode tcp\n    tcp-request content do-resolve") {
		t.Fatalf("TLS backend must not resolve after frontend routing:\n%s", out)
	}
	if !strings.Contains(out, "socks4 127.0.0.1:1080") {
		t.Fatalf("shadowsocks-rust backend must use local SOCKS:\n%s", out)
	}
	if strings.Contains(out, "example.cn") {
		t.Fatalf("DNS-only rule must not be rendered into HAProxy ACLs:\n%s", out)
	}
	if !strings.Contains(out, "nameserver dns0 1.1.1.1:53") {
		t.Fatalf("backend resolver missing:\n%s", out)
	}
}

func TestGenerateFailsUnknownExit(t *testing.T) {
	_, err := Generate(testConfig(), rules.Normalized{Rules: []rules.Rule{
		{Name: "bad", Exit: "missing", Domain: []string{"example.com"}},
	}})
	if err == nil {
		t.Fatal("expected unknown exit error")
	}
}

func TestGenerateRejectsUDPOonlyExitForHAProxy(t *testing.T) {
	cfg := testConfig()
	tcp, udp := false, true
	cfg.Exits[1].TCP = &tcp
	cfg.Exits[1].UDP = &udp
	_, err := Generate(cfg, rules.Normalized{Rules: []rules.Rule{
		{Name: "udp-only", Exit: "ss1", Domain: []string{"example.com"}},
	}})
	if err == nil {
		t.Fatal("expected udp-only exit to be rejected for HAProxy rules")
	}
}

func TestGenerateUsesComponentSubdirectories(t *testing.T) {
	cfg := testConfig()
	cfg.IOS.Enabled = true
	cfg.Telegram.Enabled = true
	files, err := Generate(cfg, rules.Normalized{Rules: []rules.Rule{
		{Name: "openai", Exit: "ss1", DomainSuffix: []string{"openai.com"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range files {
		got[file.Path] = file.Content
	}
	for _, want := range []string{
		"haproxy/haproxy.cfg",
		"nftables/5gws.nft",
		"smartdns/smartdns.conf",
		"ssrust/ss1.json",
		"systemd/5gws-smartdns.service",
		"systemd/5gws-haproxy.service",
		"systemd/5gws-quic.service",
		"systemd/5gws-ssrust-ss1.service",
		"systemd/5gws-cert.service",
		"systemd/5gws-bot.service",
	} {
		if _, ok := got[want]; !ok {
			t.Fatalf("missing generated file %q; got %#v", want, got)
		}
	}
	if strings.Contains(got["systemd/5gws-haproxy.service"], "/rendered/haproxy.cfg") {
		t.Fatalf("haproxy service still points at old flat path:\n%s", got["systemd/5gws-haproxy.service"])
	}
	if !strings.Contains(got["systemd/5gws-haproxy.service"], "/rendered/haproxy/haproxy.cfg") {
		t.Fatalf("haproxy service does not point at component path:\n%s", got["systemd/5gws-haproxy.service"])
	}
	if !strings.Contains(got["systemd/5gws-bot.service"], "--rules /etc/5gws/rules.toml") {
		t.Fatalf("bot service does not pass rules path:\n%s", got["systemd/5gws-bot.service"])
	}
}

func TestGenerateUsesSmartDNSLoggingFlags(t *testing.T) {
	cfg := testConfig()
	cfg.Logging.Level = "debug"
	files, err := Generate(cfg, rules.Normalized{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, file := range files {
		got[file.Path] = file.Content
	}
	if !strings.Contains(got["systemd/5gws-smartdns.service"], "smartdns run -c /var/lib/5gws/rendered/smartdns/smartdns.conf -v") {
		t.Fatalf("debug smartdns flags missing:\n%s", got["systemd/5gws-smartdns.service"])
	}
}

func testConfig() config.Config {
	cfg := config.Config{
		Network: config.NetworkConfig{
			GatewayIP:         "10.0.0.1",
			InternalCIDR:      "10.0.0.0/24",
			IngressIface:      "eth0",
			HTTPRedirectPort:  18080,
			HTTPSRedirectPort: 18443,
			QUICRedirectPort:  18443,
		},
		DNS: config.DNSConfig{
			ListenUDP:                "0.0.0.0:1053",
			ListenTCP:                "0.0.0.0:1053",
			ListenDOT:                "0.0.0.0:1853",
			ListenPublicDOT:          "0.0.0.0:853",
			BackendResolvers:         []string{"1.1.1.1:53"},
			CertDir:                  "/var/lib/5gws/ios",
			CacheSize:                8192,
			UpstreamsCN:              []string{"223.5.5.5"},
			UpstreamsOverseasPrivate: []string{"1.1.1.1"},
			UpstreamsOverseasPublic:  []string{"8.8.8.8"},
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
