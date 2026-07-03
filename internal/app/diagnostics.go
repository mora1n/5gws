package app

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/morain/5gws/internal/config"
)

func cmdLogs(args []string, out io.Writer) error {
	fs := newCommandFlags("logs")
	cfgPath := fs.String("config", "c", defaultConfigPath, "config.toml path")
	component := fs.String("component", "m", "all", "all|smartdns|haproxy|quic|cert|bot|ssrust")
	since := fs.String("since", "s", "1h", "duration like 10m/1h or journalctl --since value")
	lines := fs.Int("lines", "n", 200, "number of lines")
	follow := fs.Bool("follow", "f", false, "follow logs")
	if err := fs.parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	services, err := logServices(cfg, *component)
	if err != nil {
		return err
	}
	jargs := []string{"--no-pager"}
	if normalizedSince := normalizeJournalSince(*since, time.Now()); normalizedSince != "" {
		jargs = append(jargs, "--since", normalizedSince)
	}
	if *lines > 0 {
		jargs = append(jargs, "-n", strconv.Itoa(*lines))
	}
	if *follow {
		jargs = append(jargs, "-f")
	}
	for _, svc := range services {
		jargs = append(jargs, "-u", svc)
	}
	return run(out, "journalctl", jargs...)
}

func normalizeJournalSince(value string, now time.Time) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	duration, err := time.ParseDuration(trimmed)
	if err != nil || duration <= 0 {
		return trimmed
	}
	return now.Add(-duration).Format("2006-01-02 15:04:05")
}

func logServices(cfg config.Config, component string) ([]string, error) {
	switch component {
	case "all":
		return activeServices(cfg), nil
	case "smartdns":
		return []string{"5gws-smartdns.service"}, nil
	case "haproxy":
		return []string{"5gws-haproxy.service"}, nil
	case "quic":
		return []string{"5gws-quic.service"}, nil
	case "cert":
		return []string{"5gws-cert.service"}, nil
	case "bot":
		return []string{"5gws-bot.service"}, nil
	case "ssrust":
		var services []string
		for _, exit := range cfg.Exits {
			if exit.Type == "shadowsocks-rust" {
				services = append(services, "5gws-ssrust-"+exit.Name+".service")
			}
		}
		if len(services) == 0 {
			return nil, errors.New("no shadowsocks-rust exits configured")
		}
		return services, nil
	default:
		return nil, fmt.Errorf("unknown logs component %q", component)
	}
}

func cmdDetectCIDR(args []string, out io.Writer) error {
	fs := newCommandFlags("detect-cidr")
	cfgPath := fs.String("config", "c", defaultConfigPath, "config.toml path")
	iface := fs.String("iface", "i", "", "interface to observe")
	seconds := fs.Int("seconds", "s", 30, "capture duration")
	count := fs.Int("count", "n", 80, "maximum packets")
	if err := fs.parse(args); err != nil {
		return err
	}
	if *seconds <= 0 {
		return errors.New("seconds must be > 0")
	}
	if *count <= 0 {
		return errors.New("count must be > 0")
	}
	selectedIface := *iface
	if selectedIface == "" {
		cfg, err := config.Load(*cfgPath)
		if err == nil {
			selectedIface = cfg.Network.IngressIface
		}
	}
	if selectedIface == "" {
		data, err := exec.Command("ip", "route", "show", "default").Output()
		if err != nil {
			return fmt.Errorf("detect default interface: %w", err)
		}
		selectedIface = parseDefaultIface(string(data))
	}
	if selectedIface == "" {
		return errors.New("cannot detect interface; pass --iface")
	}
	fmt.Fprintf(out, "observing %s for %s; reproduce Android Private DNS/Chrome/YouTube now\n", selectedIface, time.Duration(*seconds)*time.Second)
	output, err := runTCPDump(selectedIface, *seconds, *count)
	if err != nil {
		return err
	}
	sources := parseTCPDumpSources(output)
	if len(sources) == 0 {
		fmt.Fprintln(out, "no matching packets observed")
		fmt.Fprintln(out, "check Android network, DoT domain resolution, and network.ingress_iface")
		return nil
	}
	fmt.Fprintln(out, "observed source IPs:")
	for _, ip := range sources {
		fmt.Fprintf(out, "- %s\n", ip)
	}
	suggestions := suggestCIDRs(sources)
	if len(suggestions) > 0 {
		fmt.Fprintln(out, "suggested internal_cidr values:")
		for _, cidr := range suggestions {
			fmt.Fprintf(out, "- %s\n", cidr)
		}
	}
	return nil
}

