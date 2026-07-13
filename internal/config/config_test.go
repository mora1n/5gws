package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRejectsUnsupportedExit(t *testing.T) {
	cfg := validConfig()
	cfg.Exits = append(cfg.Exits, ExitConfig{Name: "legacy", Type: "legacy-vpn"})
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unsupported exit to be rejected")
	}
}

func TestValidateRequiresDOTDomain(t *testing.T) {
	cfg := validConfig()
	cfg.DNS.DOTDomain = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected missing dot domain to be rejected")
	}
}

func TestValidateRejectsIPDOTDomain(t *testing.T) {
	cfg := validConfig()
	cfg.DNS.DOTDomain = "10.0.0.1"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected IP dot domain to be rejected")
	}
}

func TestValidateRejectsPartialDOTCertificatePaths(t *testing.T) {
	cfg := validConfig()
	cfg.DNS.KeyFile = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected partial cert/key paths to be rejected")
	}
}

func TestValidateRequiresShadowsocksFields(t *testing.T) {
	cfg := validConfig()
	cfg.Exits = append(cfg.Exits, ExitConfig{Name: "ss1", Type: "shadowsocks-rust"})
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected incomplete shadowsocks-rust exit to be rejected")
	}
}

func TestValidateRejectsInvalid2022Key(t *testing.T) {
	cfg := validConfig()
	cfg.Exits = append(cfg.Exits, validSSExit())
	cfg.Exits[1].Method = "2022-blake3-aes-128-gcm"
	cfg.Exits[1].Password = "change-me"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid 2022 key to be rejected")
	}
}

func TestApplyDefaultsSelectsSmartDNS(t *testing.T) {
	cfg := validConfig()
	cfg.DNS = DNSConfig{}
	cfg.DNS.DOTDomain = "dot.example.com"
	cfg.Logging = LoggingConfig{}
	cfg.ApplyDefaults()
	if cfg.DNS.Binary != "smartdns" {
		t.Fatalf("dns binary = %q, want smartdns", cfg.DNS.Binary)
	}
	if cfg.DNS.ListenUDP != "0.0.0.0:1053" {
		t.Fatalf("listen_udp = %q", cfg.DNS.ListenUDP)
	}
	if cfg.Panel.Listen != "127.0.0.1:19443" {
		t.Fatalf("panel.listen = %q", cfg.Panel.Listen)
	}
	if cfg.IOS.BaseURL != "https://dot.example.com" {
		t.Fatalf("ios.base_url = %q", cfg.IOS.BaseURL)
	}
	assertEqualStrings(t, cfg.DNS.UpstreamsCN, []string{
		"180.76.76.76", "101.226.4.6", "218.30.118.6",
		"114.114.114.114", "114.114.115.115", "117.50.10.10", "52.80.66.66",
		"223.5.5.5", "223.6.6.6", "https://dns.alidns.com/dns-query", "tls://dns.alidns.com",
		"119.29.29.29", "https://doh.pub/dns-query", "tls://dot.pub",
		"180.184.1.1", "180.184.2.2", "https://doh.360.cn/dns-query", "tls://dot.360.cn",
		"https://doh-pure.onedns.net/dns-query", "tls://dot-pure.onedns.net", "1.2.4.8", "210.2.4.8",
	})
	assertEqualStrings(t, cfg.DNS.UpstreamsOverseasPrivate, []string{"22.22.22.22"})
	assertEqualStrings(t, cfg.DNS.UpstreamsOverseasPublic, []string{
		"https://cloudflare-dns.com/dns-query",
		"https://dns.google/dns-query",
		"https://dns.quad9.net/dns-query",
		"1.1.1.1",
		"1.0.0.1",
		"8.8.8.8",
		"8.8.4.4",
		"9.9.9.9",
		"22.22.22.22",
	})
	assertEqualStrings(t, cfg.DNS.BackendResolvers, []string{
		"1.1.1.1:53",
		"1.0.0.1:53",
		"8.8.8.8:53",
		"8.8.4.4:53",
		"9.9.9.9:53",
		"22.22.22.22:53",
	})
	if cfg.Routing.FallbackExit != "direct" {
		t.Fatalf("fallback_exit = %q, want direct", cfg.Routing.FallbackExit)
	}
	if cfg.Logging.Level != "info" {
		t.Fatalf("logging.level = %q, want info", cfg.Logging.Level)
	}
	if !cfg.Logging.AccessEnabled() {
		t.Fatal("logging access should default to true")
	}
	if cfg.Network.TCPRedirectPort != 18082 {
		t.Fatalf("tcp_redirect_port = %d, want 18082", cfg.Network.TCPRedirectPort)
	}
	if cfg.Network.QUICPolicy != "reject" {
		t.Fatalf("quic_policy = %q, want reject", cfg.Network.QUICPolicy)
	}
	if cfg.Network.EncryptedDNSPolicy != "reject" {
		t.Fatalf("encrypted_dns_policy = %q, want reject", cfg.Network.EncryptedDNSPolicy)
	}
	if cfg.Network.HAProxyMaxConnections == nil || *cfg.Network.HAProxyMaxConnections != DefaultHAProxyMaxConnections {
		t.Fatalf("haproxy_max_connections = %v, want %d", cfg.Network.HAProxyMaxConnections, DefaultHAProxyMaxConnections)
	}
}

