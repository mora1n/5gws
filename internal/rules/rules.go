package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

type File struct {
	Imports []Import `toml:"imports" json:"imports"`
	Rules   []Rule   `toml:"rules" json:"rules"`
}

func (f *File) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, lower := fields["imports"]; lower {
		if _, legacy := fields["Imports"]; legacy {
			return errors.New(`rules file contains both "imports" and legacy "Imports"`)
		}
	}
	if _, lower := fields["rules"]; lower {
		if _, legacy := fields["Rules"]; legacy {
			return errors.New(`rules file contains both "rules" and legacy "Rules"`)
		}
	}
	for name := range fields {
		switch name {
		case "imports", "Imports", "rules", "Rules":
		default:
			return fmt.Errorf("unknown field %q in rules file", name)
		}
	}
	var decoded File
	if raw, ok := firstJSONField(fields, "imports", "Imports"); ok {
		if err := json.Unmarshal(raw, &decoded.Imports); err != nil {
			return fmt.Errorf("decode imports: %w", err)
		}
	}
	if raw, ok := firstJSONField(fields, "rules", "Rules"); ok {
		if err := json.Unmarshal(raw, &decoded.Rules); err != nil {
			return fmt.Errorf("decode rules: %w", err)
		}
	}
	*f = decoded
	return nil
}

func firstJSONField(fields map[string]json.RawMessage, names ...string) (json.RawMessage, bool) {
	for _, name := range names {
		if value, ok := fields[name]; ok {
			return value, true
		}
	}
	return nil, false
}

func (n Normalized) GatewayRules() []Rule {
	out := make([]Rule, 0, len(n.Rules))
	for _, rule := range n.Rules {
		if rule.Gateway() {
			out = append(out, rule)
		}
	}
	return out
}

func (n Normalized) DNSPoolRules() []Rule {
	out := make([]Rule, 0, len(n.Rules))
	for _, rule := range n.Rules {
		if rule.DNSOnly() {
			out = append(out, rule)
		}
	}
	return out
}

func (n Normalized) MatchDomain(host string) (Rule, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, rule := range n.Rules {
		if matchRuleDomain(rule, host) {
			return rule, true
		}
	}
	return Rule{}, false
}

func (n Normalized) MatchGatewayDomain(host string) (Rule, bool) {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, rule := range n.GatewayRules() {
		if matchRuleDomain(rule, host) {
			return rule, true
		}
	}
	return Rule{}, false
}

func matchRuleDomain(rule Rule, host string) bool {
	for _, value := range rule.Domain {
		if strings.EqualFold(strings.TrimSuffix(value, "."), host) {
			return true
		}
	}
	for _, value := range rule.DomainSuffix {
		suffix := strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(value, "."), "."))
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	for _, value := range rule.DomainKeyword {
		if strings.Contains(host, strings.ToLower(value)) {
			return true
		}
	}
	for _, value := range rule.DomainRegex {
		if regexp.MustCompile(value).MatchString(host) {
			return true
		}
	}
	return false
}

type Import struct {
	Name    string `toml:"name" json:"name"`
	Type    string `toml:"type" json:"type"`
	Path    string `toml:"path" json:"path"`
	URL     string `toml:"url" json:"url"`
	Format  string `toml:"format" json:"format"`
	Exit    string `toml:"exit" json:"exit"`
	DNSPool string `toml:"dns_pool" json:"dns_pool"`
}

