package app

import (
	"bytes"
	"strings"
	"testing"
)

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
	} {
		if !strings.Contains(cfgText, want) {
			t.Fatalf("missing %q in:\n%s", want, cfgText)
		}
	}
	if !strings.Contains(out.String(), "ingress interface: eth1") {
		t.Fatalf("assume-yes output did not show detected iface:\n%s", out.String())
	}
}

func TestParseDefaultIface(t *testing.T) {
	output := "default via 192.0.2.1 dev eth9 proto dhcp src 192.0.2.10 metric 100\n"
	if got := parseDefaultIface(output); got != "eth9" {
		t.Fatalf("iface = %q, want eth9", got)
	}
}