func TestApplyDefaultsPreservesAutomaticHAProxyMaxConnections(t *testing.T) {
	cfg := validConfig()
	automatic := 0
	cfg.Network.HAProxyMaxConnections = &automatic
	cfg.ApplyDefaults()
	if cfg.Network.HAProxyMaxConnections == nil || *cfg.Network.HAProxyMaxConnections != 0 {
		t.Fatalf("haproxy_max_connections = %v, want explicit automatic mode", cfg.Network.HAProxyMaxConnections)
	}
}

func TestValidateRejectsNegativeHAProxyMaxConnections(t *testing.T) {
	cfg := validConfig()
	invalid := -1
	cfg.Network.HAProxyMaxConnections = &invalid
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative haproxy_max_connections to be rejected")
	}
}

func TestValidateRejectsInvalidLoggingLevel(t *testing.T) {
	cfg := validConfig()
	cfg.Logging.Level = "trace"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid logging level to be rejected")
	}
}

func TestValidateRequiresHTTPSIOSOriginWhenEnabled(t *testing.T) {
	for _, value := range []string{"http://dot.example.com", "https://user@dot.example.com", "https://dot.example.com/profile", "https://dot.example.com?token=x"} {
		cfg := validConfig()
		cfg.IOS.Enabled = true
		cfg.IOS.BaseURL = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected ios.base_url %q to be rejected", value)
		}
	}
	cfg := validConfig()
	cfg.IOS.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid HTTPS iOS origin rejected: %v", err)
	}
}

func TestApplyDefaultsMigratesLegacyIOSAddress(t *testing.T) {
	cfg := validConfig()
	cfg.IOS.BaseURL = "http://10.0.0.1:8088"
	cfg.ApplyDefaults()
	if cfg.IOS.BaseURL != "https://dot.example.com" {
		t.Fatalf("ios.base_url = %q", cfg.IOS.BaseURL)
	}

	cfg.IOS.BaseURL = "https://profiles.example.net"
	cfg.ApplyDefaults()
	if cfg.IOS.BaseURL != "https://profiles.example.net" {
		t.Fatalf("custom ios.base_url changed to %q", cfg.IOS.BaseURL)
	}
}

func TestApplyDefaultsMigratesOnlyLegacyCNUpstreams(t *testing.T) {
	for _, legacy := range [][]string{
		{"180.76.76.76", "101.226.4.6", "218.30.118.6"},
		{
			"180.76.76.76", "101.226.4.6", "218.30.118.6",
			"114.114.114.114", "114.114.115.115", "117.50.10.10", "52.80.66.66",
		},
	} {
		cfg := validConfig()
		cfg.DNS.UpstreamsCN = append([]string(nil), legacy...)
		cfg.ApplyDefaults()
		assertEqualStrings(t, cfg.DNS.UpstreamsCN, appendMissing(legacy, defaultCNUpstreams()))
		cfg.ApplyDefaults()
		assertEqualStrings(t, cfg.DNS.UpstreamsCN, appendMissing(legacy, defaultCNUpstreams()))
	}

	cfg := validConfig()
	custom := []string{"192.0.2.53", "198.51.100.53"}
	cfg.DNS.UpstreamsCN = append([]string(nil), custom...)
	cfg.ApplyDefaults()
	assertEqualStrings(t, cfg.DNS.UpstreamsCN, custom)
}

