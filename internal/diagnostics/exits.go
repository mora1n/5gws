package diagnostics

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/proxy"

	"github.com/morain/5gws/internal/config"
)

const defaultEgressURL = "https://api.ipify.org"

func (r Runner) probeExits(ctx context.Context, cfg config.Config) []ExitResult {
	type indexedResult struct {
		index  int
		result ExitResult
	}
	results := make(chan indexedResult, len(cfg.Exits))
	for index, exit := range cfg.Exits {
		go func(index int, exit config.ExitConfig) {
			results <- indexedResult{index: index, result: r.probeExit(ctx, exit)}
		}(index, exit)
	}
	out := make([]ExitResult, len(cfg.Exits))
	for range cfg.Exits {
		item := <-results
		out[item.index] = item.result
	}
	return out
}

func (r Runner) probeExit(ctx context.Context, exit config.ExitConfig) ExitResult {
	result := ExitResult{Name: exit.Name, Type: exit.Type, Status: "error", EgressStatus: "error"}
	baseDialer := &net.Dialer{Timeout: 3 * time.Second}
	var dialer proxy.Dialer = baseDialer
	if exit.Type == "shadowsocks-rust" {
		result.Upstream = net.JoinHostPort(exit.Server, fmt.Sprint(exit.ServerPort))
		started := time.Now()
		conn, err := baseDialer.DialContext(ctx, "tcp", result.Upstream)
		result.UpstreamLatencyMS = elapsedMS(started)
		if err != nil {
			result.UpstreamStatus = "error"
			result.Error = "upstream: " + err.Error()
			return result
		}
		conn.Close()
		result.UpstreamStatus = "ok"
		listenHost := exit.ListenAddress
		if ip := net.ParseIP(listenHost); ip != nil && ip.IsUnspecified() {
			listenHost = "127.0.0.1"
		}
		dialer, err = proxy.SOCKS5("tcp", net.JoinHostPort(listenHost, fmt.Sprint(exit.ListenPort)), nil, baseDialer)
		if err != nil {
			result.Error = "SOCKS5: " + err.Error()
			return result
		}
	} else if exit.Type != "direct" {
		result.Error = "unsupported exit type: " + exit.Type
		return result
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
		transport.DialContext = contextDialer.DialContext
	} else {
		transport.DialContext = func(_ context.Context, network, address string) (net.Conn, error) {
			return dialer.Dial(network, address)
		}
	}
	url := r.EgressURL
	if url == "" {
		url = defaultEgressURL
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	started := time.Now()
	resp, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(req)
	result.EgressLatencyMS = elapsedMS(started)
	if err != nil {
		result.Error = "egress: " + err.Error()
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = "egress: " + resp.Status
		return result
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128))
	if err != nil {
		result.Error = "egress: " + err.Error()
		return result
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		result.Error = "egress returned an invalid IP address"
		return result
	}
	result.EgressStatus = "ok"
	result.EgressIP = ip
	result.Status = "ok"
	return result
}
