package app

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morain/5gws/internal/config"
)

func TestHelpAliases(t *testing.T) {
	empty := runApp(t)
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		got := runApp(t, args...)
		if got != empty {
			t.Fatalf("%v help mismatch\nwant:\n%s\n got:\n%s", args, empty, got)
		}
	}
	for _, want := range []string{"Usage:", "Main:", "Client:", "Runtime/debug:", "install", "ios-link", "logs", "detect-cidr"} {
		if !strings.Contains(empty, want) {
			t.Fatalf("help missing %q:\n%s", want, empty)
		}
	}
}

func TestUnknownCommandSuggestsHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run([]string{"wat"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil {
		t.Fatal("expected unknown command error")
	}
	if !strings.Contains(err.Error(), "Run '5gws --help' for usage.") {
		t.Fatalf("missing help hint: %v", err)
	}
}

func TestCommandFlagsAcceptShortAliasesAndDoubleDashLongFlags(t *testing.T) {
	fs := newCommandFlags("logs")
	fs.SetOutput(&bytes.Buffer{})
	component := fs.String("component", "m", "all", "")
	lines := fs.Int("lines", "n", 200, "")
	follow := fs.Bool("follow", "f", false, "")
	if err := fs.parse([]string{"--follow", "-m", "haproxy", "-n", "20"}); err != nil {
		t.Fatal(err)
	}
	if !*follow || *component != "haproxy" || *lines != 20 {
		t.Fatalf("parsed flags = follow:%t component:%q lines:%d", *follow, *component, *lines)
	}
}

func TestCommandFlagsRejectSingleDashLongFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "with alias", args: []string{"-follow"}, want: "long flag -follow must use --follow or -f"},
		{name: "with alias and value", args: []string{"-config=/tmp/config.toml"}, want: "long flag -config must use --config or -c"},
		{name: "without alias", args: []string{"-skip-bot-restart"}, want: "long flag -skip-bot-restart must use --skip-bot-restart"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newCommandFlags("apply")
			fs.SetOutput(&bytes.Buffer{})
			fs.String("config", "c", defaultConfigPath, "")
			fs.Bool("follow", "f", false, "")
			fs.Bool("skip-bot-restart", "", false, "")
			err := fs.parse(tc.args)
			if err == nil {
				t.Fatal("expected single-dash long flag error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLogsRejectsSingleDashFollowBeforeLoadingConfig(t *testing.T) {
	var out bytes.Buffer
	err := cmdLogs([]string{"-follow"}, &out)
	if err == nil {
		t.Fatal("expected -follow to fail")
	}
	if !strings.Contains(err.Error(), "long flag -follow must use --follow or -f") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWizardUsesDetectedDefaults(t *testing.T) {
	var out bytes.Buffer
	cfgText, _ := wizardWithDefaults(bufio.NewReader(strings.NewReader("")), &out, true, wizardDefaults{
		GatewayIP:    "172.22.1.2",
		InternalCIDR: "172.22.0.0/16",
		IngressIface: "eth1",
		DOTDomain:    "dot.example.com",
	})
	for _, want := range []string{
		`gateway_ip = "172.22.1.2"`,
		`internal_cidr = "172.22.0.0/16"`,
		`ingress_iface = "eth1"`,
		`dot_domain = "dot.example.com"`,
		`[logging]`,
		`access = true`,
		`enabled = true`,
		`base_url = "http://172.22.1.2:8088"`,
	} {
		if !strings.Contains(cfgText, want) {
			t.Fatalf("missing %q in:\n%s", want, cfgText)
		}
	}
	if !strings.Contains(out.String(), "ingress interface: eth1") {
		t.Fatalf("assume-yes output did not show detected iface:\n%s", out.String())
	}
}

func TestWizardCanDisableAppleFlow(t *testing.T) {
	var out bytes.Buffer
	cfgText, _ := wizardWithDefaults(bufio.NewReader(strings.NewReader("\n\n\ndot.example.com\nn\n")), &out, false, wizardDefaults{
		GatewayIP:    "172.22.1.2",
		InternalCIDR: "172.22.0.0/16",
		IngressIface: "eth1",
	})
	if !strings.Contains(cfgText, "enabled = false") {
		t.Fatalf("Apple flow was not disabled:\n%s", cfgText)
	}
	if strings.Contains(cfgText, "base_url") {
		t.Fatalf("disabled Apple flow should not render base_url:\n%s", cfgText)
	}
}

func TestLoadOrWizardReusesExistingConfig(t *testing.T) {
	cfgPath, rulesPath := writeInstallInputs(t, t.TempDir(), true)
	var out bytes.Buffer
	_, _, generated, err := loadOrWizard(cfgPath, rulesPath, bufio.NewReader(strings.NewReader("\n\n\n\n\n")), &out, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if generated.config == "" || generated.rules != "" {
		t.Fatalf("existing config should be regenerated with defaults and rules kept: %#v", generated)
	}
	if !strings.Contains(out.String(), "using existing") || !strings.Contains(out.String(), "DoT domain [dot.example.com]") {
		t.Fatalf("missing reuse hint:\n%s", out.String())
	}
}

func TestLoadOrWizardReconfigureRegeneratesInputs(t *testing.T) {
	cfgPath, rulesPath := writeInstallInputs(t, t.TempDir(), false)
	var out bytes.Buffer
	_, _, generated, err := loadOrWizard(cfgPath, rulesPath, bufio.NewReader(strings.NewReader("192.0.2.10\n172.22.0.0/16\neth9\ndot2.example.com\ny\n")), &out, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(generated.config, `gateway_ip = "192.0.2.10"`) {
		t.Fatalf("reconfigure did not regenerate config:\n%s", generated.config)
	}
	if generated.rules == "" {
		t.Fatal("reconfigure did not regenerate rules")
	}
	if !strings.Contains(out.String(), "reconfigure requested") {
		t.Fatalf("missing reconfigure message:\n%s", out.String())
	}
}

func TestPrintIOSInstallHint(t *testing.T) {
	cfgPath := writeIOSLinkConfigInDir(t, t.TempDir())
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printIOSInstallHint(&out, cfg, cfgPath, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"iOS profile links:", "profile:", "iOS profile QR:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("iOS install hint missing %q:\n%s", want, out.String())
		}
	}
}

func TestPrintIOSInstallHintDisabled(t *testing.T) {
	cfg := config.Config{IOS: config.IOSConfig{Enabled: false}}
	var out bytes.Buffer
	if err := printIOSInstallHint(&out, cfg, "/etc/5gws/config.toml", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "iOS profile flow disabled") {
		t.Fatalf("missing disabled hint:\n%s", out.String())
	}
}

func TestParseDefaultIface(t *testing.T) {
	output := "default via 192.0.2.1 dev eth9 proto dhcp src 192.0.2.10 metric 100\n"
	if got := parseDefaultIface(output); got != "eth9" {
		t.Fatalf("iface = %q, want eth9", got)
	}
}

func TestLogServicesSelectsComponents(t *testing.T) {
	cfg := config.Config{Exits: []config.ExitConfig{{Name: "ss1", Type: "shadowsocks-rust"}}}
	services, err := logServices(cfg, "ssrust")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(services, ",") != "5gws-ssrust-ss1.service" {
		t.Fatalf("services = %#v", services)
	}
	if _, err := logServices(cfg, "wat"); err == nil {
		t.Fatal("expected unknown component to fail")
	}
}

func TestNormalizeJournalSince(t *testing.T) {
	now := time.Date(2026, 6, 27, 14, 45, 0, 0, time.Local)
	cases := map[string]string{
		"5m":             "2026-06-27 14:40:00",
		"1h30m":          "2026-06-27 13:15:00",
		"10 minutes ago": "10 minutes ago",
		"":               "",
	}
	for input, want := range cases {
		if got := normalizeJournalSince(input, now); got != want {
			t.Fatalf("normalizeJournalSince(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseTCPDumpSources(t *testing.T) {
	output := `1719460000.1 IP 172.22.1.23.51324 > 177.0.143.3.853: Flags [S]
1719460000.2 IP 100.80.2.3.53000 > 177.0.143.3.443: UDP
1719460000.3 IP6 fe80::1.443 > fe80::2.443: UDP
`
	got := strings.Join(parseTCPDumpSources(output), ",")
	if got != "100.80.2.3,172.22.1.23" {
		t.Fatalf("sources = %q", got)
	}
	cidrs := strings.Join(suggestCIDRs(parseTCPDumpSources(output)), ",")
	if cidrs != "100.64.0.0/10,172.22.0.0/16" {
		t.Fatalf("cidrs = %q", cidrs)
	}
}

func TestIOSLinkNoQRPrintsLinksOnly(t *testing.T) {
	cfgPath := writeIOSLinkConfig(t)
	out := runIOSLink(t, "--config", cfgPath, "--out", t.TempDir(), "--no-qr")
	if !strings.Contains(out, "profile: http://10.0.0.1:8088/5gws-dot.mobileconfig") {
		t.Fatalf("links missing:\n%s", out)
	}
	if strings.Contains(out, "iOS profile QR") {
		t.Fatalf("--no-qr printed terminal QR:\n%s", out)
	}
}

func TestIOSLinkPrintsTerminalQRByDefault(t *testing.T) {
	cfgPath := writeIOSLinkConfig(t)
	out := runIOSLink(t, "--config", cfgPath, "--out", t.TempDir())
	for _, want := range []string{"profile:", "iOS profile QR:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ios-link output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderPrintsImportWarnings(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeIOSLinkConfigInDir(t, dir)
	rulesPath := filepath.Join(dir, "rules.toml")
	importPath := filepath.Join(dir, "ruleset.json")
	mustWriteFile(t, importPath, `{"version":2,"rules":[{"domain_suffix":["ookla.com"],"domain_regex":"^speed\\.example$"}]}`)
	mustWriteFile(t, rulesPath, `[[imports]]
name = "speedtest"
type = "sing-box"
path = "`+filepath.ToSlash(importPath)+`"
exit = "direct"
`)
	outDir := filepath.Join(dir, "rendered")
	var out bytes.Buffer
	if err := cmdRender([]string{"--config", cfgPath, "--rules", rulesPath, "--out", outDir}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "warning: skipped import speedtest") || !strings.Contains(out.String(), "domain_regex") {
		t.Fatalf("missing import warning:\n%s", out.String())
	}
	smartdns, err := os.ReadFile(filepath.Join(outDir, "smartdns", "smartdns.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(smartdns), "address /ookla.com/10.0.0.1") {
		t.Fatalf("rendered smartdns missing speedtest suffix:\n%s", smartdns)
	}
}

func runApp(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := Run(args, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	return stdout.String()
}

func runIOSLink(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := cmdIOSLink(args, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func mustWriteFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeIOSLinkConfig(t *testing.T) string {
	t.Helper()
	return writeIOSLinkConfigInDir(t, t.TempDir())
}

func writeIOSLinkConfigInDir(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "config.toml")
	text := `[network]
gateway_ip = "10.0.0.1"
internal_cidr = "172.22.0.0/16"
ingress_iface = "eth0"

[system]
state_dir = "` + filepath.ToSlash(filepath.Join(dir, "state")) + `"

[dns]
dot_domain = "dot.example.com"

[ios]
enabled = true
listen = "0.0.0.0:8088"
base_url = "http://10.0.0.1:8088"
organization = "5gws"
profile_identifier = "dev.5gws.dot"

[[exits]]
name = "direct"
type = "direct"
`
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeInstallInputs(t *testing.T, dir string, iosEnabled bool) (string, string) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.toml")
	rulesPath := filepath.Join(dir, "rules.toml")
	iosText := "[ios]\nenabled = false\n"
	if iosEnabled {
		iosText = `[ios]
enabled = true
listen = "0.0.0.0:8088"
base_url = "http://10.0.0.1:8088"
organization = "5gws"
profile_identifier = "dev.5gws.dot"
`
	}
	cfgText := `[network]
gateway_ip = "10.0.0.1"
internal_cidr = "172.22.0.0/16"
ingress_iface = "eth0"

[dns]
dot_domain = "dot.example.com"

` + iosText + `
[[exits]]
name = "direct"
type = "direct"
`
	if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, []byte(defaultRulesText()), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfgPath, rulesPath
}