func TestValidateRejectsInvalidQUICPolicy(t *testing.T) {
	cfg := validConfig()
	cfg.Network.QUICPolicy = "auto"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid quic policy to be rejected")
	}
}

func TestValidateRejectsInvalidEncryptedDNSPolicy(t *testing.T) {
	cfg := validConfig()
	cfg.Network.EncryptedDNSPolicy = "block"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid encrypted DNS policy to be rejected")
	}
}

func TestApplyDefaultsSetsShadowsocksDefaults(t *testing.T) {
	cfg := validConfig()
	cfg.Exits = append(cfg.Exits, ExitConfig{
		Name:          "ss1",
		Type:          "shadowsocks-rust",
		Server:        "198.51.100.10",
		ServerPort:    8388,
		Password:      "MTIzNDU2Nzg5MGFiY2RlZg==",
		ListenAddress: "127.0.0.1",
		ListenPort:    1080,
	})
	cfg.ApplyDefaults()
	if cfg.Exits[1].Method != "2022-blake3-aes-128-gcm" {
		t.Fatalf("method = %q", cfg.Exits[1].Method)
	}
	if cfg.Exits[1].TimeoutSeconds != 300 {
		t.Fatalf("timeout_seconds = %d", cfg.Exits[1].TimeoutSeconds)
	}
	if cfg.Exits[1].SSRustMode() != "tcp_and_udp" {
		t.Fatalf("mode = %q", cfg.Exits[1].SSRustMode())
	}
}

func TestLoadRejectsOldShadowsocksFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	text := `[network]
gateway_ip = "10.0.0.1"
internal_cidr = "172.22.0.0/16"
ingress_iface = "eth0"

[[exits]]
name = "ss1"
type = "shadowsocks-rust"
server = "198.51.100.10"
server_port = 8388
password = "MTIzNDU2Nzg5MGFiY2RlZg=="
local_address = "127.0.0.1"
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected old local_address field to be rejected")
	}
}

func TestValidateRejectsDisabledTCPAndUDP(t *testing.T) {
	cfg := validConfig()
	tcp, udp := false, false
	exit := validSSExit()
	exit.TCP = &tcp
	exit.UDP = &udp
	cfg.Exits = append(cfg.Exits, exit)
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected tcp=false and udp=false to be rejected")
	}
}

func TestValidatePasswordErrorIncludesOpenSSLHint(t *testing.T) {
	cfg := validConfig()
	exit := validSSExit()
	exit.Password = ""
	cfg.Exits = append(cfg.Exits, exit)
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected missing password to be rejected")
	}
	if !strings.Contains(err.Error(), "openssl rand -base64 16") {
		t.Fatalf("password error lacks openssl hint: %v", err)
	}
}

func TestValidateRejectsUnknownFallbackExit(t *testing.T) {
	cfg := validConfig()
	cfg.Routing.FallbackExit = "missing"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected unknown fallback exit to be rejected")
	}
}

func TestValidateRejectsTCPDisabledFallbackExit(t *testing.T) {
	cfg := validConfig()
	tcp, udp := false, true
	exit := validSSExit()
	exit.TCP = &tcp
	exit.UDP = &udp
	cfg.Exits = append(cfg.Exits, exit)
	cfg.Routing.FallbackExit = exit.Name
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected tcp-disabled fallback exit to be rejected")
	}
}

func validSSExit() ExitConfig {
	return ExitConfig{
		Name:           "ss1",
		Type:           "shadowsocks-rust",
		Server:         "198.51.100.10",
		ServerPort:     8388,
		Method:         "aes-256-gcm",
		Password:       "change-me",
		ListenAddress:  "127.0.0.1",
		ListenPort:     1080,
		TimeoutSeconds: 300,
	}
}

func validConfig() Config {
	cfg := Config{
		Network: NetworkConfig{
			GatewayIP:    "10.0.0.1",
			InternalCIDR: "10.0.0.0/24",
			IngressIface: "eth0",
		},
		DNS: DNSConfig{
			DOTDomain: "dot.example.com",
		},
		Exits: []ExitConfig{{Name: "direct", Type: "direct"}},
	}
	cfg.ApplyDefaults()
	return cfg
}

func assertEqualStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("strings = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("strings = %#v, want %#v", got, want)
		}
	}
}
