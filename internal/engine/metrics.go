package engine

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Metrics struct {
	Timestamp      int64   `json:"timestamp"`
	ProcessCount   int     `json:"process_count"`
	RSSBytes       uint64  `json:"rss_bytes"`
	TCPConnections int     `json:"tcp_connections"`
	RXBytes        uint64  `json:"rx_bytes"`
	TXBytes        uint64  `json:"tx_bytes"`
	DNSLatencyMS   float64 `json:"dns_latency_ms"`
}

func CollectMetrics(processes []ProcessStatus, dnsAddress string) Metrics {
	metric := Metrics{Timestamp: time.Now().Unix(), ProcessCount: len(processes)}
	pageSize := uint64(os.Getpagesize())
	for _, process := range processes {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", process.PID))
		if err != nil {
			continue
		}
		fields := strings.Fields(string(data))
		if len(fields) > 1 {
			pages, _ := strconv.ParseUint(fields[1], 10, 64)
			metric.RSSBytes += pages * pageSize
		}
	}
	metric.TCPConnections = countProcLines("/proc/net/tcp") + countProcLines("/proc/net/tcp6")
	metric.RXBytes, metric.TXBytes = networkBytes()
	started := time.Now()
	if probeDNS(dnsAddress) == nil {
		metric.DNSLatencyMS = float64(time.Since(started).Microseconds()) / 1000
	}
	return metric
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

func networkBytes() (uint64, uint64) {
	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	var rx, tx uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ":") {
			continue
		}
		fields := strings.Fields(strings.Replace(line, ":", " ", 1))
		if len(fields) < 10 || fields[0] == "lo" {
			continue
		}
		in, _ := strconv.ParseUint(fields[1], 10, 64)
		out, _ := strconv.ParseUint(fields[9], 10, 64)
		rx += in
		tx += out
	}
	return rx, tx
}

func probeDNS(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	conn, err := net.DialTimeout("udp", net.JoinHostPort(host, port), time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	packet := make([]byte, 12, 29)
	binary.BigEndian.PutUint16(packet[0:2], 0x5a5a)
	binary.BigEndian.PutUint16(packet[2:4], 0x0100)
	binary.BigEndian.PutUint16(packet[4:6], 1)
	packet = append(packet, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1)
	if _, err := conn.Write(packet); err != nil {
		return err
	}
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	if n < 12 || binary.BigEndian.Uint16(buf[:2]) != 0x5a5a {
		return fmt.Errorf("invalid DNS response")
	}
	return nil
}
