package telegram

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

const summaryListLimit = 20

func (h handler) configSummary() string {
	cfg, err := h.loadConfig()
	if err != nil {
		return "load config: " + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s\nrules: %s\n", h.configPath, h.rulesPath)
	fmt.Fprintf(&b, "network: gateway=%s internal=%s iface=%s\n", cfg.Network.GatewayIP, cfg.Network.InternalCIDR, cfg.Network.IngressIface)
	fmt.Fprintf(&b, "redirect: tcp/80->%d tcp/443->%d udp/443->%d\n", cfg.Network.HTTPRedirectPort, cfg.Network.HTTPSRedirectPort, cfg.Network.QUICRedirectPort)
	for _, proxy := range cfg.UDPProxies {
		fmt.Fprintf(&b, "udp_proxy: udp/%d->%d target=%s exit=%s\n", proxy.ClientPort, proxy.ListenPort, proxy.Target, proxy.Exit)
	}
	fmt.Fprintf(&b, "routing: fallback_exit=%s\n", cfg.Routing.FallbackExit)
	fmt.Fprintf(&b, "dns: binary=%s domain=%s udp=%s tcp=%s dot=%s public_dot=%s\n", cfg.DNS.Binary, cfg.DNS.DOTDomain, cfg.DNS.ListenUDP, cfg.DNS.ListenTCP, cfg.DNS.ListenDOT, cfg.DNS.ListenPublicDOT)
	fmt.Fprintf(&b, "dns pools: cn=%d overseas_private=%d overseas_public=%d\n", len(cfg.DNS.UpstreamsCN), len(cfg.DNS.UpstreamsOverseasPrivate), len(cfg.DNS.UpstreamsOverseasPublic))
	fmt.Fprintf(&b, "ios: enabled=%t base_url=%s\n", cfg.IOS.Enabled, cfg.IOS.BaseURL)
	fmt.Fprintf(&b, "telegram: enabled=%t allowed_users=%d\n", cfg.Telegram.Enabled, len(cfg.Telegram.AllowedUsers))
	writeExitSummary(&b, cfg.Exits)
	return strings.TrimSpace(b.String())
}

func writeExitSummary(b *strings.Builder, exits []config.ExitConfig) {
	fmt.Fprintln(b, "exits:")
	for _, exit := range exits {
		fmt.Fprintf(b, "- %s type=%s", exit.Name, exit.Type)
		if exit.Type == "shadowsocks-rust" {
			fmt.Fprintf(b, " server=%s:%d listen=%s:%d method=%s username=%s tcp=%t udp=%t timeout_seconds=%d",
				exit.Server, exit.ServerPort, exit.ListenAddress, exit.ListenPort, exit.Method, exit.Username, exit.TCPEnabled(), exit.UDPEnabled(), exit.TimeoutSeconds)
		}
		if exit.FWMark != 0 {
			fmt.Fprintf(b, " fwmark=%d", exit.FWMark)
		}
		fmt.Fprintln(b)
	}
}

func (h handler) rulesSummary() string {
	file, err := h.loadRules()
	if err != nil {
		return "load rules: " + err.Error()
	}
	var b strings.Builder
	targets := map[string]int{}
	fmt.Fprintf(&b, "rules: %s\n", h.rulesPath)
	writeImportSummary(&b, file.Imports, targets)
	writeRuleSummary(&b, file.Rules, targets)
	writeTargetSummary(&b, targets)
	return strings.TrimSpace(b.String())
}

func writeImportSummary(b *strings.Builder, imports []rules.Import, targets map[string]int) {
	fmt.Fprintf(b, "imports: %d\n", len(imports))
	for i, imp := range imports {
		targets[targetName(imp.Exit, imp.DNSPool)]++
		if i >= summaryListLimit {
			continue
		}
		fmt.Fprintf(b, "- import %s type=%s target=%s source=%s\n", imp.Name, imp.Type, targetName(imp.Exit, imp.DNSPool), importSource(imp))
	}
	if len(imports) > summaryListLimit {
		fmt.Fprintf(b, "- ... %d more imports\n", len(imports)-summaryListLimit)
	}
}

func writeRuleSummary(b *strings.Builder, ruleList []rules.Rule, targets map[string]int) {
	fmt.Fprintf(b, "inline rules: %d\n", len(ruleList))
	for i, rule := range ruleList {
		targets[targetName(rule.Exit, rule.DNSPool)]++
		if i >= summaryListLimit {
			continue
		}
		fmt.Fprintf(b, "- rule %s target=%s matchers=%d\n", rule.Name, targetName(rule.Exit, rule.DNSPool), ruleMatcherCount(rule))
	}
	if len(ruleList) > summaryListLimit {
		fmt.Fprintf(b, "- ... %d more rules\n", len(ruleList)-summaryListLimit)
	}
}

func writeTargetSummary(b *strings.Builder, targets map[string]int) {
	fmt.Fprintln(b, "targets:")
	for _, key := range sortedKeys(targets) {
		fmt.Fprintf(b, "- %s: %d\n", key, targets[key])
	}
}

func (h handler) logs(lines int) string {
	cfg, err := h.loadConfig()
	if err != nil {
		return "load config: " + err.Error()
	}
	args := []string{"--no-pager", "-n", strconv.Itoa(lines)}
	for _, svc := range serviceNames(cfg, true) {
		args = append(args, "-u", svc)
	}
	return h.runner("journalctl", args...)
}

func (h handler) restartServices() string {
	cfg, err := h.loadConfig()
	if err != nil {
		return "load config: " + err.Error()
	}
	var b strings.Builder
	for _, svc := range serviceNames(cfg, false) {
		out := strings.TrimSpace(h.runner("systemctl", "restart", svc))
		if out == "" {
			out = "ok"
		}
		fmt.Fprintf(&b, "%s: %s\n", svc, out)
	}
	return strings.TrimSpace(b.String())
}

func serviceNames(cfg config.Config, includeBot bool) []string {
	services := []string{"5gws-smartdns.service", "5gws-haproxy.service", "5gws-quic.service"}
	for _, exit := range cfg.Exits {
		if exit.Type == "shadowsocks-rust" {
			services = append(services, "5gws-ssrust-"+exit.Name+".service")
		}
	}
	if cfg.IOS.Enabled {
		services = append(services, "5gws-cert.service")
	}
	if includeBot && cfg.Telegram.Enabled {
		services = append(services, "5gws-bot.service")
	}
	return services
}

func targetName(exit, pool string) string {
	if exit != "" {
		return "exit:" + exit
	}
	if pool != "" {
		return "dns_pool:" + pool
	}
	return "unset"
}

func importSource(imp rules.Import) string {
	if imp.Path != "" {
		return imp.Path
	}
	if imp.URL != "" {
		return imp.URL
	}
	return "(none)"
}

func ruleMatcherCount(rule rules.Rule) int {
	return len(rule.Domain) + len(rule.DomainSuffix) + len(rule.DomainKeyword) +
		len(rule.DomainRegex) + len(rule.IPCIDR) + len(rule.RuleSet)
}

func sortedKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
