package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	cfgText, _ := wizardWithDefaults(strings.NewReader(""), &out, true, wizardDefaults{
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
	cfgText, _ := wizardWithDefaults(strings.NewReader("\n\n\nn\n"), &out, false, wizardDefaults{
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
	path := filepath.Join(t.TempDir(), "config.toml")
	text := `[network]
gateway_ip = "10.0.0.1"
internal_cidr = "172.22.0.0/16"
ingress_iface = "eth0"

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
