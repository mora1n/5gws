package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	System  SystemConfig  `toml:"system" json:"system"`
	Panel   PanelConfig   `toml:"panel" json:"panel"`
	Network NetworkConfig `toml:"network" json:"network"`
	Routing RoutingConfig `toml:"routing" json:"routing"`
	DNS     DNSConfig     `toml:"dns" json:"dns"`
	Logging LoggingConfig `toml:"logging" json:"logging"`
	IOS     IOSConfig     `toml:"ios" json:"ios"`
	Exits   []ExitConfig  `toml:"exits" json:"exits"`
}

type SystemConfig struct {
	ConfigDir string `toml:"config_dir" json:"config_dir"`
	StateDir  string `toml:"state_dir" json:"state_dir"`
	RunDir    string `toml:"run_dir" json:"run_dir"`
	User      string `toml:"user" json:"user"`
}

type PanelConfig struct {
	Listen       string   `toml:"listen" json:"listen"`
	AllowedCIDRs []string `toml:"allowed_cidrs" json:"allowed_cidrs"`
}

type NetworkConfig struct {
	GatewayIP             string `toml:"gateway_ip" json:"gateway_ip"`
	InternalCIDR          string `toml:"internal_cidr" json:"internal_cidr"`
	IngressIface          string `toml:"ingress_iface" json:"ingress_iface"`
	HTTPRedirectPort      int    `toml:"http_redirect_port" json:"http_redirect_port"`
	HTTPSRedirectPort     int    `toml:"https_redirect_port" json:"https_redirect_port"`
	QUICRedirectPort      int    `toml:"quic_redirect_port" json:"quic_redirect_port"`
	TCPRedirectPort       int    `toml:"tcp_redirect_port" json:"tcp_redirect_port"`
	HAProxyMaxConnections *int   `toml:"haproxy_max_connections" json:"haproxy_max_connections"`
	QUICPolicy            string `toml:"quic_policy" json:"quic_policy"`
	EncryptedDNSPolicy    string `toml:"encrypted_dns_policy" json:"encrypted_dns_policy"`
}

const DefaultHAProxyMaxConnections = 16384

type RoutingConfig struct {
	FallbackExit string `toml:"fallback_exit" json:"fallback_exit"`
}

type DNSConfig struct {
	Binary                   string   `toml:"binary" json:"binary"`
	DOTDomain                string   `toml:"dot_domain" json:"dot_domain"`
	ListenUDP                string   `toml:"listen_udp" json:"listen_udp"`
	ListenTCP                string   `toml:"listen_tcp" json:"listen_tcp"`
	ListenDOT                string   `toml:"listen_dot" json:"listen_dot"`
	ListenPublicDOT          string   `toml:"listen_public_dot" json:"listen_public_dot"`
	BackendResolvers         []string `toml:"backend_resolvers" json:"backend_resolvers"`
	CertDir                  string   `toml:"cert_dir" json:"cert_dir"`
	CertFile                 string   `toml:"cert_file" json:"cert_file"`
	KeyFile                  string   `toml:"key_file" json:"key_file"`
	CacheSize                int      `toml:"cache_size" json:"cache_size"`
	UpstreamsCN              []string `toml:"upstreams_cn" json:"upstreams_cn"`
	UpstreamsOverseasPrivate []string `toml:"upstreams_overseas_private" json:"upstreams_overseas_private"`
	UpstreamsOverseasPublic  []string `toml:"upstreams_overseas_public" json:"upstreams_overseas_public"`
}

type LoggingConfig struct {
	Level  string `toml:"level" json:"level"`
	Access *bool  `toml:"access" json:"access"`
}

type IOSConfig struct {
	Enabled           bool   `toml:"enabled" json:"enabled"`
	Listen            string `toml:"listen" json:"listen"`
	BaseURL           string `toml:"base_url" json:"base_url"`
	Organization      string `toml:"organization" json:"organization"`
	ProfileIdentifier string `toml:"profile_identifier" json:"profile_identifier"`
}

