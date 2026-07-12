package diagnostics

import (
	"context"
	"crypto/x509"
	"time"

	"github.com/morain/5gws/internal/config"
)

const (
	ScopeAll   = "all"
	ScopeDNS   = "dns"
	ScopeExits = "exits"
	ScopeDOT   = "dot"
)

type Result struct {
	CheckedAt time.Time    `json:"checked_at"`
	DNS       []DNSResult  `json:"dns,omitempty"`
	Exits     []ExitResult `json:"exits,omitempty"`
	DOT       *DOTResult   `json:"dot,omitempty"`
}

type DNSResult struct {
	Pool      string   `json:"pool"`
	Upstream  string   `json:"upstream"`
	Protocol  string   `json:"protocol"`
	Status    string   `json:"status"`
	LatencyMS float64  `json:"latency_ms"`
	Answers   []string `json:"answers,omitempty"`
	Error     string   `json:"error,omitempty"`
}

type ExitResult struct {
	Name              string  `json:"name"`
	Type              string  `json:"type"`
	Status            string  `json:"status"`
	Upstream          string  `json:"upstream,omitempty"`
	UpstreamStatus    string  `json:"upstream_status,omitempty"`
	UpstreamLatencyMS float64 `json:"upstream_latency_ms,omitempty"`
	EgressStatus      string  `json:"egress_status"`
	EgressIP          string  `json:"egress_ip,omitempty"`
	EgressLatencyMS   float64 `json:"egress_latency_ms,omitempty"`
	Error             string  `json:"error,omitempty"`
}

type DOTResult struct {
	Domain            string    `json:"domain"`
	Listen            string    `json:"listen"`
	Status            string    `json:"status"`
	LatencyMS         float64   `json:"latency_ms,omitempty"`
	CertificateStatus string    `json:"certificate_status"`
	ExpiresAt         time.Time `json:"expires_at,omitempty"`
	DaysRemaining     int       `json:"days_remaining,omitempty"`
	DomainMatch       bool      `json:"domain_match"`
	Error             string    `json:"error,omitempty"`
}

type Runner struct {
	EgressURL string
	RootCAs   *x509.CertPool
}

func ValidScope(scope string) bool {
	switch scope {
	case ScopeAll, ScopeDNS, ScopeExits, ScopeDOT:
		return true
	default:
		return false
	}
}

func (r Runner) Run(ctx context.Context, cfg config.Config, scope string) Result {
	result := Result{CheckedAt: time.Now().UTC()}
	type part struct {
		kind  string
		dns   []DNSResult
		exits []ExitResult
		dot   DOTResult
	}
	count := 0
	parts := make(chan part, 3)
	if scope == ScopeAll || scope == ScopeDNS {
		count++
		go func() { parts <- part{kind: ScopeDNS, dns: probeDNSPools(ctx, cfg)} }()
	}
	if scope == ScopeAll || scope == ScopeExits {
		count++
		go func() { parts <- part{kind: ScopeExits, exits: r.probeExits(ctx, cfg)} }()
	}
	if scope == ScopeAll || scope == ScopeDOT {
		count++
		go func() { parts <- part{kind: ScopeDOT, dot: probeDOT(ctx, cfg, r.RootCAs)} }()
	}
	for range count {
		item := <-parts
		switch item.kind {
		case ScopeDNS:
			result.DNS = item.dns
		case ScopeExits:
			result.Exits = item.exits
		case ScopeDOT:
			result.DOT = &item.dot
		}
	}
	return result
}
