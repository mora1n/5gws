package engine

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/morain/5gws/internal/config"
)

const dnsReadinessRetryInterval = 50 * time.Millisecond
const dnsReadinessExchangeTimeout = time.Second

type dnsReadinessProbe struct {
	label     string
	address   string
	domain    string
	network   string
	tlsConfig *tls.Config
}

func dnsReadinessProbes(cfg config.Config) []dnsReadinessProbe {
	domains := []string{"example.com.", "www.baidu.com."}
	seen := map[string]bool{"example.com.": true, "www.baidu.com.": true}
	for _, pool := range cfg.DNS.CustomPools {
		domain := dnsFQDN(pool.ProbeDomain)
		if !seen[domain] {
			domains = append(domains, domain)
			seen[domain] = true
		}
	}
	probes := make([]dnsReadinessProbe, 0, len(domains)*2)
	for _, domain := range domains {
		probes = append(probes, dnsReadinessProbe{
			label:   "internal DNS TCP",
			address: loopbackAddress(cfg.DNS.ListenTCP),
			domain:  domain,
			network: "tcp",
		})
	}
	if cfg.DNS.ListenPublicDOT != "" {
		for _, domain := range domains {
			probes = append(probes, dnsReadinessProbe{
				label:   "public DoT",
				address: loopbackAddress(cfg.DNS.ListenPublicDOT),
				domain:  domain,
				network: "tcp-tls",
				tlsConfig: &tls.Config{
					ServerName: cfg.DNS.DOTDomain,
					MinVersion: tls.VersionTLS12,
				},
			})
		}
	}
	return probes
}

func dnsFQDN(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), ".") + "."
}

func waitDNSReadiness(ctx context.Context, probe dnsReadinessProbe) error {
	var lastErr error
	for {
		if err := probeDNSReadiness(ctx, probe); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s readiness for %s failed: %w", probe.label, probe.domain, errors.Join(lastErr, ctx.Err()))
		case <-time.After(dnsReadinessRetryInterval):
		}
	}
}

func probeDNSReadiness(ctx context.Context, probe dnsReadinessProbe) error {
	message := new(dns.Msg)
	message.SetQuestion(probe.domain, dns.TypeA)
	client := &dns.Client{
		Net:       probe.network,
		Timeout:   dnsReadinessExchangeTimeout,
		TLSConfig: probe.tlsConfig,
	}
	response, _, err := client.ExchangeContext(ctx, message, probe.address)
	if err != nil {
		return err
	}
	if response.Rcode != dns.RcodeSuccess {
		return fmt.Errorf("DNS response code: %s", dns.RcodeToString[response.Rcode])
	}
	for _, answer := range response.Answer {
		if record, ok := answer.(*dns.A); ok && net.IP(record.A).To4() != nil {
			return nil
		}
	}
	return errors.New("DNS response contains no A answer")
}