func runTCPDump(iface string, seconds, count int) (string, error) {
	filter := "(tcp port 53 or tcp port 80 or tcp port 443 or tcp port 853 or tcp port 5060 or tcp port 8080 or udp port 53 or udp port 443 or udp port 3478 or udp port 19302)"
	args := []string{
		strconv.Itoa(seconds) + "s",
		"tcpdump",
		"-ni", iface,
		"-tt",
		"-c", strconv.Itoa(count),
		filter,
	}
	cmd := exec.Command("timeout", args...)
	data, err := cmd.CombinedOutput()
	if err == nil {
		return string(data), nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 124 {
		return string(data), nil
	}
	return "", fmt.Errorf("tcpdump failed: %w\n%s", err, strings.TrimSpace(string(data)))
}

func parseTCPDumpSources(output string) []string {
	re := regexp.MustCompile(`\bIP ([0-9]{1,3}(?:\.[0-9]{1,3}){3})(?:\.[0-9]+)? >`)
	seen := map[string]bool{}
	for _, match := range re.FindAllStringSubmatch(output, -1) {
		ip := net.ParseIP(match[1])
		if ip == nil || ip.To4() == nil {
			continue
		}
		seen[ip.String()] = true
	}
	out := make([]string, 0, len(seen))
	for ip := range seen {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}

func suggestCIDRs(ips []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range ips {
		ip := net.ParseIP(value).To4()
		if ip == nil {
			continue
		}
		cidr := suggestedCIDR(ip)
		if cidr == "" || seen[cidr] {
			continue
		}
		seen[cidr] = true
		out = append(out, cidr)
	}
	sort.Strings(out)
	return out
}

func suggestedCIDR(ip net.IP) string {
	switch {
	case ip[0] == 10:
		return "10.0.0.0/8"
	case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
		return fmt.Sprintf("172.%d.0.0/16", ip[1])
	case ip[0] == 192 && ip[1] == 168:
		return "192.168.0.0/16"
	case ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127:
		return "100.64.0.0/10"
	default:
		return ip.String() + "/32"
	}
}

func writeDoctorRuntime(out io.Writer, cfg config.Config) {
	fmt.Fprintln(out, "redirect_mode: nft redirect only; public 80/443 are not listened by 5gws")
	fmt.Fprintf(out, "redirect_match: iifname=%s source=%s\n", cfg.Network.IngressIface, cfg.Network.InternalCIDR)
	fmt.Fprintf(out, "redirect_gateway_ip: %s\n", cfg.Network.GatewayIP)
	fmt.Fprintf(out, "redirect_tcp_gateway: gateway_ip tcp other -> %d\n", cfg.Network.TCPRedirectPort)
	fmt.Fprintf(out, "quic_policy: %s\n", cfg.Network.QUICPolicy)
	fmt.Fprintf(out, "encrypted_dns_policy: %s\n", cfg.Network.EncryptedDNSPolicy)
	data, err := exec.Command("nft", "list", "chain", "inet", "fivegws", "prerouting").CombinedOutput()
	if err != nil {
		fmt.Fprintf(out, "nft_prerouting: unavailable: %v\n", err)
		if len(data) > 0 {
			fmt.Fprint(out, strings.TrimSpace(string(data))+"\n")
		}
		return
	}
	lines := redirectCounterLines(string(data))
	if len(lines) == 0 {
		fmt.Fprintln(out, "nft_prerouting: no redirect counters found")
		return
	}
	fmt.Fprintln(out, "nft_prerouting:")
	for _, line := range lines {
		fmt.Fprintf(out, "  %s\n", line)
	}
}

func redirectCounterLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "redirect to") {
			lines = append(lines, compactSpaces(line))
		}
	}
	return lines
}

func compactSpaces(line string) string {
	return strings.Join(strings.Fields(line), " ")
}
