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
	Imports []Import `toml:"imports"`
	Rules   []Rule   `toml:"rules"`
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
	Name    string `toml:"name"`
	Type    string `toml:"type"`
	Path    string `toml:"path"`
	URL     string `toml:"url"`
	Format  string `toml:"format"`
	Exit    string `toml:"exit"`
	DNSPool string `toml:"dns_pool"`
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
	Rules []Rule
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
	for _, rule := range file.Rules {
		if err := validateRule(rule); err != nil {
			return Normalized{}, err
		}
		out = append(out, rule)
	}
	for _, imp := range file.Imports {
		imported, err := loadImport(imp)
		if err != nil {
			return Normalized{}, err
		}
		out = append(out, imported...)
	}
	return Normalized{Rules: out}, nil
}

func validateRule(rule Rule) error {
	if rule.Name == "" {
		return errors.New("rule name is required")
	}
	if (rule.Exit == "") == (rule.DNSPool == "") {
		return fmt.Errorf("rule %q: exactly one of exit or dns_pool is required", rule.Name)
	}
	if rule.DNSPool != "" && !validDNSPool(rule.DNSPool) {
		return fmt.Errorf("rule %q: unsupported dns_pool %q", rule.Name, rule.DNSPool)
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

func validDNSPool(pool string) bool {
	switch pool {
	case "cn", "overseas_private", "overseas_public":
		return true
	default:
		return false
	}
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

func loadImport(imp Import) ([]Rule, error) {
	if imp.Name == "" || imp.Type == "" {
		return nil, errors.New("import name and type are required")
	}
	if (imp.Exit == "") == (imp.DNSPool == "") {
		return nil, fmt.Errorf("import %q: exactly one of exit or dns_pool is required", imp.Name)
	}
	if imp.DNSPool != "" && !validDNSPool(imp.DNSPool) {
		return nil, fmt.Errorf("import %q: unsupported dns_pool %q", imp.Name, imp.DNSPool)
	}
	data, err := readImportData(imp)
	if err != nil {
		return nil, fmt.Errorf("import %q: %w", imp.Name, err)
	}
	switch strings.ToLower(imp.Type) {
	case "sing-box", "singbox":
		return parseSingBox(imp, data)
	case "mihomo", "clash", "clash-meta", "mimoho":
		return parseMihomo(imp, data)
	default:
		return nil, fmt.Errorf("import %q: unsupported type %q", imp.Name, imp.Type)
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

func parseSingBox(imp Import, data []byte) ([]Rule, error) {
	var set singBoxRuleSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, err
	}
	if len(set.Rules) == 0 {
		return nil, fmt.Errorf("import %q: no rules found", imp.Name)
	}
	out := make([]Rule, 0, len(set.Rules))
	for i, rule := range set.Rules {
		rule.Name = fmt.Sprintf("%s-%d", imp.Name, i+1)
		rule.Exit = imp.Exit
		rule.DNSPool = imp.DNSPool
		if rule.Empty() {
			return nil, fmt.Errorf("import %q rule %d: no supported matchers", imp.Name, i+1)
		}
		if err := validateRule(rule); err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

type mihomoProvider struct {
	Payload []string `yaml:"payload"`
}

func parseMihomo(imp Import, data []byte) ([]Rule, error) {
	var provider mihomoProvider
	if err := yaml.Unmarshal(data, &provider); err != nil {
		return nil, err
	}
	if len(provider.Payload) == 0 {
		return nil, fmt.Errorf("import %q: empty payload", imp.Name)
	}
	rule := Rule{Name: imp.Name, Exit: imp.Exit, DNSPool: imp.DNSPool}
	for _, item := range provider.Payload {
		if err := addMihomoPayload(&rule, item); err != nil {
			return nil, fmt.Errorf("import %q: %w", imp.Name, err)
		}
	}
	if rule.Empty() {
		return nil, fmt.Errorf("import %q: no supported matchers", imp.Name)
	}
	if err := validateRule(rule); err != nil {
		return nil, err
	}
	return []Rule{rule}, nil
}

func addMihomoPayload(rule *Rule, item string) error {
	parts := strings.Split(item, ",")
	if len(parts) < 2 {
		return fmt.Errorf("invalid payload line %q", item)
	}
	kind := strings.ToUpper(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if value == "" {
		return fmt.Errorf("empty matcher in %q", item)
	}
	switch kind {
	case "DOMAIN":
		rule.Domain = append(rule.Domain, value)
	case "DOMAIN-SUFFIX":
		rule.DomainSuffix = append(rule.DomainSuffix, value)
	case "DOMAIN-KEYWORD":
		rule.DomainKeyword = append(rule.DomainKeyword, value)
	case "DOMAIN-REGEX":
		rule.DomainRegex = append(rule.DomainRegex, value)
	case "IP-CIDR", "IP-CIDR6":
		rule.IPCIDR = append(rule.IPCIDR, value)
	case "RULE-SET":
		rule.RuleSet = append(rule.RuleSet, value)
	case "DOMAIN-WILDCARD":
		rule.DomainSuffix = append(rule.DomainSuffix, strings.TrimPrefix(value, "*."))
	default:
		return fmt.Errorf("unsupported Mihomo matcher %q", kind)
	}
	return nil
}
