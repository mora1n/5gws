package render

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/smartdns"
	"github.com/morain/5gws/internal/ssrust"
)

type OutputFile struct {
	Path    string
	Content string
	Mode    os.FileMode
}

func Generate(cfg config.Config, norm rules.Normalized) ([]OutputFile, error) {
	if err := validateExits(cfg, norm); err != nil {
		return nil, err
	}
	smartdnsConf, err := smartdns.Config(cfg, norm)
	if err != nil {
		return nil, err
	}
	files := []OutputFile{
		{"haproxy/haproxy.cfg", HAProxy(cfg, norm), 0o600},
		{"nftables/5gws.nft", NFTables(cfg), 0o600},
		{"smartdns/smartdns.conf", smartdnsConf, 0o600},
		{"systemd/5gws-smartdns.service", serviceUnit("5gws smartdns-rs", smartDNSCommand(cfg)), 0o644},
		{"systemd/5gws-haproxy.service", serviceUnit("5gws haproxy", "/usr/sbin/haproxy -Ws -f "+cfg.System.StateDir+"/rendered/haproxy/haproxy.cfg"), 0o644},
		{"systemd/5gws-quic.service", serviceUnit("5gws quic gateway", "/usr/local/bin/5gws quicgw --config "+cfg.System.ConfigDir+"/config.toml --rules "+cfg.System.ConfigDir+"/rules.toml"), 0o644},
	}
	for _, exit := range cfg.Exits {
		if exit.Type != "shadowsocks-rust" {
			continue
		}
		ssConfig, err := ssrust.Config(exit)
		if err != nil {
			return nil, err
		}
		configPath := "ssrust/" + sanitize(exit.Name) + ".json"
		files = append(files,
			OutputFile{configPath, ssConfig, 0o600},
			OutputFile{"systemd/" + ssrust.ServiceName(exit), serviceUnit("5gws shadowsocks-rust "+exit.Name, "/usr/local/bin/sslocal -c "+cfg.System.StateDir+"/rendered/"+configPath), 0o644},
		)
	}
	if cfg.IOS.Enabled {
		files = append(files, OutputFile{"systemd/5gws-cert.service", serviceUnit("5gws iOS certificate server", "/usr/local/bin/5gws cert-server --config "+cfg.System.ConfigDir+"/config.toml --dir "+cfg.System.StateDir+"/ios"), 0o644})
	}
	if cfg.Telegram.Enabled {
		files = append(files, OutputFile{"systemd/5gws-bot.service", serviceUnit("5gws telegram bot", "/usr/local/bin/5gws bot --config "+cfg.System.ConfigDir+"/config.toml --rules "+cfg.System.ConfigDir+"/rules.toml"), 0o644})
	}
	return files, nil
}

func WriteAll(outDir string, files []OutputFile) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	for _, file := range files {
		path := filepath.Join(outDir, file.Path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(file.Content), file.Mode); err != nil {
			return err
		}
	}
	return nil
}

func validateExits(cfg config.Config, norm rules.Normalized) error {
	if err := validateTCPExit(cfg, cfg.Routing.FallbackExit, "routing.fallback_exit"); err != nil {
		return err
	}
	for _, rule := range norm.GatewayRules() {
		if err := validateTCPExit(cfg, rule.Exit, fmt.Sprintf("rule %q", rule.Name)); err != nil {
			return err
		}
	}
	return nil
}

func validateTCPExit(cfg config.Config, name, label string) error {
	exit, ok := cfg.ExitByName(name)
	if !ok {
		return fmt.Errorf("%s references unknown exit %q", label, name)
	}
	if exit.Type == "shadowsocks-rust" && !exit.TCPEnabled() {
		return fmt.Errorf("%s references exit %q with tcp=false; HAProxy requires a TCP-capable exit", label, name)
	}
	return nil
}

