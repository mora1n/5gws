package rules

import (
	"fmt"
	"regexp"
	"strings"
)

type Compiled struct {
	rules   []Rule
	exact   map[string]int
	suffix  suffixNode
	complex []compiledComplex
}

type suffixNode struct {
	children map[string]*suffixNode
	rank     int
}

type compiledComplex struct {
	rank     int
	keywords []string
	regexps  []*regexp.Regexp
}

func Compile(norm Normalized) (*Compiled, error) {
	c := &Compiled{exact: make(map[string]int), suffix: suffixNode{rank: -1}}
	for _, rule := range norm.Rules {
		if !rule.Gateway() {
			continue
		}
		rank := len(c.rules)
		c.rules = append(c.rules, rule)
		for _, value := range rule.Domain {
			host := normalizeMatchHost(value)
			if host == "" {
				return nil, fmt.Errorf("rule %q has empty domain", rule.Name)
			}
			if current, ok := c.exact[host]; !ok || rank < current {
				c.exact[host] = rank
			}
		}
		for _, value := range rule.DomainSuffix {
			host := normalizeMatchHost(value)
			if host == "" {
				return nil, fmt.Errorf("rule %q has empty domain suffix", rule.Name)
			}
			c.suffix.insert(host, rank)
		}
		entry := compiledComplex{rank: rank}
		for _, keyword := range rule.DomainKeyword {
			entry.keywords = append(entry.keywords, strings.ToLower(keyword))
		}
		for _, pattern := range rule.DomainRegex {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return nil, fmt.Errorf("rule %q: compile regex %q: %w", rule.Name, pattern, err)
			}
			entry.regexps = append(entry.regexps, re)
		}
		if len(entry.keywords) > 0 || len(entry.regexps) > 0 {
			c.complex = append(c.complex, entry)
		}
	}
	return c, nil
}

func (c *Compiled) MatchGatewayDomain(host string) (Rule, bool) {
	if c == nil {
		return Rule{}, false
	}
	host = normalizeMatchHost(host)
	best := len(c.rules)
	if rank, ok := c.exact[host]; ok {
		best = rank
	}
	if rank := c.suffix.match(host); rank >= 0 && rank < best {
		best = rank
	}
	for _, entry := range c.complex {
		if entry.rank >= best {
			break
		}
		if matchComplex(entry, host) {
			best = entry.rank
			break
		}
	}
	if best == len(c.rules) {
		return Rule{}, false
	}
	return c.rules[best], true
}

func (c *Compiled) Len() int {
	if c == nil {
		return 0
	}
	return len(c.rules)
}

func matchComplex(entry compiledComplex, host string) bool {
	for _, keyword := range entry.keywords {
		if strings.Contains(host, keyword) {
			return true
		}
	}
	for _, re := range entry.regexps {
		if re.MatchString(host) {
			return true
		}
	}
	return false
}

func (n *suffixNode) insert(host string, rank int) {
	current := n
	for end := len(host); end > 0; {
		start := strings.LastIndexByte(host[:end], '.') + 1
		label := host[start:end]
		if current.children == nil {
			current.children = make(map[string]*suffixNode)
		}
		next := current.children[label]
		if next == nil {
			next = &suffixNode{rank: -1}
			current.children[label] = next
		}
		current = next
		if start == 0 {
			break
		}
		end = start - 1
	}
	if current.rank < 0 || rank < current.rank {
		current.rank = rank
	}
}

func (n *suffixNode) match(host string) int {
	current := n
	best := -1
	for end := len(host); end > 0; {
		start := strings.LastIndexByte(host[:end], '.') + 1
		next := current.children[host[start:end]]
		if next == nil {
			break
		}
		current = next
		if current.rank >= 0 && (best < 0 || current.rank < best) {
			best = current.rank
		}
		if start == 0 {
			break
		}
		end = start - 1
	}
	return best
}

func normalizeMatchHost(value string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(value), "."))
}
