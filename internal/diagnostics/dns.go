package diagnostics

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/miekg/dns"

	"github.com/morain/5gws/internal/config"
)

const dnsProbeTimeout = 2 * time.Second

func probeDNSPools(ctx context.Context, cfg config.Config) []DNSResult {
	pools := cfg.DNS.Pools()
	total := 0
	for _, pool := range pools {
		total += len(pool.Upstreams)
	}
	type indexedResult struct {
		index  int
		result DNSResult
	}
	results := make(chan indexedResult, total)
	index := 0
	for _, pool := range pools {
		for _, upstream := range pool.Upstreams {
			go func(index int, pool, queryName, upstream string) {
				results <- indexedResult{index: index, result: probeDNSUpstream(ctx, pool, queryName, upstream)}
			}(index, pool.Name, dnsQueryName(pool.ProbeDomain), upstream)
			index++
		}
	}
	out := make([]DNSResult, total)
	for range total {
		item := <-results
		out[item.index] = item.result
	}
	return out
}

func dnsQueryName(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), ".") + "."
}

func probeDNSUpstream(ctx context.Context, pool, queryName, upstream string) DNSResult {
	result := DNSResult{Pool: pool, Upstream: upstream, Status: "error"}
	message := new(dns.Msg)
	message.SetQuestion(queryName, dns.TypeA)
	started := time.Now()
	var response *dns.Msg
	var err error
	switch {
	case strings.HasPrefix(upstream, "https://"):
		result.Protocol = "doh"
		response, err = queryDOH(ctx, upstream, message, nil)
	case strings.HasPrefix(upstream, "tls://"):
		result.Protocol = "dot"
		response, err = queryDOTUpstream(ctx, upstream, message)
	default:
		result.Protocol = "udp"
		response, _, err = (&dns.Client{Net: "udp", Timeout: dnsProbeTimeout}).ExchangeContext(ctx, message, dnsAddress(upstream, "53"))
	}
	result.LatencyMS = elapsedMS(started)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if response.Rcode != dns.RcodeSuccess {
		result.Error = fmt.Sprintf("DNS response code: %s", dns.RcodeToString[response.Rcode])
		return result
	}
	result.Status = "ok"
	result.Answers = answerAddresses(response)
	return result
}

func queryDOH(ctx context.Context, endpoint string, message *dns.Msg, client *http.Client) (*dns.Msg, error) {
	data, err := message.Pack()
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, dnsProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-message")
	req.Header.Set("Content-Type", "application/dns-message")
	if client == nil {
		client = &http.Client{Timeout: dnsProbeTimeout, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DoH request failed: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	answer := new(dns.Msg)
	if err := answer.Unpack(body); err != nil {
		return nil, err
	}
	if answer.Id != message.Id {
		return nil, fmt.Errorf("DoH response ID does not match request")
	}
	return answer, nil
}

func queryDOTUpstream(ctx context.Context, upstream string, message *dns.Msg) (*dns.Msg, error) {
	parsed, err := url.Parse(upstream)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid DoT upstream %q", upstream)
	}
	address := net.JoinHostPort(parsed.Hostname(), parsed.Port())
	if parsed.Port() == "" {
		address = net.JoinHostPort(parsed.Hostname(), "853")
	}
	client := &dns.Client{Net: "tcp-tls", Timeout: dnsProbeTimeout, TLSConfig: &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}}
	response, _, err := client.ExchangeContext(ctx, message, address)
	return response, err
}

func dnsAddress(value, defaultPort string) string {
	if _, _, err := net.SplitHostPort(value); err == nil {
		return value
	}
	return net.JoinHostPort(strings.Trim(value, "[]"), defaultPort)
}

func answerAddresses(message *dns.Msg) []string {
	var out []string
	for _, answer := range message.Answer {
		switch value := answer.(type) {
		case *dns.A:
			out = append(out, value.A.String())
		case *dns.AAAA:
			out = append(out, value.AAAA.String())
		}
	}
	return out
}

func elapsedMS(started time.Time) float64 {
	return float64(time.Since(started).Microseconds()) / 1000
}