func HAProxy(cfg config.Config, norm rules.Normalized) string {
	gatewayRules := norm.GatewayRules()
	exits := exitsForHAProxy(norm, cfg.Routing.FallbackExit)
	data := struct {
		Config              config.Config
		Rules               []ruleView
		Exits               []exitView
		FallbackExit        string
		AccessLog           bool
		RejectEncryptedDNS  bool
		EncryptedDNSDomains []string
	}{
		Config:              cfg,
		Rules:               buildRuleViews(gatewayRules),
		Exits:               buildExitViews(cfg, exits),
		FallbackExit:        sanitize(cfg.Routing.FallbackExit),
		AccessLog:           cfg.Logging.AccessEnabled(),
		RejectEncryptedDNS:  cfg.Network.EncryptedDNSPolicy == "reject",
		EncryptedDNSDomains: encryptedDNSDomains,
	}
	return mustExecute(haproxyTemplate, data)
}

func NFTables(cfg config.Config) string {
	dnsUDPPort := mustPort(cfg.DNS.ListenUDP)
	udpProxies := buildUDPProxyViews(cfg.UDPProxies)
	quicProxy := cfg.Network.QUICPolicy == "proxy"
	data := struct {
		Config                  config.Config
		DNSUDPPort              int
		DNSTCPPort              int
		DNSDOTPort              int
		QUICProxy               bool
		TCPGatewayExcludedPorts string
		TCPProxies              []tcpProxyView
		UDPProxies              []udpProxyView
		ProtectedTCPPorts       string
		ProtectedUDPPorts       string
	}{
		Config:                  cfg,
		DNSUDPPort:              dnsUDPPort,
		DNSTCPPort:              mustPort(cfg.DNS.ListenTCP),
		DNSDOTPort:              mustPort(cfg.DNS.ListenDOT),
		QUICProxy:               quicProxy,
		TCPGatewayExcludedPorts: joinInts(tcpGatewayExcludedPorts(cfg)),
		TCPProxies:              buildTCPProxyViews(cfg.TCPProxies),
		UDPProxies:              udpProxies,
		ProtectedTCPPorts:       joinInts(protectedTCPPorts(cfg)),
		ProtectedUDPPorts:       joinInts(protectedUDPPorts(cfg, udpProxies, quicProxy)),
	}
	return mustExecute(nftTemplate, data)
}

func exitsForHAProxy(norm rules.Normalized, fallback string) []string {
	set := map[string]bool{}
	for _, rule := range norm.GatewayRules() {
		set[rule.Exit] = true
	}
	set[fallback] = true
	var exits []string
	for exit := range set {
		exits = append(exits, exit)
	}
	sort.Strings(exits)
	return exits
}

type ruleView struct {
	ID   string
	Exit string
	Rule rules.Rule
}

type exitView struct {
	Name   string
	Type   string
	Socks4 string
}

type udpProxyView struct {
	ClientPort int
	ListenPort int
}

type tcpProxyView struct {
	Name       string
	ClientPort int
	ListenPort int
}

var encryptedDNSDomains = []string{
	"cloudflare-dns.com",
	"dns.google",
	"dns.google.com",
	"dns.quad9.net",
	"dns.adguard-dns.com",
	"dns.nextdns.io",
	"one.one.one.one",
	"1dot1dot1dot1.cloudflare-dns.com",
}

func buildRuleViews(src []rules.Rule) []ruleView {
	views := make([]ruleView, 0, len(src))
	for i, rule := range src {
		views = append(views, ruleView{ID: fmt.Sprintf("r%d", i+1), Exit: sanitize(rule.Exit), Rule: rule})
	}
	return views
}

func buildExitViews(cfg config.Config, names []string) []exitView {
	views := make([]exitView, 0, len(names))
	for _, name := range names {
		exit, _ := cfg.ExitByName(name)
		view := exitView{Name: name, Type: exit.Type}
		if exit.Type == "shadowsocks-rust" {
			view.Socks4 = ssrust.LocalAddr(exit)
		}
		views = append(views, view)
	}
	return views
}

