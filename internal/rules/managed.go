package rules

import (
	"fmt"
	"reflect"
)

func ManagedFile() File {
	return File{
		Rules: []Rule{{
			Name:         "ip-check",
			Exit:         "direct",
			DomainSuffix: []string{"icanhazip.com", "ipinfo.io", "ippure.com"},
		}},
		Imports: []Import{
			{Name: "speedtest", Type: "sing-box", URL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/category-speedtest.json", Exit: "direct"},
			{Name: "cn", Type: "sing-box", URL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.json", DNSPool: "cn"},
			{Name: "gfw", Type: "sing-box", URL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.json", Exit: "direct"},
		},
	}
}

func DefaultNeteaseRule() Rule {
	return Rule{
		Name:         "netease-music",
		DNSPool:      "cn_netease",
		DomainSuffix: []string{"music.163.com", "music.126.net", "iplay.163.com", "look.163.com", "y.163.com"},
	}
}

func EnsureOptionalDefaults(file File) File {
	out := File{
		Rules:   append([]Rule(nil), file.Rules...),
		Imports: append([]Import(nil), file.Imports...),
	}
	defaultRule := DefaultNeteaseRule()
	if !containsRuleName(out.Rules, defaultRule.Name) && !containsImportName(out.Imports, defaultRule.Name) {
		out.Rules = append(out.Rules, defaultRule)
	}
	return out
}

func EnsureManaged(file File) File {
	out := File{
		Rules:   append([]Rule(nil), file.Rules...),
		Imports: append([]Import(nil), file.Imports...),
	}
	managed := ManagedFile()
	for _, expected := range managed.Rules {
		if !containsRuleName(out.Rules, expected.Name) && !containsImportName(out.Imports, expected.Name) {
			out.Rules = append(out.Rules, expected)
		}
	}
	for _, expected := range managed.Imports {
		if !containsImportName(out.Imports, expected.Name) && !containsRuleName(out.Rules, expected.Name) {
			out.Imports = append(out.Imports, expected)
		}
	}
	return out
}

func ValidateManaged(file File) error {
	managed := ManagedFile()
	for _, expected := range managed.Rules {
		matches := rulesNamed(file.Rules, expected.Name)
		if len(matches) != 1 || containsImportName(file.Imports, expected.Name) {
			return fmt.Errorf("managed rule %q must appear exactly once as a local rule", expected.Name)
		}
		if !reflect.DeepEqual(matches[0], expected) {
			return fmt.Errorf("managed rule %q is read-only and must not be modified", expected.Name)
		}
	}
	for _, expected := range managed.Imports {
		matches := importsNamed(file.Imports, expected.Name)
		if len(matches) != 1 || containsRuleName(file.Rules, expected.Name) {
			return fmt.Errorf("managed import %q must appear exactly once as a remote import", expected.Name)
		}
		if !reflect.DeepEqual(matches[0], expected) {
			return fmt.Errorf("managed import %q is read-only and must not be modified", expected.Name)
		}
	}
	return nil
}

func containsRuleName(items []Rule, name string) bool {
	return len(rulesNamed(items, name)) > 0
}

func containsImportName(items []Import, name string) bool {
	return len(importsNamed(items, name)) > 0
}

func rulesNamed(items []Rule, name string) []Rule {
	var matches []Rule
	for _, item := range items {
		if item.Name == name {
			matches = append(matches, item)
		}
	}
	return matches
}

func importsNamed(items []Import, name string) []Import {
	var matches []Import
	for _, item := range items {
		if item.Name == name {
			matches = append(matches, item)
		}
	}
	return matches
}
