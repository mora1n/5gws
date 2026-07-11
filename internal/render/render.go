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
	return GenerateAt(cfg, norm, filepath.Join(cfg.System.StateDir, "rendered"))
}

func GenerateAt(cfg config.Config, norm rules.Normalized, root string) ([]OutputFile, error) {
	if err := validateExits(cfg, norm); err != nil {
		return nil, err
	}
	smartdnsOut, err := smartdns.GenerateAt(cfg, norm, filepath.Join(root, "smartdns"))
	if err != nil {
		return nil, err
	}
	haproxyConfig, aclFiles := HAProxyAt(cfg, norm, root)
	files := []OutputFile{
		{"haproxy/haproxy.cfg", haproxyConfig, 0o600},
		{"nftables/5gws.nft", NFTables(cfg), 0o600},
		{"smartdns/smartdns.conf", smartdnsOut.Config, 0o600},
	}
	files = append(files, aclFiles...)
	for name, content := range smartdnsOut.Files {
		files = append(files, OutputFile{"smartdns/" + name, content, 0o600})
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
		)
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
	out, _ := HAProxyAt(cfg, norm, filepath.Join(cfg.System.StateDir, "rendered"))
	return out
}

func HAProxyAt(cfg config.Config, norm rules.Normalized, root string) (string, []OutputFile) {
	gatewayRules := norm.GatewayRules()
	exits := exitsForHAProxy(norm, cfg.Routing.FallbackExit)
	views, files := buildRuleViews(gatewayRules, root)
	data := struct {
		Config              config.Config
		Rules               []ruleView
		Exits               []exitView
		FallbackExit        string
		HAProxyMaxConn      int
		AccessLog           bool
		RejectEncryptedDNS  bool
		EncryptedDNSDomains []string
	}{
		Config:              cfg,
		Rules:               views,
		Exits:               buildExitViews(cfg, exits),
		FallbackExit:        sanitize(cfg.Routing.FallbackExit),
		HAProxyMaxConn:      *cfg.Network.HAProxyMaxConnections,
		AccessLog:           cfg.Logging.AccessEnabled(),
		RejectEncryptedDNS:  cfg.Network.EncryptedDNSPolicy == "reject",
		EncryptedDNSDomains: encryptedDNSDomains,
	}
	return mustExecute(haproxyTemplate, data), files
}

func NFTables(cfg config.Config) string {
	dnsUDPPort := mustPort(cfg.DNS.ListenUDP)
	quicProxy := cfg.Network.QUICPolicy == "proxy"
	data := struct {
		Config                  config.Config
		DNSUDPPort              int
		DNSTCPPort              int
		DNSDOTPort              int
		QUICProxy               bool
		TCPGatewayExcludedPorts string
		ProtectedTCPPorts       string
		ProtectedUDPPorts       string
	}{
		Config:                  cfg,
		DNSUDPPort:              dnsUDPPort,
		DNSTCPPort:              mustPort(cfg.DNS.ListenTCP),
		DNSDOTPort:              mustPort(cfg.DNS.ListenDOT),
		QUICProxy:               quicProxy,
		TCPGatewayExcludedPorts: joinInts(tcpGatewayExcludedPorts(cfg)),
		ProtectedTCPPorts:       joinInts(protectedTCPPorts(cfg)),
		ProtectedUDPPorts:       joinInts(protectedUDPPorts(cfg, quicProxy)),
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
	ID             string
	Exit           string
	ExactFile      string
	SuffixFile     string
	SuffixRootFile string
	Rule           rules.Rule
}

type exitView struct {
	Name   string
	Type   string
	Socks4 string
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

func buildRuleViews(src []rules.Rule, root string) ([]ruleView, []OutputFile) {
	views := make([]ruleView, 0, len(src))
	var files []OutputFile
	for i, rule := range src {
		id := fmt.Sprintf("r%d", i+1)
		view := ruleView{ID: id, Exit: sanitize(rule.Exit), Rule: rule}
		if len(rule.Domain) > 0 {
			view.ExactFile = filepath.Join(root, "haproxy", "rules", id+"_exact.lst")
			files = append(files, OutputFile{Path: filepath.Join("haproxy", "rules", id+"_exact.lst"), Content: strings.Join(rule.Domain, "\n") + "\n", Mode: 0o600})
		}
		if len(rule.DomainSuffix) > 0 {
			view.SuffixFile = filepath.Join(root, "haproxy", "rules", id+"_suffix.lst")
			view.SuffixRootFile = filepath.Join(root, "haproxy", "rules", id+"_suffix_root.lst")
			suffixes := make([]string, len(rule.DomainSuffix))
			for i, domain := range rule.DomainSuffix {
				suffixes[i] = "." + domain
			}
			files = append(files,
				OutputFile{Path: filepath.Join("haproxy", "rules", id+"_suffix.lst"), Content: strings.Join(suffixes, "\n") + "\n", Mode: 0o600},
				OutputFile{Path: filepath.Join("haproxy", "rules", id+"_suffix_root.lst"), Content: strings.Join(rule.DomainSuffix, "\n") + "\n", Mode: 0o600},
			)
		}
		views = append(views, view)
	}
	return views, files
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
	return uniqueInts(ports)
}

func protectedUDPPorts(cfg config.Config, quicProxy bool) []int {
	ports := []int{mustPort(cfg.DNS.ListenUDP)}
	if quicProxy {
		ports = append(ports, cfg.Network.QUICRedirectPort)
	}
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
{{- if .HAProxyMaxConn }}
    maxconn {{ .HAProxyMaxConn }}
{{- end }}

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
{{- if .ExactFile }}
    acl host_{{ $rv.ID }}_exact var(txn.host) -m str -i -f {{ .ExactFile }}
{{- end }}
{{- if .SuffixFile }}
    acl host_{{ $rv.ID }}_suffix var(txn.host) -m end -i -f {{ .SuffixFile }}
    acl host_{{ $rv.ID }}_root var(txn.host) -m str -i -f {{ .SuffixRootFile }}
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
{{- if .ExactFile }}
    acl sni_{{ $rv.ID }}_exact req.ssl_sni,lower -m str -i -f {{ .ExactFile }}
{{- end }}
{{- if .SuffixFile }}
    acl sni_{{ $rv.ID }}_suffix req.ssl_sni,lower -m end -i -f {{ .SuffixFile }}
    acl sni_{{ $rv.ID }}_root req.ssl_sni,lower -m str -i -f {{ .SuffixRootFile }}
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
        iifname {{ quote .Config.Network.IngressIface }} ip saddr {{ .Config.Network.InternalCIDR }} ip daddr {{ .Config.Network.GatewayIP }} tcp dport != { {{ .TCPGatewayExcludedPorts }} } counter redirect to :{{ .Config.Network.TCPRedirectPort }}
    }

    chain input {
        type filter hook input priority filter; policy accept;
        iifname {{ quote .Config.Network.IngressIface }} ip saddr != {{ .Config.Network.InternalCIDR }} udp dport { {{ .ProtectedUDPPorts }} } drop
        iifname {{ quote .Config.Network.IngressIface }} ip saddr != {{ .Config.Network.InternalCIDR }} tcp dport { {{ .ProtectedTCPPorts }} } reject with tcp reset
    }
}
`