func buildTCPProxyViews(src []config.TCPProxyConfig) []tcpProxyView {
	views := make([]tcpProxyView, 0, len(src))
	for _, proxy := range src {
		views = append(views, tcpProxyView{
			Name:       proxy.Name,
			ClientPort: proxy.ClientPort,
			ListenPort: proxy.ListenPort,
		})
	}
	return views
}

func buildUDPProxyViews(src []config.UDPProxyConfig) []udpProxyView {
	views := make([]udpProxyView, 0, len(src))
	for _, proxy := range src {
		views = append(views, udpProxyView{ClientPort: proxy.ClientPort, ListenPort: proxy.ListenPort})
	}
	return views
}

func tcpProxyListenPorts(proxies []config.TCPProxyConfig) []int {
	ports := make([]int, 0, len(proxies))
	for _, proxy := range proxies {
		ports = append(ports, proxy.ListenPort)
	}
	return ports
}

func udpProxyListenPorts(proxies []udpProxyView) []int {
	ports := make([]int, 0, len(proxies))
	for _, proxy := range proxies {
		ports = append(ports, proxy.ListenPort)
	}
	return ports
}

func tcpGatewayExcludedPorts(cfg config.Config) []int {
	ports := []int{
		53,
		80,
		443,
		mustPort(cfg.DNS.ListenTCP),
		mustPort(cfg.DNS.ListenDOT),
		cfg.Network.HTTPRedirectPort,
		cfg.Network.HTTPSRedirectPort,
		cfg.Network.TCPRedirectPort,
	}
	if cfg.DNS.ListenPublicDOT != "" {
		ports = append(ports, mustPort(cfg.DNS.ListenPublicDOT))
	}
	for _, proxy := range cfg.TCPProxies {
		ports = append(ports, proxy.ClientPort, proxy.ListenPort)
	}
	return uniqueInts(ports)
}

func protectedTCPPorts(cfg config.Config) []int {
	ports := []int{
		mustPort(cfg.DNS.ListenTCP),
		mustPort(cfg.DNS.ListenDOT),
		cfg.Network.HTTPRedirectPort,
		cfg.Network.HTTPSRedirectPort,
		cfg.Network.TCPRedirectPort,
	}
	ports = append(ports, tcpProxyListenPorts(cfg.TCPProxies)...)
	return uniqueInts(ports)
}

func protectedUDPPorts(cfg config.Config, udpProxies []udpProxyView, quicProxy bool) []int {
	ports := []int{mustPort(cfg.DNS.ListenUDP)}
	if quicProxy {
		ports = append(ports, cfg.Network.QUICRedirectPort)
	}
	ports = append(ports, udpProxyListenPorts(udpProxies)...)
	return uniqueInts(ports)
}

func uniqueInts(values []int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

func joinInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.Itoa(value))
	}
	return strings.Join(parts, ", ")
}

func sanitize(value string) string {
	re := regexp.MustCompile(`[^A-Za-z0-9_]+`)
	out := re.ReplaceAllString(value, "_")
	return strings.Trim(out, "_")
}

func serviceUnit(description, execStart string) string {
	return mustExecute(serviceTemplate, struct {
		Description string
		ExecStart   string
	}{description, execStart})
}

func mustExecute(tmpl string, data any) string {
	t := template.Must(template.New("render").Funcs(template.FuncMap{
		"aclAny":    aclAny,
		"aclAnyAll": aclAnyAll,
		"quote":     quote,
		"san":       sanitize,
	}).Parse(tmpl))
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		panic(err)
	}
	return buf.String()
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func serviceBinary(name string) string {
	if strings.Contains(name, "/") {
		return name
	}
	return "/usr/local/bin/" + name
}

func smartDNSCommand(cfg config.Config) string {
	cmd := serviceBinary(cfg.DNS.Binary) + " run -c " + cfg.System.StateDir + "/rendered/smartdns/smartdns.conf"
	switch cfg.Logging.Level {
	case "debug":
		return cmd + " -v"
	case "warn":
		return cmd + " -q"
	case "error":
		return cmd + " -q -q"
	default:
		return cmd
	}
}

func mustPort(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		panic(err)
	}
	return value
}

