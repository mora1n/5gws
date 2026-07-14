package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeImportsSingBoxAndMihomo(t *testing.T) {
	dir := t.TempDir()
	singPath := filepath.Join(dir, "sing.json")
	mihomoPath := filepath.Join(dir, "mihomo.yaml")
	mustWrite(t, singPath, `{"version":3,"rules":[{"domain_suffix":["example.cn"]}]}`)
	mustWrite(t, mihomoPath, "payload:\n  - DOMAIN-SUFFIX,openai.com\n  - DOMAIN,example.com\n")

	norm, err := Normalize(File{
		Imports: []Import{
			{Name: "cn", Type: "sing-box", Path: singPath, DNSPool: "cn"},
			{Name: "proxy", Type: "mihomo", Path: mihomoPath, Exit: "ss1"},
		},
		Rules: []Rule{{Name: "manual", Exit: "ss1", Domain: []string{"chatgpt.com"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(norm.Rules); got != 3 {
		t.Fatalf("rules count = %d, want 3", got)
	}
	if norm.Rules[1].DomainSuffix[0] != "example.cn" {
		t.Fatalf("sing-box suffix not imported: %#v", norm.Rules[1])
	}
	if norm.Rules[1].DNSPool != "cn" || norm.Rules[1].Exit != "" {
		t.Fatalf("sing-box DNS pool import has wrong action: %#v", norm.Rules[1])
	}
	if norm.Rules[1].Name != "cn" {
		t.Fatalf("single sing-box rule name = %q, want cn", norm.Rules[1].Name)
	}
	if norm.Rules[2].Domain[0] != "example.com" {
		t.Fatalf("mihomo domain not imported: %#v", norm.Rules[2])
	}
	if got := len(norm.GatewayRules()); got != 2 {
		t.Fatalf("gateway rules count = %d, want 2", got)
	}
	if got := len(norm.DNSPoolRules()); got != 1 {
		t.Fatalf("DNS pool rules count = %d, want 1", got)
	}
}

func TestNormalizeImportsSingBoxStringMatcher(t *testing.T) {
	dir := t.TempDir()
	singPath := filepath.Join(dir, "sing.json")
	mustWrite(t, singPath, `{"version":2,"rules":[{"domain_suffix":"example.com"}]}`)

	norm, err := Normalize(File{
		Imports: []Import{{Name: "stun", Type: "sing-box", Path: singPath, Exit: "direct"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := norm.Rules[0].DomainSuffix; len(got) != 1 || got[0] != "example.com" {
		t.Fatalf("domain_suffix = %#v, want [example.com]", got)
	}
	if norm.Rules[0].Name != "stun" {
		t.Fatalf("single sing-box rule name = %q, want stun", norm.Rules[0].Name)
	}
}

func TestNormalizeKeepsIndexesForMultipleSingBoxRules(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multiple.json")
	mustWrite(t, path, `{"version":3,"rules":[{"domain_suffix":["one.example"]},{"domain_suffix":["two.example"]}]}`)

	norm, err := Normalize(File{Imports: []Import{{Name: "multiple", Type: "sing-box", Path: path, Exit: "direct"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(norm.Rules) != 2 || norm.Rules[0].Name != "multiple-1" || norm.Rules[1].Name != "multiple-2" {
		t.Fatalf("multiple sing-box rule names = %#v", norm.Rules)
	}
}

func TestNormalizeRejectsAmbiguousRuleAction(t *testing.T) {
	_, err := Normalize(File{Rules: []Rule{{
		Name:         "bad",
		Exit:         "direct",
		DNSPool:      "cn",
		DomainSuffix: []string{"example.com"},
	}}})
	if err == nil {
		t.Fatal("expected ambiguous action to be rejected")
	}
}

func TestValidateDNSPoolReferencesAcceptsCustomPool(t *testing.T) {
	file := File{Rules: []Rule{{Name: "music", DNSPool: "music_pool", DomainSuffix: []string{"music.163.com"}}}}
	if err := ValidateDNSPoolReferences(file, []string{"cn", "music_pool"}); err != nil {
		t.Fatalf("custom pool rejected: %v", err)
	}
	if err := ValidateDNSPoolReferences(file, []string{"cn"}); err == nil {
		t.Fatal("expected unknown custom pool to be rejected")
	}
}

func TestNormalizeRejectsImportWithoutAction(t *testing.T) {
	dir := t.TempDir()
	singPath := filepath.Join(dir, "sing.json")
	mustWrite(t, singPath, `{"version":3,"rules":[{"domain_suffix":["example.cn"]}]}`)

	_, err := Normalize(File{Imports: []Import{{Name: "cn", Type: "sing-box", Path: singPath}}})
	if err == nil {
		t.Fatal("expected import without exit or dns_pool to be rejected")
	}
}

func TestNormalizeSkipsUnsupportedSingBoxImportMatchers(t *testing.T) {
	dir := t.TempDir()
	singPath := filepath.Join(dir, "speedtest.json")
	mustWrite(t, singPath, `{"version":2,"rules":[
		{"domain_suffix":["ookla.com"],"domain_regex":"^speed\\.example$"},
		{"domain_regex":"^only-regex\\.example$"}
	]}`)

	norm, err := Normalize(File{
		Imports: []Import{{Name: "speedtest", Type: "sing-box", Path: singPath, Exit: "direct"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(norm.Rules); got != 1 {
		t.Fatalf("rules count = %d, want 1", got)
	}
	if got := norm.Rules[0].DomainSuffix; len(got) != 1 || got[0] != "ookla.com" {
		t.Fatalf("domain_suffix = %#v, want [ookla.com]", got)
	}
	if norm.Rules[0].Name != "speedtest" {
		t.Fatalf("filtered single rule name = %q, want speedtest", norm.Rules[0].Name)
	}
	joined := warningText(norm.Warnings)
	for _, want := range []string{"domain_regex", "rule speedtest matcher", "speedtest-2", "no supported matchers"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "speedtest-1") {
		t.Fatalf("warning kept old single rule name:\n%s", joined)
	}
}

func TestNormalizeSkipsUnsupportedMihomoImportMatchers(t *testing.T) {
	dir := t.TempDir()
	mihomoPath := filepath.Join(dir, "mihomo.yaml")
	mustWrite(t, mihomoPath, "payload:\n  - DOMAIN-SUFFIX,ookla.com\n  - DOMAIN-REGEX,^speed\\.example$\n  - IP-CIDR,192.0.2.0/24\n  - PROCESS-NAME,curl\n")

	norm, err := Normalize(File{
		Imports: []Import{{Name: "speedtest", Type: "mihomo", Path: mihomoPath, Exit: "direct"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(norm.Rules); got != 1 {
		t.Fatalf("rules count = %d, want 1", got)
	}
	if got := norm.Rules[0].DomainSuffix; len(got) != 1 || got[0] != "ookla.com" {
		t.Fatalf("domain_suffix = %#v, want [ookla.com]", got)
	}
	joined := warningText(norm.Warnings)
	for _, want := range []string{"DOMAIN-REGEX", "IP-CIDR", "PROCESS-NAME"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings missing %q:\n%s", want, joined)
		}
	}
}

func TestNormalizeRejectsMalformedMihomoPayload(t *testing.T) {
	dir := t.TempDir()
	mihomoPath := filepath.Join(dir, "mihomo.yaml")
	mustWrite(t, mihomoPath, "payload:\n  - DOMAIN-SUFFIX\n")

	_, err := Normalize(File{
		Imports: []Import{{Name: "bad", Type: "mihomo", Path: mihomoPath, Exit: "direct"}},
	})
	if err == nil {
		t.Fatal("expected malformed payload to fail")
	}
}

func warningText(warnings []Warning) string {
	var parts []string
	for _, warning := range warnings {
		parts = append(parts, warning.String())
	}
	return strings.Join(parts, "\n")
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