type ExitConfig struct {
	Name           string `toml:"name" json:"name"`
	Type           string `toml:"type" json:"type"`
	FWMark         int    `toml:"fwmark" json:"fwmark"`
	Server         string `toml:"server" json:"server"`
	ServerPort     int    `toml:"server_port" json:"server_port"`
	Method         string `toml:"method" json:"method"`
	Password       string `toml:"password" json:"password"`
	Username       string `toml:"username" json:"username"`
	ListenAddress  string `toml:"listen_address" json:"listen_address"`
	ListenPort     int    `toml:"listen_port" json:"listen_port"`
	TCP            *bool  `toml:"tcp" json:"tcp"`
	UDP            *bool  `toml:"udp" json:"udp"`
	TimeoutSeconds int    `toml:"timeout_seconds" json:"timeout_seconds"`
}

func Load(path string) (Config, error) {
	cfg, err := LoadDraft(path)
	if err != nil {
		return Config{}, err
	}
	return cfg, cfg.Validate()
}

func LoadDraft(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

func (c *Config) ApplyDefaults() {
	if c.System.ConfigDir == "" {
		c.System.ConfigDir = "/etc/5gws"
	}
	if c.System.StateDir == "" {
		c.System.StateDir = "/var/lib/5gws"
	}
	if c.System.RunDir == "" {
		c.System.RunDir = "/run/5gws"
	}
	if c.System.User == "" {
		c.System.User = "5gws"
	}
	if c.Panel.Listen == "" {
		c.Panel.Listen = "127.0.0.1:19443"
	}
	if len(c.Panel.AllowedCIDRs) == 0 && c.Network.InternalCIDR != "" {
		c.Panel.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128", c.Network.InternalCIDR}
	}
	if c.Network.HTTPRedirectPort == 0 {
		c.Network.HTTPRedirectPort = 18080
	}
	if c.Network.HTTPSRedirectPort == 0 {
		c.Network.HTTPSRedirectPort = 18443
	}
	if c.Network.QUICRedirectPort == 0 {
		c.Network.QUICRedirectPort = 18443
	}
	if c.Network.TCPRedirectPort == 0 {
		c.Network.TCPRedirectPort = 18082
	}
	if c.Network.HAProxyMaxConnections == nil {
		value := DefaultHAProxyMaxConnections
		c.Network.HAProxyMaxConnections = &value
	}
	if c.Network.QUICPolicy == "" {
		c.Network.QUICPolicy = "reject"
	}
	if c.Network.EncryptedDNSPolicy == "" {
		c.Network.EncryptedDNSPolicy = "reject"
	}
	if c.Routing.FallbackExit == "" {
		c.Routing.FallbackExit = "direct"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.DNS.Binary == "" {
		c.DNS.Binary = "smartdns"
	}
	if c.DNS.ListenUDP == "" {
		c.DNS.ListenUDP = "0.0.0.0:1053"
	}
	if c.DNS.ListenTCP == "" {
		c.DNS.ListenTCP = "0.0.0.0:1053"
	}
	if c.DNS.ListenDOT == "" {
		c.DNS.ListenDOT = "0.0.0.0:1853"
	}
	if c.DNS.ListenPublicDOT == "" {
		c.DNS.ListenPublicDOT = "0.0.0.0:853"
	}
	if len(c.DNS.BackendResolvers) == 0 {
		c.DNS.BackendResolvers = []string{
			"1.1.1.1:53",
			"1.0.0.1:53",
			"8.8.8.8:53",
			"8.8.4.4:53",
			"9.9.9.9:53",
			"22.22.22.22:53",
		}
	}
	if c.DNS.CertDir == "" {
		c.DNS.CertDir = c.System.StateDir + "/ios"
	}
	if c.DNS.CertFile == "" {
		c.DNS.CertFile = c.System.ConfigDir + "/certs/fullchain.pem"
	}
	if c.DNS.KeyFile == "" {
		c.DNS.KeyFile = c.System.ConfigDir + "/certs/privkey.pem"
	}
	legacyIOSURL := "http://" + c.Network.GatewayIP + ":8088"
	if (c.IOS.BaseURL == "" || c.IOS.BaseURL == legacyIOSURL) && c.DNS.DOTDomain != "" {
		c.IOS.BaseURL = "https://" + c.DNS.DOTDomain
	}
	if c.IOS.Organization == "" {
		c.IOS.Organization = "5gws"
	}
	if c.IOS.ProfileIdentifier == "" {
		c.IOS.ProfileIdentifier = "dev.5gws.dot"
	}
	if c.DNS.CacheSize == 0 {
		c.DNS.CacheSize = 32768
	}
	if len(c.DNS.UpstreamsCN) == 0 || usesLegacyCNUpstreams(c.DNS.UpstreamsCN) {
		c.DNS.UpstreamsCN = defaultCNUpstreams()
	}
	if len(c.DNS.UpstreamsOverseasPrivate) == 0 {
		c.DNS.UpstreamsOverseasPrivate = []string{
			"22.22.22.22",
		}
	}
	if len(c.DNS.UpstreamsOverseasPublic) == 0 {
		c.DNS.UpstreamsOverseasPublic = []string{
			"https://cloudflare-dns.com/dns-query",
			"https://dns.google/dns-query",
			"https://dns.quad9.net/dns-query",
			"1.1.1.1",
			"1.0.0.1",
			"8.8.8.8",
			"8.8.4.4",
			"9.9.9.9",
			"22.22.22.22",
		}
	}
	for i := range c.Exits {
		exit := &c.Exits[i]
		if exit.Type != "shadowsocks-rust" {
			continue
		}
		if exit.Method == "" {
			exit.Method = "2022-blake3-aes-128-gcm"
		}
		if exit.TimeoutSeconds == 0 {
			exit.TimeoutSeconds = 300
		}
	}
}

func defaultCNUpstreams() []string {
	return []string{
		"180.76.76.76",
		"101.226.4.6",
		"218.30.118.6",
		"114.114.114.114",
		"114.114.115.115",
		"117.50.10.10",
		"52.80.66.66",
	}
}

func usesLegacyCNUpstreams(values []string) bool {
	return len(values) == 3 &&
		values[0] == "180.76.76.76" &&
		values[1] == "101.226.4.6" &&
		values[2] == "218.30.118.6"
}

func (c Config) Validate() error {
	if net.ParseIP(c.Network.GatewayIP) == nil {
		return fmt.Errorf("network.gateway_ip is not a valid IP: %q", c.Network.GatewayIP)
	}
	if _, _, err := net.ParseCIDR(c.Network.InternalCIDR); err != nil {
		return fmt.Errorf("network.internal_cidr is invalid: %w", err)
	}
	if c.Network.IngressIface == "" {
		return errors.New("network.ingress_iface is required")
	}
	if err := validateNetwork(c.Network); err != nil {
		return err
	}
	if err := validatePanel(c.Panel); err != nil {
		return err
	}
	if len(c.Exits) == 0 {
		return errors.New("at least one [[exits]] entry is required")
	}
	if err := validateDNS(c.DNS); err != nil {
		return err
	}
	if err := validateLogging(c.Logging); err != nil {
		return err
	}
	if err := validateIOS(c.IOS); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, exit := range c.Exits {
		if err := validateExit(exit); err != nil {
			return err
		}
		if seen[exit.Name] {
			return fmt.Errorf("duplicate exit name: %s", exit.Name)
		}
		seen[exit.Name] = true
	}
	if err := validateRouting(c.Routing, c.Exits); err != nil {
		return err
	}
	return nil
}

func validatePanel(p PanelConfig) error {
	if err := validateHostPort("panel.listen", p.Listen, false); err != nil {
		return err
	}
	if len(p.AllowedCIDRs) == 0 {
		return errors.New("panel.allowed_cidrs is required")
	}
	for _, value := range p.AllowedCIDRs {
		if _, _, err := net.ParseCIDR(value); err != nil {
			return fmt.Errorf("panel.allowed_cidrs contains invalid CIDR %q: %w", value, err)
		}
	}
	return nil
}

func validateNetwork(n NetworkConfig) error {
	for field, port := range map[string]int{
		"network.http_redirect_port":  n.HTTPRedirectPort,
		"network.https_redirect_port": n.HTTPSRedirectPort,
		"network.quic_redirect_port":  n.QUICRedirectPort,
		"network.tcp_redirect_port":   n.TCPRedirectPort,
	} {
		if err := validatePort(field, port); err != nil {
			return err
		}
	}
	if n.HAProxyMaxConnections == nil {
		return errors.New("network.haproxy_max_connections is required after applying defaults")
	}
	if *n.HAProxyMaxConnections < 0 {
		return errors.New("network.haproxy_max_connections must be zero or positive")
	}
	switch n.QUICPolicy {
	case "reject", "proxy":
	default:
		return fmt.Errorf("network.quic_policy must be reject or proxy: %q", n.QUICPolicy)
	}
	switch n.EncryptedDNSPolicy {
	case "reject", "allow":
	default:
		return fmt.Errorf("network.encrypted_dns_policy must be reject or allow: %q", n.EncryptedDNSPolicy)
	}
	return nil
}

func validateLogging(l LoggingConfig) error {
	switch l.Level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("logging.level must be debug, info, warn, or error: %q", l.Level)
	}
}

func validateIOS(cfg IOSConfig) error {
	if !cfg.Enabled {
		return nil
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("ios.base_url must be an absolute HTTPS URL: %q", cfg.BaseURL)
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("ios.base_url must be an HTTPS origin without credentials, path, query, or fragment: %q", cfg.BaseURL)
	}
	if cfg.Organization == "" {
		return errors.New("ios.organization is required")
	}
	if matched, _ := regexp.MatchString(`^[A-Za-z0-9.-]+$`, cfg.ProfileIdentifier); !matched {
		return fmt.Errorf("ios.profile_identifier is invalid: %q", cfg.ProfileIdentifier)
	}
	return nil
}

func validateDNS(d DNSConfig) error {
	if d.Binary == "" {
		return errors.New("dns.binary is required")
	}
	for field, addr := range map[string]string{
		"dns.listen_udp": d.ListenUDP,
		"dns.listen_tcp": d.ListenTCP,
		"dns.listen_dot": d.ListenDOT,
	} {
		if err := validateHostPort(field, addr, false); err != nil {
			return err
		}
	}
	if d.ListenPublicDOT != "" {
		if err := validateHostPort("dns.listen_public_dot", d.ListenPublicDOT, false); err != nil {
			return err
		}
		if d.DOTDomain == "" {
			return errors.New("dns.dot_domain is required when dns.listen_public_dot is enabled")
		}
		if err := validateDomainName("dns.dot_domain", d.DOTDomain); err != nil {
			return err
		}
	}
	if len(d.BackendResolvers) == 0 {
		return errors.New("dns.backend_resolvers is required")
	}
	for _, resolver := range d.BackendResolvers {
		if err := validateHostPort("dns.backend_resolvers", resolver, false); err != nil {
			return err
		}
	}
	if d.CertDir == "" {
		return errors.New("dns.cert_dir is required")
	}
	if (d.CertFile == "") != (d.KeyFile == "") {
		return errors.New("dns.cert_file and dns.key_file must be configured together")
	}
	if d.ListenPublicDOT != "" && (d.CertFile == "" || d.KeyFile == "") {
		return errors.New("dns.cert_file and dns.key_file are required when dns.listen_public_dot is enabled")
	}
	if d.CacheSize < 0 {
		return errors.New("dns.cache_size must be >= 0")
	}
	if len(d.UpstreamsCN) == 0 || len(d.UpstreamsOverseasPrivate) == 0 || len(d.UpstreamsOverseasPublic) == 0 {
		return errors.New("dns.upstreams_cn, dns.upstreams_overseas_private, and dns.upstreams_overseas_public are required")
	}
	return nil
}

func validateDomainName(field, value string) error {
	if net.ParseIP(value) != nil {
		return fmt.Errorf("%s must be a domain name, not an IP address: %q", field, value)
	}
	name := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if name == "" || len(name) > 253 || !strings.Contains(name, ".") {
		return fmt.Errorf("%s is not a valid domain name: %q", field, value)
	}
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return fmt.Errorf("%s is not a valid domain name: %q", field, value)
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%s is not a valid domain name: %q", field, value)
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return fmt.Errorf("%s is not a valid domain name: %q", field, value)
		}
	}
	return nil
}