const haproxyTemplate = `# Generated by 5gws. Do not edit.
global
    log stdout format raw local0
    stats socket {{ .Config.System.RunDir }}/haproxy.sock mode 660 level admin

defaults
    log global
    timeout connect 5s
    timeout client  60s
    timeout server  60s

resolvers realdns
{{- range $i, $resolver := .Config.DNS.BackendResolvers }}
    nameserver dns{{ $i }} {{ $resolver }}
{{- end }}
    accepted_payload_size 8192
    resolve_retries 3
    timeout retry 1s
    hold valid 10s

frontend http_in
    bind 0.0.0.0:{{ .Config.Network.HTTPRedirectPort }}
    mode http
{{- if .AccessLog }}
    option httplog
    log-format "5gws http src=%ci:%cp fe=%ft be=%b host=%[var(txn.host)] status=%ST bytes=%B term=%ts"
{{- end }}
    http-request set-var(txn.host) req.hdr(host),lower
    acl has_host var(txn.host) -m found
{{- if .RejectEncryptedDNS }}
{{- range .EncryptedDNSDomains }}
    acl encrypted_dns_host var(txn.host) -m str -i {{ . }}
    acl encrypted_dns_host var(txn.host) -m end -i .{{ . }}
{{- end }}
{{- end }}
{{- range .Rules }}
{{- $rv := . }}
{{- range .Rule.Domain }}
    acl host_{{ $rv.ID }}_exact var(txn.host) -m str -i {{ . }}
{{- end }}
{{- range .Rule.DomainSuffix }}
    acl host_{{ $rv.ID }}_suffix var(txn.host) -m end -i .{{ . }}
    acl host_{{ $rv.ID }}_root var(txn.host) -m str -i {{ . }}
{{- end }}
{{- range .Rule.DomainKeyword }}
    acl host_{{ $rv.ID }}_keyword var(txn.host) -m sub -i {{ . }}
{{- end }}
{{- range .Rule.DomainRegex }}
    acl host_{{ $rv.ID }}_regex var(txn.host) -m reg {{ . }}
{{- end }}
{{- end }}
    http-request deny deny_status 403 unless has_host
{{- if .RejectEncryptedDNS }}
    http-request deny deny_status 403 if encrypted_dns_host
{{- end }}
{{- range .Rules }}
    use_backend http_{{ san .Exit }} if {{ aclAny "host" . }}
{{- end }}
    default_backend http_{{ .FallbackExit }}

frontend tls_in
    bind 0.0.0.0:{{ .Config.Network.HTTPSRedirectPort }}
    mode tcp
{{- if .AccessLog }}
    option tcplog
    log-format "5gws tls src=%ci:%cp fe=%ft be=%b sni=%[var(sess.sni)] dst=%[var(sess.dst)] bytes=%B term=%ts"
{{- end }}
    tcp-request inspect-delay 5s
    acl has_sni req.ssl_sni -m found
    acl client_hello req_ssl_hello_type 1
    tcp-request content set-var(sess.sni) req.ssl_sni if has_sni
{{- if .RejectEncryptedDNS }}
{{- range .EncryptedDNSDomains }}
    acl encrypted_dns_sni req.ssl_sni,lower -m str -i {{ . }}
    acl encrypted_dns_sni req.ssl_sni,lower -m end -i .{{ . }}
{{- end }}
    tcp-request content reject if encrypted_dns_sni
{{- end }}
    tcp-request content do-resolve(sess.dst,realdns,ipv4) var(sess.sni) if has_sni
    tcp-request content set-dst var(sess.dst) if has_sni
    tcp-request content reject if client_hello !has_sni
    tcp-request content accept if client_hello
{{- range .Rules }}
{{- $rv := . }}
{{- range .Rule.Domain }}
    acl sni_{{ $rv.ID }}_exact req.ssl_sni,lower -m str -i {{ . }}
{{- end }}
{{- range .Rule.DomainSuffix }}
    acl sni_{{ $rv.ID }}_suffix req.ssl_sni,lower -m end -i .{{ . }}
    acl sni_{{ $rv.ID }}_root req.ssl_sni,lower -m str -i {{ . }}
{{- end }}
{{- range .Rule.DomainKeyword }}
    acl sni_{{ $rv.ID }}_keyword req.ssl_sni,lower -m sub -i {{ . }}
{{- end }}
{{- range .Rule.DomainRegex }}
    acl sni_{{ $rv.ID }}_regex req.ssl_sni,lower -m reg {{ . }}
{{- end }}
{{- end }}
{{- range .Rules }}
    use_backend tls_{{ san .Exit }} if {{ aclAny "sni" . }}
{{- end }}
    default_backend tls_{{ .FallbackExit }}

{{- range .Exits }}
backend http_{{ san .Name }}
    mode http
    http-request do-resolve(txn.dst,realdns,ipv4) var(txn.host)
    http-request set-dst var(txn.dst)
    server dyn 0.0.0.0:80{{ if .Socks4 }} socks4 {{ .Socks4 }}{{ end }}

backend tls_{{ san .Name }}
    mode tcp
    server dyn 0.0.0.0:443{{ if .Socks4 }} socks4 {{ .Socks4 }}{{ end }}
{{ end }}
`

