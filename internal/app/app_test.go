package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/store"
)

func TestHelpAndUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if err := Run(nil, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"install", "reset-admin", "status", "export"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q", want)
		}
	}
	if !strings.Contains(out.String(), "state is kept unless --purge is used") || !strings.Contains(out.String(), "run apply to activate") {
		t.Fatalf("help does not explain uninstall/import lifecycle:\n%s", out.String())
	}
	if strings.Contains(out.String(), "rollback") {
		t.Fatal("help still exposes removed revision rollback")
	}
	if err := Run([]string{"bot"}, strings.NewReader(""), &out, &out); err == nil {
		t.Fatal("removed bot command was accepted")
	}
}

func TestPrintAdminCredentialsShowsUsernameAndPassword(t *testing.T) {
	var out bytes.Buffer
	printAdminCredentials(&out, "admin", "secret-password")

	for _, want := range []string{"管理员账号", "Username: admin", "Password: secret-password"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("admin credentials output missing %q:\n%s", want, out.String())
		}
	}
}

func TestLoadExistingBundlePreservesCustomExit(t *testing.T) {
	stateDir := t.TempDir()
	state, err := store.Open(filepath.Join(stateDir, "5gws.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := installConfig("10.0.0.1", "172.22.0.0/16", "eth0", "dot.example.com", "", false)
	cfg.Exits = append(cfg.Exits, config.ExitConfig{
		Name: "uzu.bo", Type: "shadowsocks-rust", Server: "203.0.113.20", ServerPort: 27080,
		Method: "aes-128-gcm", Password: "change-me", ListenAddress: "127.0.0.1", ListenPort: 1080,
	})
	if _, err := state.Initialize(context.Background(), store.Bundle{Config: cfg}); err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadExistingBundle(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || len(loaded.Config.Exits) != 2 || loaded.Config.Exits[1].Name != "uzu.bo" {
		t.Fatalf("loaded bundle lost custom exit: %+v", loaded)
	}
}

func TestValidateInstallIdentityRejectsChangedInput(t *testing.T) {
	existing := installConfig("10.0.0.1", "172.22.0.0/16", "eth0", "dot.example.com", "", false)
	requested := installConfig("10.0.0.2", "172.22.0.0/16", "eth0", "dot.example.com", "", false)
	if err := validateInstallIdentity(requested, existing); err == nil || !strings.Contains(err.Error(), "gateway IP") {
		t.Fatalf("expected gateway mismatch, got %v", err)
	}
}

func TestInstallUsesOneServiceUnit(t *testing.T) {
	if strings.Count(systemdUnit, "[Service]") != 1 || !strings.Contains(systemdUnit, "5gws daemon") {
		t.Fatalf("invalid daemon unit:\n%s", systemdUnit)
	}
	for _, removed := range []string{"5gws-smartdns.service", "5gws-haproxy.service", "5gws-quic.service", "5gws-bot.service"} {
		if strings.Contains(systemdUnit, removed) {
			t.Fatalf("unit references removed child service %q", removed)
		}
	}
}

func TestInstallConfigUsesLocalPanelAndDisablesIOSByDefault(t *testing.T) {
	cfg := installConfig("203.0.113.10", "172.22.0.0/16", "eth0", "DNS.Example.COM.", "", false)

	if cfg.Panel.Listen != "127.0.0.1:19443" {
		t.Fatalf("panel listen = %q", cfg.Panel.Listen)
	}
	if cfg.IOS.Enabled {
		t.Fatal("iOS profile should be disabled by default")
	}
	if cfg.DNS.DOTDomain != "dns.example.com" {
		t.Fatalf("DoT domain = %q", cfg.DNS.DOTDomain)
	}
	if cfg.IOS.BaseURL != "https://dns.example.com" {
		t.Fatalf("iOS base URL = %q", cfg.IOS.BaseURL)
	}
}

func TestInstallConfigCanEnableIOS(t *testing.T) {
	cfg := installConfig("203.0.113.10", "172.22.0.0/16", "eth0", "dns.example.com", "", true)
	if !cfg.IOS.Enabled {
		t.Fatal("iOS profile should be enabled when requested")
	}
}

func TestInstallConfigCanOverridePanelListen(t *testing.T) {
	cfg := installConfig("203.0.113.10", "172.22.0.0/16", "eth0", "dns.example.com", "127.0.0.1:18000", false)
	if cfg.Panel.Listen != "127.0.0.1:18000" {
		t.Fatalf("panel listen = %q", cfg.Panel.Listen)
	}
}

func TestMissingInstallFlags(t *testing.T) {
	missing := missingInstallFlags(installOptions{})
	want := []string{"--gateway-ip", "--internal-cidr", "--ingress-iface", "--dot-domain"}
	if strings.Join(missing, ",") != strings.Join(want, ",") {
		t.Fatalf("missing flags = %v, want %v", missing, want)
	}

	complete := installOptions{
		gatewayIP: "203.0.113.10", internalCIDR: "172.22.0.0/16",
		ingressIface: "eth0", dotDomain: "dns.example.com",
	}
	if missing := missingInstallFlags(complete); len(missing) != 0 {
		t.Fatalf("unexpected missing flags: %v", missing)
	}
}

func TestInteractiveInstallOptionsUsesDefaultsAndExplainsInputs(t *testing.T) {
	opts := installOptions{}
	var out bytes.Buffer
	input := strings.NewReader("\n\n\ndns.example.com\n")

	if err := opts.collect(input, &out); err != nil {
		t.Fatal(err)
	}
	if opts.gatewayIP != "10.0.0.1" || opts.internalCIDR != "172.22.0.0/16" {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
	if opts.ingressIface == "" || opts.dotDomain != "dns.example.com" {
		t.Fatalf("unexpected collected values: %+v", opts)
	}
	for _, want := range []string{
		"命中分流规则时返回给客户端的服务器地址",
		"允许使用此网关的客户端来源 CIDR",
		"接收客户端流量的服务器网络接口",
		"用于 DNS over TLS 和面板证书",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("prompt output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "additional management CIDR") {
		t.Fatal("prompt still contains the removed management CIDR question")
	}
}

func TestInteractiveInstallOptionsRetriesRequiredDomain(t *testing.T) {
	opts := installOptions{}
	var out bytes.Buffer
	input := strings.NewReader("\n\n\n\ndns.example.com\n")

	if err := opts.collect(input, &out); err != nil {
		t.Fatal(err)
	}
	if opts.dotDomain != "dns.example.com" {
		t.Fatalf("DoT domain = %q", opts.dotDomain)
	}
	if !strings.Contains(out.String(), "DoT 域名不能为空，请重新输入") {
		t.Fatalf("missing required-value message:\n%s", out.String())
	}
}

func TestInteractiveInstallOptionsRejectsMissingDomainAtEOF(t *testing.T) {
	opts := installOptions{}
	err := opts.collect(strings.NewReader("\n\n\n"), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "DoT 域名为必填项且输入已结束") {
		t.Fatalf("expected explicit domain EOF error, got %v", err)
	}
}

func TestNonInteractiveInstallOptionsRequiresEveryValue(t *testing.T) {
	opts := installOptions{
		nonInteractive: true,
		gatewayIP:      "203.0.113.10",
		internalCIDR:   "172.22.0.0/16",
		ingressIface:   "eth0",
	}
	err := opts.collect(strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--dot-domain") {
		t.Fatalf("expected missing --dot-domain error, got %v", err)
	}
}

func TestPrintInstallSummaryShowsLocalPanelAndIOSState(t *testing.T) {
	cfg := installConfig("203.0.113.10", "172.22.0.0/16", "eth0", "dns.example.com", "", false)
	var out bytes.Buffer
	printInstallSummary(&out, cfg, 3)

	for _, want := range []string{"http://127.0.0.1:19443", "iOS Profile: 关闭", "初始规则:   3"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
	}
}

func TestEnsureSmartDNSLogDirDryRunExplainsAction(t *testing.T) {
	var out bytes.Buffer
	if err := ensureSmartDNSLogDir(true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/var/log/smartdns") {
		t.Fatalf("dry-run output missing log dir:\n%s", out.String())
	}
}
