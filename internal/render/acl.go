package render

import (
	"fmt"
	"strings"

	"github.com/morain/5gws/internal/rules"
)

func init() {
	// The template package requires functions at parse time, so keep the ACL
	// expression builder in the same package and register it from init.
}

func aclAny(prefix string, view ruleView) string {
	var names []string
	seen := map[string]bool{}
	appendName := func(kind string) {
		name := fmt.Sprintf("%s_%s_%s", prefix, view.ID, kind)
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if len(view.Rule.Domain) > 0 {
		appendName("exact")
	}
	if len(view.Rule.DomainKeyword) > 0 {
		appendName("keyword")
	}
	if len(view.Rule.DomainRegex) > 0 {
		appendName("regex")
	}
	if len(view.Rule.DomainSuffix) > 0 {
		appendName("suffix")
		appendName("root")
	}
	if len(names) == 0 {
		return "FALSE"
	}
	return strings.Join(names, " || ")
}

func aclAnyAll(prefix string, views []ruleView) string {
	var exprs []string
	for _, view := range views {
		expr := aclAny(prefix, view)
		if expr != "FALSE" {
			exprs = append(exprs, expr)
		}
	}
	if len(exprs) == 0 {
		return "FALSE"
	}
	return strings.Join(exprs, " || ")
}

func HTTP3Capable(rule rules.Rule) bool {
	return len(rule.Domain) > 0 || len(rule.DomainSuffix) > 0 ||
		len(rule.DomainKeyword) > 0 || len(rule.DomainRegex) > 0
}
