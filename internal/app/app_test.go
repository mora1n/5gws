package app

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	for _, want := range []string{"Usage:", "Main:", "Client:", "Runtime/debug:", "install", "ios-link"} {
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

func TestWizardUsesDetectedDefaults(t *testing.T) {
	var out bytes.Buffer
	cfgText, _ := wizardWithDefaults(bufio.NewReader(strings.NewReader("")), &out, true, wizardDefaults{
		GatewayIP:    "172.22.1.2",
		InternalCIDR: "172.22.0.0/16",
		IngressIface: "eth1",
	})
	for _, want := range []string{
		`gateway_ip = "172.22.1.2"`,
		`internal_cidr = "172.22.0.0/16"`,
		`ingress_iface = "eth1"`,
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
	cfgText, _ := wizardWithDefaults(bufio.NewReader(strings.NewReader("\n\n\nn\n")), &out, false, wizardDefaults{
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
	_, _, generated, err := loadOrWizard(cfgPath, rulesPath, bufio.NewReader(strings.NewReader("")), &out, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if generated.config != "" || generated.rules != "" {
		t.Fatalf("existing inputs should not be regenerated: %#v", generated)
	}
	if !strings.Contains(out.String(), "using existing") || !strings.Contains(out.String(), "--reconfigure") {
		t.Fatalf("missing reuse hint:\n%s", out.String())
	}
}

func TestLoadOrWizardReconfigureRegeneratesInputs(t *testing.T) {
	cfgPath, rulesPath := writeInstallInputs(t, t.TempDir(), false)
	var out bytes.Buffer
	_, _, generated, err := loadOrWizard(cfgPath, rulesPath, bufio.NewReader(strings.NewReader("192.0.2.10\n172.22.0.0/16\neth9\ny\n")), &out, false, true)
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

func TestInstallWizardAndConfirmShareReader(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	rulesPath := filepath.Join(t.TempDir(), "rules.toml")
	var out bytes.Buffer
	err := cmdInstall([]string{"--dry-run", "--config", cfgPath, "--rules", rulesPath}, strings.NewReader("192.0.2.10\n172.22.0.0/16\neth9\ny\ny\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry-run: no files or services changed") {
		t.Fatalf("install did not complete after confirmation:\n%s", out.String())
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
	for _, want := range []string{"iOS certificate/profile links:", "cert:", "profile:", "CA certificate QR:", "iOS profile QR:"} {
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

func TestIOSLinkNoQRPrintsLinksOnly(t *testing.T) {
	cfgPath := writeIOSLinkConfig(t)
	out := runIOSLink(t, "--config", cfgPath, "--out", t.TempDir(), "--no-qr")
	if !strings.Contains(out, "cert: http://10.0.0.1:8088/5gws-ca.crt") {
		t.Fatalf("links missing:\n%s", out)
	}
	if strings.Contains(out, "CA certificate QR") || strings.Contains(out, "iOS profile QR") {
		t.Fatalf("--no-qr printed terminal QR:\n%s", out)
	}
}

func TestIOSLinkPrintsTerminalQRByDefault(t *testing.T) {
	cfgPath := writeIOSLinkConfig(t)
	out := runIOSLink(t, "--config", cfgPath, "--out", t.TempDir())
	for _, want := range []string{"cert:", "profile:", "CA certificate QR:", "iOS profile QR:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ios-link output missing %q:\n%s", want, out)
		}
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
