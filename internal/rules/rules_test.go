package rules

import (
	"os"
	"path/filepath"
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

func TestNormalizeRejectsImportWithoutAction(t *testing.T) {
	dir := t.TempDir()
	singPath := filepath.Join(dir, "sing.json")
	mustWrite(t, singPath, `{"version":3,"rules":[{"domain_suffix":["example.cn"]}]}`)

	_, err := Normalize(File{Imports: []Import{{Name: "cn", Type: "sing-box", Path: singPath}}})
	if err == nil {
		t.Fatal("expected import without exit or dns_pool to be rejected")
	}
}

func TestMihomoUnsupportedMatcherErrors(t *testing.T) {
	err := addMihomoPayload(&Rule{}, "PROCESS-NAME,curl")
	if err == nil {
		t.Fatal("expected unsupported matcher error")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