func validateRouting(r RoutingConfig, exits []ExitConfig) error {
	if r.FallbackExit == "" {
		return errors.New("routing.fallback_exit is required")
	}
	for _, exit := range exits {
		if exit.Name != r.FallbackExit {
			continue
		}
		if exit.Type == "shadowsocks-rust" && !exit.TCPEnabled() {
			return fmt.Errorf("routing.fallback_exit %q has tcp=false; HAProxy fallback requires a TCP-capable exit", exit.Name)
		}
		return nil
	}
	return fmt.Errorf("routing.fallback_exit references unknown exit %q", r.FallbackExit)
}

func validateExit(exit ExitConfig) error {
	if exit.Name == "" || exit.Type == "" {
		return errors.New("exit name and type are required")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(exit.Name) {
		return fmt.Errorf("exit %q: name may only contain letters, digits, dot, underscore, and dash", exit.Name)
	}
	switch exit.Type {
	case "direct":
		return nil
	case "shadowsocks-rust":
		return validateShadowsocksExit(exit)
	default:
		return fmt.Errorf("exit %q: unsupported type %q", exit.Name, exit.Type)
	}
}

func validatePort(field string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("%s is invalid: %d", field, port)
	}
	return nil
}

func validateShadowsocksExit(exit ExitConfig) error {
	if exit.Server == "" {
		return fmt.Errorf("exit %q: server is required", exit.Name)
	}
	if exit.ServerPort <= 0 || exit.ServerPort > 65535 {
		return fmt.Errorf("exit %q: server_port is invalid", exit.Name)
	}
	if exit.Password == "" {
		return fmt.Errorf("exit %q: password is required; generate one with: openssl rand -base64 16", exit.Name)
	}
	if err := validateSSKey(exit.Method, exit.Password); err != nil {
		return fmt.Errorf("exit %q: %w", exit.Name, err)
	}
	if err := validateHostPort("exit "+exit.Name+" listen", net.JoinHostPort(exit.ListenAddress, fmt.Sprint(exit.ListenPort)), true); err != nil {
		return err
	}
	if !exit.TCPEnabled() && !exit.UDPEnabled() {
		return fmt.Errorf("exit %q: tcp and udp cannot both be false", exit.Name)
	}
	if exit.TimeoutSeconds < 0 {
		return fmt.Errorf("exit %q: timeout_seconds must be >= 0", exit.Name)
	}
	return nil
}