func (i *Import) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	accepted := map[string]string{
		"name": "Name", "type": "Type", "path": "Path", "url": "URL",
		"format": "Format", "exit": "Exit", "dns_pool": "DNSPool",
	}
	for lower, legacy := range accepted {
		if _, ok := fields[lower]; ok {
			if _, duplicate := fields[legacy]; duplicate {
				return fmt.Errorf("import contains both %q and legacy %q", lower, legacy)
			}
		}
	}
	for name := range fields {
		known := false
		for lower, legacy := range accepted {
			if name == lower || name == legacy {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown field %q in import", name)
		}
	}
	var decoded Import
	targets := map[string]*string{
		"name": &decoded.Name, "type": &decoded.Type, "path": &decoded.Path,
		"url": &decoded.URL, "format": &decoded.Format, "exit": &decoded.Exit,
		"dns_pool": &decoded.DNSPool,
	}
	for lower, target := range targets {
		if raw, ok := firstJSONField(fields, lower, accepted[lower]); ok {
			if err := json.Unmarshal(raw, target); err != nil {
				return fmt.Errorf("decode import %s: %w", lower, err)
			}
		}
	}
	*i = decoded
	return nil
}

type Rule struct {
	Name          string   `toml:"name" json:"name" yaml:"name"`
	Exit          string   `toml:"exit" json:"exit" yaml:"exit"`
	DNSPool       string   `toml:"dns_pool" json:"dns_pool" yaml:"dns_pool"`
	Domain        []string `toml:"domain" json:"domain" yaml:"domain"`
	DomainSuffix  []string `toml:"domain_suffix" json:"domain_suffix" yaml:"domain_suffix"`
	DomainKeyword []string `toml:"domain_keyword" json:"domain_keyword" yaml:"domain_keyword"`
	DomainRegex   []string `toml:"domain_regex" json:"domain_regex" yaml:"domain_regex"`
	IPCIDR        []string `toml:"ip_cidr" json:"ip_cidr" yaml:"ip_cidr"`
	RuleSet       []string `toml:"rule_set" json:"rule_set" yaml:"rule_set"`
}

