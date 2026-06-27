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
	cfg.ApplyDefaults()
	if cfg.DNS.Binary != "smartdns" {
		t.Fatalf("dns binary = %q, want smartdns", cfg.DNS.Binary)
	}
	if cfg.DNS.ListenUDP != "127.0.0.1:1053" {
		t.Fatalf("listen_udp = %q", cfg.DNS.ListenUDP)
	}
	if len(cfg.DNS.UpstreamsCN) == 0 {
		t.Fatal("expected default CN upstreams")
	}
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