const nftTemplate = `#!/usr/sbin/nft -f
# Generated by 5gws. This replaces only table inet fivegws.
destroy table inet fivegws

table inet fivegws {
{{- if not .QUICProxy }}
    chain early_filter {
        type filter hook prerouting priority filter; policy accept;
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} ip daddr {{ .Config.Network.GatewayIP }} udp dport 443 counter reject
    }

{{- end }}
    chain prerouting {
        type nat hook prerouting priority dstnat; policy accept;
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} udp dport 53 counter redirect to :{{ .DNSUDPPort }}
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} tcp dport 53 counter redirect to :{{ .DNSTCPPort }}
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} tcp dport 853 counter redirect to :{{ .DNSDOTPort }}
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} ip daddr {{ .Config.Network.GatewayIP }} tcp dport 80 counter redirect to :{{ .Config.Network.HTTPRedirectPort }}
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} ip daddr {{ .Config.Network.GatewayIP }} tcp dport 443 counter redirect to :{{ .Config.Network.HTTPSRedirectPort }}
{{- if .QUICProxy }}
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} ip daddr {{ .Config.Network.GatewayIP }} udp dport 443 counter redirect to :{{ .Config.Network.QUICRedirectPort }}
{{- end }}
{{- range .TCPProxies }}
        iifname {{ quote $.Config.Network.IngressIface }} ip saddr {{ $.Config.Network.InternalCIDR }} ip daddr {{ $.Config.Network.GatewayIP }} tcp dport {{ .ClientPort }} counter redirect to :{{ .ListenPort }}
{{- end }}
{{- range .UDPProxies }}
        iifname {{ quote $.Config.Network.IngressIface }} ip saddr {{ $.Config.Network.InternalCIDR }} ip daddr {{ $.Config.Network.GatewayIP }} udp dport {{ .ClientPort }} counter redirect to :{{ .ListenPort }}
{{- end }}
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} ip daddr {{ .Config.Network.GatewayIP }} tcp dport != { {{ .TCPGatewayExcludedPorts }} } counter redirect to :{{ .Config.Network.TCPRedirectPort }}
    }

    chain input {
        type filter hook input priority filter; policy accept;
        iifname {{ quote .Config.Network.IngressIface }} ip saddr != {{ .Config.Network.InternalCIDR }} udp dport { {{ .ProtectedUDPPorts }} } drop
        iifname {{ quote .Config.Network.IngressIface }} ip saddr != {{ .Config.Network.InternalCIDR }} tcp dport { {{ .ProtectedTCPPorts }} } reject with tcp reset
    }
}
`

const serviceTemplate = `[Unit]
Description={{ .Description }}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{ .ExecStart }}
Restart=on-failure
RestartSec=2s

[Install]
WantedBy=multi-user.target
`
