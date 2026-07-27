package rules

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestFileJSONUsesLowercaseKeys(t *testing.T) {
	data, err := json.Marshal(ManagedFile())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{`"Imports"`, `"Rules"`, `"Name"`, `"DNSPool"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("JSON contains legacy key %s: %s", forbidden, text)
		}
	}
	for _, required := range []string{`"imports"`, `"rules"`, `"name"`, `"dns_pool"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("JSON missing lowercase key %s: %s", required, text)
		}
	}
}

func TestFileJSONAcceptsLegacyUppercaseKeys(t *testing.T) {
	var file File
	legacy := `{"Imports":[{"name":"legacy","type":"sing-box","url":"https://example.com/rules.json","exit":"direct"}],"Rules":[{"name":"local","exit":"direct","domain_suffix":"example.com"}]}`
	if err := json.Unmarshal([]byte(legacy), &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Imports) != 1 || file.Imports[0].Name != "legacy" || len(file.Rules) != 1 || file.Rules[0].Name != "local" {
		t.Fatalf("decoded file = %+v", file)
	}
}

func TestFileJSONRejectsAmbiguousAndUnknownKeys(t *testing.T) {
	for _, input := range []string{
		`{"Rules":[],"rules":[]}`,
		`{"Imports":[],"imports":[]}`,
		`{"rules":[],"unexpected":[]}`,
	} {
		var file File
		if err := json.Unmarshal([]byte(input), &file); err == nil {
			t.Fatalf("json.Unmarshal(%s) succeeded", input)
		}
	}
}

func TestEnsureManagedAddsDefaultsAndPreservesCustomRules(t *testing.T) {
	custom := Rule{Name: "uhd", Exit: "direct", DomainSuffix: []string{"uhdnow.com"}}
	got := EnsureManaged(File{Rules: []Rule{custom}})
	if err := ValidateManaged(got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rules) != 2 || !reflect.DeepEqual(got.Rules[0], custom) {
		t.Fatalf("custom rule was not preserved: %+v", got.Rules)
	}
	if len(got.Imports) != 3 {
		t.Fatalf("imports = %+v", got.Imports)
	}
}

func TestOptionalDefaultRulesAreNotManaged(t *testing.T) {
	file := EnsureOptionalDefaults(ManagedFile())
	defaults := []Rule{DefaultNeteaseRule(), DefaultUnicomRule()}
	for _, defaultRule := range defaults {
		if !containsRuleName(file.Rules, defaultRule.Name) {
			t.Fatalf("optional defaults did not add %q", defaultRule.Name)
		}
	}
	file.Rules = slices.DeleteFunc(file.Rules, func(rule Rule) bool {
		return rule.Name == defaults[0].Name || rule.Name == defaults[1].Name
	})
	file = EnsureManaged(file)
	for _, defaultRule := range defaults {
		if containsRuleName(file.Rules, defaultRule.Name) {
			t.Fatalf("EnsureManaged restored optional rule %q", defaultRule.Name)
		}
	}
}

func TestManagedFileSurvivesTOMLRoundTrip(t *testing.T) {
	data, err := toml.Marshal(ManagedFile())
	if err != nil {
		t.Fatal(err)
	}
	var decoded File
	if err := toml.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManaged(decoded); err != nil {
		t.Fatalf("validate managed rules after TOML round trip: %v\n%s", err, data)
	}
}

func TestValidateManagedRejectsMissingModifiedDuplicateAndCollision(t *testing.T) {
	valid := EnsureManaged(File{Rules: []Rule{{Name: "uhd", Exit: "direct", DomainSuffix: []string{"uhdnow.com"}}}})
	if err := ValidateManaged(valid); err != nil {
		t.Fatalf("valid managed file rejected: %v", err)
	}
	tests := map[string]File{
		"missing":         {Rules: valid.Rules[:1], Imports: valid.Imports},
		"modified-rule":   {Rules: append([]Rule(nil), valid.Rules...), Imports: valid.Imports},
		"modified-import": {Rules: valid.Rules, Imports: append([]Import(nil), valid.Imports...)},
		"duplicate":       {Rules: append(append([]Rule(nil), valid.Rules...), valid.Rules[1]), Imports: valid.Imports},
		"collision":       {Rules: append(append([]Rule(nil), valid.Rules...), Rule{Name: "gfw", Exit: "direct", DomainSuffix: []string{"example.com"}}), Imports: valid.Imports},
	}
	tests["modified-rule"].Rules[1].Exit = ""
	tests["modified-import"].Imports[0].URL = "https://example.com/changed.json"
	for name, file := range tests {
		t.Run(name, func(t *testing.T) {
			if err := ValidateManaged(file); err == nil {
				t.Fatal("expected managed validation failure")
			}
		})
	}
}
