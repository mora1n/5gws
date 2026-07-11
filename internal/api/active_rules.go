package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/store"
)

type activeRulesResponse struct {
	RevisionID   int64             `json:"revision_id"`
	ActiveAt     time.Time         `json:"active_at"`
	RuleCount    int               `json:"rule_count"`
	MatcherCount int               `json:"matcher_count"`
	Groups       []activeRuleGroup `json:"groups"`
}

type activeRuleGroup struct {
	Key          string              `json:"key"`
	Title        string              `json:"title"`
	RuleCount    int                 `json:"rule_count"`
	MatcherCount int                 `json:"matcher_count"`
	Rules        []activeRuleSummary `json:"rules"`
}

type activeRuleSummary struct {
	Name     string           `json:"name"`
	Target   string           `json:"target"`
	Matchers []matcherSummary `json:"matchers"`
}

type matcherSummary struct {
	Label   string   `json:"label"`
	Count   int      `json:"count"`
	Samples []string `json:"samples"`
}

func (s *Server) activeRules(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	response := summarizeActiveRules(revision.Bundle)
	response.RevisionID = revision.ID
	response.ActiveAt = revision.ActiveAt
	if response.ActiveAt.IsZero() {
		response.ActiveAt = revision.CreatedAt
	}
	writeJSON(w, http.StatusOK, response)
}

func summarizeActiveRules(bundle store.Bundle) activeRulesResponse {
	items := bundle.ResolvedRules
	response := activeRulesResponse{RuleCount: len(items)}
	indices := make(map[string]int)
	displayNames := legacySingleImportNames(bundle)
	for _, rule := range items {
		title, target := activeRuleTarget(rule)
		key := title + ":" + target
		index, exists := indices[key]
		if !exists {
			index = len(response.Groups)
			indices[key] = index
			response.Groups = append(response.Groups, activeRuleGroup{Key: key, Title: title + " · " + target})
		}
		matchers := summarizeMatchers(rule)
		count := 0
		for _, matcher := range matchers {
			count += matcher.Count
		}
		group := &response.Groups[index]
		name := rule.Name
		if displayName, ok := displayNames[name]; ok {
			name = displayName
		}
		group.Rules = append(group.Rules, activeRuleSummary{Name: name, Target: target, Matchers: matchers})
		group.RuleCount++
		group.MatcherCount += count
		response.MatcherCount += count
	}
	return response
}

func legacySingleImportNames(bundle store.Bundle) map[string]string {
	aliases := make(map[string]string)
	for _, imp := range bundle.Rules.Imports {
		var matched string
		count := 0
		prefix := imp.Name + "-"
		for _, rule := range bundle.ResolvedRules {
			if !strings.HasPrefix(rule.Name, prefix) {
				continue
			}
			index, err := strconv.Atoi(strings.TrimPrefix(rule.Name, prefix))
			if err != nil || index < 1 {
				continue
			}
			matched = rule.Name
			count++
		}
		if count == 1 {
			aliases[matched] = imp.Name
		}
	}
	return aliases
}

func activeRuleTarget(rule rules.Rule) (string, string) {
	if rule.Exit != "" {
		return "出口规则", "exit:" + rule.Exit
	}
	if rule.DNSPool != "" {
		return "DNS 解析池", "pool:" + rule.DNSPool
	}
	return "未分类", "-"
}

func summarizeMatchers(rule rules.Rule) []matcherSummary {
	values := []struct {
		label string
		items []string
	}{
		{"domain", rule.Domain}, {"domain_suffix", rule.DomainSuffix},
		{"domain_keyword", rule.DomainKeyword}, {"domain_regex", rule.DomainRegex},
		{"ip_cidr", rule.IPCIDR}, {"rule_set", rule.RuleSet},
	}
	var out []matcherSummary
	for _, value := range values {
		if len(value.items) == 0 {
			continue
		}
		limit := min(6, len(value.items))
		out = append(out, matcherSummary{Label: value.label, Count: len(value.items), Samples: value.items[:limit]})
	}
	return out
}
