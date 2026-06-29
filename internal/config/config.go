package config

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	System     SystemConfig     `toml:"system"`
	Network    NetworkConfig    `toml:"network"`
	Routing    RoutingConfig    `toml:"routing"`
	DNS        DNSConfig        `toml:"dns"`
	Logging    LoggingConfig    `toml:"logging"`
	IOS        IOSConfig        `toml:"ios"`
	Telegram   TelegramConfig   `toml:"telegram"`
	TCPProxies []TCPProxyConfig `toml:"tcp_proxies"`
	UDPProxies []UDPProxyConfig `toml:"udp_proxies"`
	Exits      []ExitConfig     `toml:"exits"`
}

type SystemConfig struct {
	ConfigDir string `toml:"config_dir"`
	StateDir  string `toml:"state_dir"`
	RunDir    string `toml:"run_dir"`
	User      string `toml:"user"`
}

type NetworkConfig struct {
	GatewayIP         string `toml:"gateway_ip"`
	InternalCIDR      string `toml:"internal_cidr"`
	IngressIface      string `toml:"ingress_iface"`
	HTTPRedirectPort  int    `toml:"http_redirect_port"`
	HTTPSRedirectPort int    `toml:"https_redirect_port"`
	QUICRedirectPort  int    `toml:"quic_redirect_port"`
}

type RoutingConfig struct {
	FallbackExit string `toml:"fallback_exit"`
}

type DNSConfig struct {
	Binary                   string   `toml:"binary"`
	DOTDomain                string   `toml:"dot_domain"`
	ListenUDP                string   `toml:"listen_udp"`
	ListenTCP                string   `toml:"listen_tcp"`
	ListenDOT                string   `toml:"listen_dot"`
	ListenPublicDOT          string   `toml:"listen_public_dot"`
	BackendResolvers         []string `toml:"backend_resolvers"`
	CertDir                  string   `toml:"cert_dir"`
	CertFile                 string   `toml:"cert_file"`
	KeyFile                  string   `toml:"key_file"`
	CacheSize                int      `toml:"cache_size"`
	UpstreamsCN              []string `toml:"upstreams_cn"`
	UpstreamsOverseasPrivate []string `toml:"upstreams_overseas_private"`
	UpstreamsOverseasPublic  []string `toml:"upstreams_overseas_public"`
}

type LoggingConfig struct {
	Level  string `toml:"level"`
	Access *bool  `toml:"access"`
}

type IOSConfig struct {
	Enabled           bool   `toml:"enabled"`
	Listen            string `toml:"listen"`
	BaseURL           string `toml:"base_url"`
	Organization      string `toml:"organization"`
	ProfileIdentifier string `toml:"profile_identifier"`
}

type TelegramConfig struct {
	Enabled      bool     `toml:"enabled"`
	BotEnv       string   `toml:"bot_env"`
	AllowedUsers []string `toml:"allowed_users"`
}

type UDPProxyConfig struct {
	Name       string `toml:"name"`
	ClientPort int    `toml:"client_port"`
	ListenPort int    `toml:"listen_port"`
	Target     string `toml:"target"`
	Exit       string `toml:"exit"`
}

type TCPProxyConfig struct {
	Name       string `toml:"name"`
	ClientPort int    `toml:"client_port"`
	ListenPort int    `toml:"listen_port"`
	Exit       string `toml:"exit"`
}