func validateSSKey(method, password string) error {
	if !strings.HasPrefix(method, "2022-blake3-") {
		return nil
	}
	key, err := base64.StdEncoding.DecodeString(password)
	if err != nil {
		return fmt.Errorf("password must be a base64 key for %s; generate one with: openssl rand -base64 16", method)
	}
	want := 32
	if strings.Contains(method, "aes-128-gcm") {
		want = 16
	}
	if len(key) != want {
		return fmt.Errorf("password key length for %s is %d bytes, want %d; generate one with: openssl rand -base64 %d", method, len(key), want, want)
	}
	return nil
}

func (l LoggingConfig) AccessEnabled() bool {
	return l.Access == nil || *l.Access
}

func (e ExitConfig) TCPEnabled() bool {
	return e.TCP == nil || *e.TCP
}

func (e ExitConfig) UDPEnabled() bool {
	return e.UDP == nil || *e.UDP
}

func (e ExitConfig) SSRustMode() string {
	tcp := e.TCPEnabled()
	udp := e.UDPEnabled()
	switch {
	case tcp && udp:
		return "tcp_and_udp"
	case tcp:
		return "tcp_only"
	case udp:
		return "udp_only"
	default:
		return ""
	}
}

func validateHostPort(field, addr string, requireLoopback bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%s must be host:port: %w", field, err)
	}
	if port == "" {
		return fmt.Errorf("%s port is required", field)
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return fmt.Errorf("%s port is invalid: %q", field, port)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%s host must be an IP address: %q", field, host)
	}
	if requireLoopback && !ip.IsLoopback() {
		return fmt.Errorf("%s must listen on loopback, got %q", field, host)
	}
	return nil
}

func (c Config) ExitByName(name string) (ExitConfig, bool) {
	return findExit(c.Exits, name)
}

func findExit(exits []ExitConfig, name string) (ExitConfig, bool) {
	for _, exit := range exits {
		if exit.Name == name {
			return exit, true
		}
	}
	return ExitConfig{}, false
}
