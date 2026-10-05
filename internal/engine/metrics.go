package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/diagnostics"
)

type Metrics struct {
	Timestamp      int64   `json:"timestamp"`
	ProcessCount   int     `json:"process_count"`
	RSSBytes       uint64  `json:"rss_bytes"`
	SwapBytes      uint64  `json:"swap_bytes"`
	TCPConnections int     `json:"tcp_connections"`
	RXBytes        uint64  `json:"rx_bytes"`
	TXBytes        uint64  `json:"tx_bytes"`
	Interface      string  `json:"interface"`
	DNSOK          bool    `json:"dns_ok"`
	DNSLatencyMS   float64 `json:"dns_latency_ms"`
	DNSError       string  `json:"dns_error,omitempty"`
	DOTOK          bool    `json:"dot_ok"`
	DOTLatencyMS   float64 `json:"dot_latency_ms"`
	DOTError       string  `json:"dot_error,omitempty"`
}

func CollectMetrics(ctx context.Context, processes []ProcessStatus, cfg config.Config) Metrics {
	interfaceName := cfg.Network.IngressIface
	metric := Metrics{Timestamp: time.Now().Unix(), ProcessCount: len(processes), Interface: interfaceName}
	pageSize := uint64(os.Getpagesize())
	for _, process := range processes {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", process.PID))
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) > 1 {
				pages, _ := strconv.ParseUint(fields[1], 10, 64)
				metric.RSSBytes += pages * pageSize
			}
		}
		status, err := os.Open(fmt.Sprintf("/proc/%d/status", process.PID))
		if err == nil {
			swapBytes, parseErr := swapBytesFrom(status)
			status.Close()
			if parseErr == nil {
				metric.SwapBytes += swapBytes
			}
		}
	}
	metric.TCPConnections = countProcLines("/proc/net/tcp") + countProcLines("/proc/net/tcp6")
	metric.RXBytes, metric.TXBytes = networkBytes(interfaceName)
	started := time.Now()
	if err := probeDNS(ctx, cfg.DNS.ListenUDP); err == nil {
		metric.DNSOK = true
		metric.DNSLatencyMS = float64(time.Since(started).Microseconds()) / 1000
	} else {
		metric.DNSError = err.Error()
	}
	collectDOTMetrics(ctx, cfg, &metric)
	return metric
}

func collectDOTMetrics(ctx context.Context, cfg config.Config, metric *Metrics) {
	result := (diagnostics.Runner{}).Run(ctx, cfg, diagnostics.ScopeDOT)
	metric.DOTOK = true
	var failures []string
	for _, item := range []struct {
		label  string
		result *diagnostics.DOTResult
	}{{"public DoT", result.DOT}, {"internal DoT", result.InternalDOT}} {
		if item.result.Status == "disabled" {
			metric.DOTOK = false
			failures = append(failures, item.label+": disabled")
		} else if item.result.Status != "ok" {
			metric.DOTOK = false
			failures = append(failures, item.label+": "+item.result.Error)
		}
		metric.DOTLatencyMS = max(metric.DOTLatencyMS, item.result.LatencyMS)
	}
	metric.DOTError = strings.Join(failures, "; ")
}

func swapBytesFrom(reader io.Reader) (uint64, error) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "VmSwap:" {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return 0, fmt.Errorf("invalid VmSwap line %q", scanner.Text())
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse VmSwap value %q: %w", fields[1], err)
		}
		if kilobytes > ^uint64(0)/1024 {
			return 0, fmt.Errorf("VmSwap value %d kB overflows bytes", kilobytes)
		}
		return kilobytes * 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan process status: %w", err)
	}
	return 0, fmt.Errorf("process status does not contain VmSwap")
}

func countProcLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := -1
	for scanner.Scan() {
		count++
	}
	if count < 0 {
		return 0
	}
	return count
}

func networkBytes(interfaceName string) (uint64, uint64) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	return networkBytesFrom(file, interfaceName)
}

func networkBytesFrom(reader io.Reader, interfaceName string) (uint64, uint64) {
	var rx, tx uint64
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ":") {
			continue
		}
		fields := strings.Fields(strings.Replace(line, ":", " ", 1))
		if len(fields) < 10 || fields[0] != interfaceName {
			continue
		}
		in, _ := strconv.ParseUint(fields[1], 10, 64)
		out, _ := strconv.ParseUint(fields[9], 10, 64)
		rx += in
		tx += out
	}
	return rx, tx
}

func probeDNS(ctx context.Context, address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	message := new(dns.Msg)
	message.SetQuestion("example.com.", dns.TypeA)
	response, _, err := (&dns.Client{Net: "udp", Timeout: time.Second}).ExchangeContext(ctx, message, net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	return diagnostics.ValidateDNSResponse(message, response)
}