type ExitConfig struct {
	Name           string `toml:"name"`
	Type           string `toml:"type"`
	FWMark         int    `toml:"fwmark"`
	Server         string `toml:"server"`
	ServerPort     int    `toml:"server_port"`
	Method         string `toml:"method"`
	Password       string `toml:"password"`
	Username       string `toml:"username"`
	ListenAddress  string `toml:"listen_address"`
	ListenPort     int    `toml:"listen_port"`
	TCP            *bool  `toml:"tcp"`
	UDP            *bool  `toml:"udp"`
	TimeoutSeconds int    `toml:"timeout_seconds"`
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
	if c.Network.HTTPRedirectPort == 0 {
		c.Network.HTTPRedirectPort = 18080
	}
	if c.Network.HTTPSRedirectPort == 0 {
		c.Network.HTTPSRedirectPort = 18443
	}
	if c.Network.QUICRedirectPort == 0 {
		c.Network.QUICRedirectPort = 18443
	}
	if c.Routing.FallbackExit == "" {
		c.Routing.FallbackExit = "direct"
	}
	if len(c.TCPProxies) == 0 {
		c.TCPProxies = DefaultTCPProxies()
	}
	for i := range c.TCPProxies {
		if c.TCPProxies[i].Exit == "" {
			c.TCPProxies[i].Exit = "direct"
		}
	}
	if len(c.UDPProxies) == 0 {
		c.UDPProxies = DefaultUDPProxies()
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
	if c.DNS.CacheSize == 0 {
		c.DNS.CacheSize = 8192
	}
	if len(c.DNS.UpstreamsCN) == 0 {
		c.DNS.UpstreamsCN = []string{
			"https://223.5.5.5/dns-query",
			"https://doh.pub/dns-query",
			"223.5.5.5",
			"223.6.6.6",
			"119.29.29.29",
			"180.76.76.76",
			"101.226.4.6",
			"218.30.118.6",
		}
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
	if len(c.Exits) == 0 {
		return errors.New("at least one [[exits]] entry is required")
	}
	if err := validateDNS(c.DNS); err != nil {
		return err
	}
	if err := validateLogging(c.Logging); err != nil {
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
	if err := validateTCPProxies(c.TCPProxies, c); err != nil {
		return err
	}
	if err := validateUDPProxies(c.UDPProxies, c.Exits); err != nil {
		return err
	}
	return nil
}

func DefaultTCPProxies() []TCPProxyConfig {
	return []TCPProxyConfig{
		{
			Name:       "speedtest-8080",
			ClientPort: 8080,
			ListenPort: 18081,
			Exit:       "direct",
		},
		{
			Name:       "speedtest-5060",
			ClientPort: 5060,
			ListenPort: 15060,
			Exit:       "direct",
		},
	}
}

func DefaultUDPProxies() []UDPProxyConfig {
	return []UDPProxyConfig{
		{
			Name:       "stun-3478",
			ClientPort: 3478,
			ListenPort: 13478,
			Target:     "stun.cloudflare.com:3478",
			Exit:       "direct",
		},
		{
			Name:       "stun-19302",
			ClientPort: 19302,
			ListenPort: 13902,
			Target:     "stun.l.google.com:19302",
			Exit:       "direct",
		},
	}
}

func validateLogging(l LoggingConfig) error {
	switch l.Level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("logging.level must be debug, info, warn, or error: %q", l.Level)
	}
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

func validateTCPProxies(proxies []TCPProxyConfig, cfg Config) error {
	names := map[string]bool{}
	clientPorts := map[int]string{}
	listenPorts := reservedTCPPorts(cfg)
	for _, proxy := range proxies {
		if proxy.Name == "" {
			return errors.New("tcp_proxy name is required")
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(proxy.Name) {
			return fmt.Errorf("tcp_proxy %q: name may only contain letters, digits, dot, underscore, and dash", proxy.Name)
		}
		if names[proxy.Name] {
			return fmt.Errorf("duplicate tcp_proxy name: %s", proxy.Name)
		}
		names[proxy.Name] = true
		if err := validatePort("tcp_proxy "+proxy.Name+" client_port", proxy.ClientPort); err != nil {
			return err
		}
		if err := validatePort("tcp_proxy "+proxy.Name+" listen_port", proxy.ListenPort); err != nil {
			return err
		}
		if previous := clientPorts[proxy.ClientPort]; previous != "" {
			return fmt.Errorf("tcp_proxy %q: client_port %d already used by %q", proxy.Name, proxy.ClientPort, previous)
		}
		clientPorts[proxy.ClientPort] = proxy.Name
		if previous := listenPorts[proxy.ListenPort]; previous != "" {
			return fmt.Errorf("tcp_proxy %q: listen_port %d conflicts with %s", proxy.Name, proxy.ListenPort, previous)
		}
		listenPorts[proxy.ListenPort] = "tcp_proxy " + proxy.Name
		exit, ok := findExit(cfg.Exits, proxy.Exit)
		if !ok {
			return fmt.Errorf("tcp_proxy %q references unknown exit %q", proxy.Name, proxy.Exit)
		}
		if exit.Type == "shadowsocks-rust" && !exit.TCPEnabled() {
			return fmt.Errorf("tcp_proxy %q references exit %q with tcp=false", proxy.Name, proxy.Exit)
		}
	}
	return nil
}

func reservedTCPPorts(cfg Config) map[int]string {
	ports := map[int]string{
		80:                            "built-in tcp/80 redirect",
		443:                           "built-in tcp/443 redirect",
		cfg.Network.HTTPRedirectPort:  "network.http_redirect_port",
		cfg.Network.HTTPSRedirectPort: "network.https_redirect_port",
	}
	for field, addr := range map[string]string{
		"dns.listen_tcp":        cfg.DNS.ListenTCP,
		"dns.listen_dot":        cfg.DNS.ListenDOT,
		"dns.listen_public_dot": cfg.DNS.ListenPublicDOT,
	} {
		if port := hostPortNumber(addr); port != 0 {
			ports[port] = field
		}
	}
	return ports
}

func hostPortNumber(addr string) int {
	if addr == "" {
		return 0
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	value, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return value
}

func validateUDPProxies(proxies []UDPProxyConfig, exits []ExitConfig) error {
	names := map[string]bool{}
	clientPorts := map[int]string{}
	listenPorts := map[int]string{}
	for _, proxy := range proxies {
		if proxy.Name == "" {
			return errors.New("udp_proxy name is required")
		}
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(proxy.Name) {
			return fmt.Errorf("udp_proxy %q: name may only contain letters, digits, dot, underscore, and dash", proxy.Name)
		}
		if names[proxy.Name] {
			return fmt.Errorf("duplicate udp_proxy name: %s", proxy.Name)
		}
		names[proxy.Name] = true
		if err := validatePort("udp_proxy "+proxy.Name+" client_port", proxy.ClientPort); err != nil {
			return err
		}
		if err := validatePort("udp_proxy "+proxy.Name+" listen_port", proxy.ListenPort); err != nil {
			return err
		}
		if previous := clientPorts[proxy.ClientPort]; previous != "" {
			return fmt.Errorf("udp_proxy %q: client_port %d already used by %q", proxy.Name, proxy.ClientPort, previous)
		}
		clientPorts[proxy.ClientPort] = proxy.Name
		if previous := listenPorts[proxy.ListenPort]; previous != "" {
			return fmt.Errorf("udp_proxy %q: listen_port %d already used by %q", proxy.Name, proxy.ListenPort, previous)
		}
		listenPorts[proxy.ListenPort] = proxy.Name
		host, _, err := proxy.TargetHostPort()
		if err != nil {
			return fmt.Errorf("udp_proxy %q: %w", proxy.Name, err)
		}
		if net.ParseIP(host) == nil {
			if err := validateDomainName("udp_proxy "+proxy.Name+" target host", host); err != nil {
				return err
			}
		}
		exit, ok := findExit(exits, proxy.Exit)
		if !ok {
			return fmt.Errorf("udp_proxy %q references unknown exit %q", proxy.Name, proxy.Exit)
		}
		if exit.Type == "shadowsocks-rust" && !exit.UDPEnabled() {
			return fmt.Errorf("udp_proxy %q references exit %q with udp=false", proxy.Name, proxy.Exit)
		}
	}
	return nil
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

func (p UDPProxyConfig) TargetHostPort() (string, int, error) {
	host, port, err := net.SplitHostPort(p.Target)
	if err != nil {
		return "", 0, fmt.Errorf("target must be host:port: %w", err)
	}
	if host == "" {
		return "", 0, errors.New("target host is required")
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return "", 0, fmt.Errorf("target port is invalid: %q", port)
	}
	return host, portNum, nil
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