func (r *Rule) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name          string         `json:"name"`
		Exit          string         `json:"exit"`
		DNSPool       string         `json:"dns_pool"`
		Domain        jsonStringList `json:"domain"`
		DomainSuffix  jsonStringList `json:"domain_suffix"`
		DomainKeyword jsonStringList `json:"domain_keyword"`
		DomainRegex   jsonStringList `json:"domain_regex"`
		IPCIDR        jsonStringList `json:"ip_cidr"`
		RuleSet       jsonStringList `json:"rule_set"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*r = Rule{
		Name:          raw.Name,
		Exit:          raw.Exit,
		DNSPool:       raw.DNSPool,
		Domain:        []string(raw.Domain),
		DomainSuffix:  []string(raw.DomainSuffix),
		DomainKeyword: []string(raw.DomainKeyword),
		DomainRegex:   []string(raw.DomainRegex),
		IPCIDR:        []string(raw.IPCIDR),
		RuleSet:       []string(raw.RuleSet),
	}
	return nil
}

type jsonStringList []string

func (l *jsonStringList) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*l = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*l = many
		return nil
	}
	return fmt.Errorf("must be string or string array")
}

type Normalized struct {
	Rules    []Rule
	Warnings []Warning
}

type Warning struct {
	Import  string
	Rule    string
	Matcher string
	Detail  string
}

func (w Warning) String() string {
	parts := []string{"skipped import", w.Import}
	if w.Rule != "" {
		parts = append(parts, "rule", w.Rule)
	}
	if w.Matcher != "" {
		parts = append(parts, "matcher", w.Matcher)
	}
	if w.Detail != "" {
		parts = append(parts, ":", w.Detail)
	}
	return strings.Join(parts, " ")
}

func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, err
	}
	var file File
	if err := toml.Unmarshal(data, &file); err != nil {
		return File{}, err
	}
	return file, nil
}

func Normalize(file File) (Normalized, error) {
	var out []Rule
	var warnings []Warning
	for _, rule := range file.Rules {
		if err := validateRule(rule); err != nil {
			return Normalized{}, err
		}
		out = append(out, rule)
	}
	for _, imp := range file.Imports {
		imported, skipped, err := loadImport(imp)
		if err != nil {
			return Normalized{}, err
		}
		out = append(out, imported...)
		warnings = append(warnings, skipped...)
	}
	return Normalized{Rules: out, Warnings: warnings}, nil
}

func validateRule(rule Rule) error {
	if rule.Name == "" {
		return errors.New("rule name is required")
	}
	if (rule.Exit == "") == (rule.DNSPool == "") {
		return fmt.Errorf("rule %q: exactly one of exit or dns_pool is required", rule.Name)
	}
	if rule.DNSPool != "" && !validDNSPoolName(rule.DNSPool) {
		return fmt.Errorf("rule %q: invalid dns_pool %q", rule.Name, rule.DNSPool)
	}
	if rule.Empty() {
		return fmt.Errorf("rule %q: no matchers configured", rule.Name)
	}
	for _, value := range rule.DomainRegex {
		if _, err := regexp.Compile(value); err != nil {
			return fmt.Errorf("rule %q: invalid domain_regex %q: %w", rule.Name, value, err)
		}
	}
	return nil
}

func validDNSPoolName(pool string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(pool)
}

func ValidateDNSPoolReferences(file File, poolNames []string) error {
	valid := make(map[string]bool, len(poolNames))
	for _, name := range poolNames {
		valid[name] = true
	}
	for _, rule := range file.Rules {
		if rule.DNSPool != "" && !valid[rule.DNSPool] {
			return fmt.Errorf("rule %q: unknown dns_pool %q", rule.Name, rule.DNSPool)
		}
	}
	for _, imp := range file.Imports {
		if imp.DNSPool != "" && !valid[imp.DNSPool] {
			return fmt.Errorf("import %q: unknown dns_pool %q", imp.Name, imp.DNSPool)
		}
	}
	return nil
}

func (r Rule) Gateway() bool {
	return r.Exit != ""
}

func (r Rule) DNSOnly() bool {
	return r.DNSPool != ""
}

func (r Rule) Empty() bool {
	return len(r.Domain) == 0 && len(r.DomainSuffix) == 0 &&
		len(r.DomainKeyword) == 0 && len(r.DomainRegex) == 0 &&
		len(r.IPCIDR) == 0 && len(r.RuleSet) == 0
}

func loadImport(imp Import) ([]Rule, []Warning, error) {
	if imp.Name == "" || imp.Type == "" {
		return nil, nil, errors.New("import name and type are required")
	}
	if (imp.Exit == "") == (imp.DNSPool == "") {
		return nil, nil, fmt.Errorf("import %q: exactly one of exit or dns_pool is required", imp.Name)
	}
	if imp.DNSPool != "" && !validDNSPoolName(imp.DNSPool) {
		return nil, nil, fmt.Errorf("import %q: invalid dns_pool %q", imp.Name, imp.DNSPool)
	}
	data, err := readImportData(imp)
	if err != nil {
		return nil, nil, fmt.Errorf("import %q: %w", imp.Name, err)
	}
	switch strings.ToLower(imp.Type) {
	case "sing-box", "singbox":
		return parseSingBox(imp, data)
	case "mihomo", "clash", "clash-meta", "mimoho":
		return parseMihomo(imp, data)
	default:
		return nil, nil, fmt.Errorf("import %q: unsupported type %q", imp.Name, imp.Type)
	}
}

func readImportData(imp Import) ([]byte, error) {
	switch {
	case imp.Path != "":
		return os.ReadFile(imp.Path)
	case imp.URL != "":
		client := http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(imp.URL)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("download failed: %s", resp.Status)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	default:
		return nil, errors.New("path or url is required")
	}
}

type singBoxRuleSet struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

func parseSingBox(imp Import, data []byte) ([]Rule, []Warning, error) {
	var set singBoxRuleSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, nil, err
	}
	if len(set.Rules) == 0 {
		return nil, nil, fmt.Errorf("import %q: no rules found", imp.Name)
	}
	out := make([]Rule, 0, len(set.Rules))
	var warnings []Warning
	for i, rule := range set.Rules {
		rule.Name = fmt.Sprintf("%s-%d", imp.Name, i+1)
		rule.Exit = imp.Exit
		rule.DNSPool = imp.DNSPool
		warnings = append(warnings, stripUnsupportedImportMatchers(imp.Name, &rule)...)
		if rule.Empty() {
			warnings = append(warnings, Warning{
				Import: imp.Name,
				Rule:   rule.Name,
				Detail: "no supported matchers left",
			})
			continue
		}
		if err := validateRule(rule); err != nil {
			return nil, nil, err
		}
		out = append(out, rule)
	}
	if len(out) == 1 {
		oldName := out[0].Name
		out[0].Name = imp.Name
		for i := range warnings {
			if warnings[i].Rule == oldName {
				warnings[i].Rule = imp.Name
			}
		}
	}
	return out, warnings, nil
}

type mihomoProvider struct {
	Payload []string `yaml:"payload"`
}

func parseMihomo(imp Import, data []byte) ([]Rule, []Warning, error) {
	var provider mihomoProvider
	if err := yaml.Unmarshal(data, &provider); err != nil {
		return nil, nil, err
	}
	if len(provider.Payload) == 0 {
		return nil, nil, fmt.Errorf("import %q: empty payload", imp.Name)
	}
	rule := Rule{Name: imp.Name, Exit: imp.Exit, DNSPool: imp.DNSPool}
	var warnings []Warning
	for _, item := range provider.Payload {
		warning, err := addMihomoPayload(&rule, item)
		if err != nil {
			return nil, nil, fmt.Errorf("import %q: %w", imp.Name, err)
		}
		if warning != nil {
			warnings = append(warnings, *warning)
		}
	}
	if rule.Empty() {
		warnings = append(warnings, Warning{
			Import: imp.Name,
			Rule:   rule.Name,
			Detail: "no supported matchers left",
		})
		return nil, warnings, nil
	}
	if err := validateRule(rule); err != nil {
		return nil, nil, err
	}
	return []Rule{rule}, warnings, nil
}

func stripUnsupportedImportMatchers(importName string, rule *Rule) []Warning {
	matchers := []struct {
		name   string
		values []string
		clear  func()
	}{
		{"domain_keyword", rule.DomainKeyword, func() { rule.DomainKeyword = nil }},
		{"domain_regex", rule.DomainRegex, func() { rule.DomainRegex = nil }},
		{"ip_cidr", rule.IPCIDR, func() { rule.IPCIDR = nil }},
		{"rule_set", rule.RuleSet, func() { rule.RuleSet = nil }},
	}
	var warnings []Warning
	for _, matcher := range matchers {
		if len(matcher.values) == 0 {
			continue
		}
		warnings = append(warnings, Warning{
			Import:  importName,
			Rule:    rule.Name,
			Matcher: matcher.name,
			Detail:  fmt.Sprintf("%d values are not supported by smartdns-rs rendering", len(matcher.values)),
		})
		matcher.clear()
	}
	return warnings
}

func addMihomoPayload(rule *Rule, item string) (*Warning, error) {
	parts := strings.Split(item, ",")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid payload line %q", item)
	}
	kind := strings.ToUpper(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if value == "" {
		return nil, fmt.Errorf("empty matcher in %q", item)
	}
	switch kind {
	case "DOMAIN":
		rule.Domain = append(rule.Domain, value)
	case "DOMAIN-SUFFIX":
		rule.DomainSuffix = append(rule.DomainSuffix, value)
	case "DOMAIN-WILDCARD":
		rule.DomainSuffix = append(rule.DomainSuffix, strings.TrimPrefix(value, "*."))
	case "DOMAIN-KEYWORD", "DOMAIN-REGEX", "IP-CIDR", "IP-CIDR6", "RULE-SET":
		return &Warning{
			Import:  rule.Name,
			Rule:    rule.Name,
			Matcher: kind,
			Detail:  fmt.Sprintf("%q is not supported by smartdns-rs rendering", value),
		}, nil
	default:
		return &Warning{
			Import:  rule.Name,
			Rule:    rule.Name,
			Matcher: kind,
			Detail:  fmt.Sprintf("%q is not supported by 5gws", value),
		}, nil
	}
	return nil, nil
}
