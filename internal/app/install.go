package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/morain/5gws/internal/auth"
	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/installer"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/store"
)

func runInstall(args []string, input io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	opts := installOptions{}
	flags.StringVar(&opts.gatewayIP, "gateway-ip", "", "gateway IPv4 returned for routed domains")
	flags.StringVar(&opts.internalCIDR, "internal-cidr", "", "client source CIDR allowed through the gateway")
	flags.StringVar(&opts.ingressIface, "ingress-iface", "", "network interface receiving client traffic")
	flags.StringVar(&opts.dotDomain, "dot-domain", "", "domain pointing to this server for DNS over TLS")
	flags.StringVar(&opts.panelListen, "panel-listen", "", "local HTTP address for the web panel reverse proxy")
	flags.BoolVar(&opts.iosEnabled, "ios", false, "enable the optional iOS configuration profile")
	flags.BoolVar(&opts.nonInteractive, "non-interactive", false, "require all installation values as flags")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "validate and show actions only")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := opts.collect(input, out); err != nil {
		return err
	}
	cfg := installConfig(opts.gatewayIP, opts.internalCIDR, opts.ingressIface, opts.dotDomain, opts.panelListen, opts.iosEnabled)
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !opts.dryRun {
		if os.Geteuid() != 0 {
			return errors.New("安装必须以 root 运行")
		}
		if err := refuseLegacyInstall(cfg.System.StateDir); err != nil {
			return err
		}
	}
	ruleFile := defaultRuleFile()
	norm, err := (rules.Resolver{}).Normalize(context.Background(), ruleFile)
	if err != nil {
		return err
	}
	printInstallSummary(out, cfg, len(norm.Rules))
	if err := installer.EnsureRuntime(cfg, opts.dryRun, out); err != nil {
		return err
	}
	if err := ensureSmartDNSLogDir(opts.dryRun, out); err != nil {
		return err
	}
	if opts.dryRun {
		fmt.Fprintln(out, "试运行：将申请 TLS 证书、初始化服务并启动 5gws.service")
		return nil
	}
	if err := ensureCertificate(cfg, out); err != nil {
		return err
	}
	return initializeService(cfg, ruleFile, norm, out)
}

type installOptions struct {
	gatewayIP      string
	internalCIDR   string
	ingressIface   string
	dotDomain      string
	panelListen    string
	iosEnabled     bool
	nonInteractive bool
	dryRun         bool
}

func (o *installOptions) collect(input io.Reader, out io.Writer) error {
	if o.nonInteractive {
		missing := missingInstallFlags(*o)
		if len(missing) > 0 {
			return fmt.Errorf("非交互安装缺少参数：%s", strings.Join(missing, ", "))
		}
		return nil
	}
	reader := bufio.NewReader(input)
	var err error
	if o.gatewayIP, err = prompt(reader, out, "网关 IPv4", "命中分流规则时返回给客户端的服务器地址", valueOr(o.gatewayIP, "10.0.0.1"), false); err != nil {
		return err
	}
	if o.internalCIDR, err = prompt(reader, out, "客户端网段", "允许使用此网关的客户端来源 CIDR", valueOr(o.internalCIDR, "172.22.0.0/16"), false); err != nil {
		return err
	}
	if o.ingressIface, err = prompt(reader, out, "入口网卡", "接收客户端流量的服务器网络接口", valueOr(o.ingressIface, defaultInterface()), false); err != nil {
		return err
	}
	o.dotDomain, err = prompt(reader, out, "DoT 域名", "已解析到本机，用于 DNS over TLS 和面板证书", o.dotDomain, true)
	return err
}

func missingInstallFlags(opts installOptions) []string {
	values := []struct{ flag, value string }{
		{"--gateway-ip", opts.gatewayIP}, {"--internal-cidr", opts.internalCIDR},
		{"--ingress-iface", opts.ingressIface}, {"--dot-domain", opts.dotDomain},
	}
	var missing []string
	for _, item := range values {
		if strings.TrimSpace(item.value) == "" {
			missing = append(missing, item.flag)
		}
	}
	return missing
}

func installConfig(gateway, cidr, iface, domain, panelListen string, iosEnabled bool) config.Config {
	access := true
	cfg := config.Config{
		Panel:   config.PanelConfig{Listen: valueOr(panelListen, "127.0.0.1:19443")},
		Network: config.NetworkConfig{GatewayIP: gateway, InternalCIDR: cidr, IngressIface: iface},
		DNS:     config.DNSConfig{DOTDomain: strings.ToLower(strings.TrimSuffix(domain, "."))},
		Logging: config.LoggingConfig{Level: "info", Access: &access},
		IOS:     config.IOSConfig{Enabled: iosEnabled, Listen: "0.0.0.0:8088", BaseURL: "http://" + gateway + ":8088", Organization: "5gws", ProfileIdentifier: "dev.5gws.dot"},
		Exits:   []config.ExitConfig{{Name: "direct", Type: "direct"}},
	}
	cfg.ApplyDefaults()
	return cfg
}

func printInstallSummary(out io.Writer, cfg config.Config, ruleCount int) {
	fmt.Fprintln(out, "\n安装配置")
	fmt.Fprintf(out, "  网关 IPv4:  %s\n", cfg.Network.GatewayIP)
	fmt.Fprintf(out, "  客户端网段: %s\n", cfg.Network.InternalCIDR)
	fmt.Fprintf(out, "  入口网卡:   %s\n", cfg.Network.IngressIface)
	fmt.Fprintf(out, "  DoT 域名:   %s\n", cfg.DNS.DOTDomain)
	fmt.Fprintf(out, "  Web 面板:   http://%s（仅供 Nginx 反代）\n", cfg.Panel.Listen)
	fmt.Fprintf(out, "  iOS Profile: %s\n", enabledText(cfg.IOS.Enabled))
	fmt.Fprintf(out, "  初始规则:   %d\n\n", ruleCount)
}

func enabledText(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "关闭"
}

func defaultRuleFile() rules.File {
	return rules.File{
		Rules: []rules.Rule{{Name: "ip-check", Exit: "direct", DomainSuffix: []string{"icanhazip.com", "ipinfo.io", "ippure.com"}}},
		Imports: []rules.Import{
			{Name: "speedtest", Type: "sing-box", URL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/category-speedtest.json", Exit: "direct"},
			{Name: "cn", Type: "sing-box", URL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.json", DNSPool: "cn"},
			{Name: "gfw", Type: "sing-box", URL: "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.json", Exit: "direct"},
		},
	}
}

func refuseLegacyInstall(stateDir string) error {
	if _, err := os.Stat(filepath.Join(stateDir, "5gws.db")); err == nil {
		return errors.New("existing 5gws database found; fresh installation only")
	}
	units, _ := filepath.Glob("/etc/systemd/system/5gws-*.service")
	if len(units) > 0 {
		return fmt.Errorf("legacy units found: %s", strings.Join(units, ", "))
	}
	return nil
}

func initializeService(cfg config.Config, file rules.File, norm rules.Normalized, out io.Writer) error {
	if err := os.MkdirAll(cfg.System.StateDir, 0o700); err != nil {
		return err
	}
	state, err := store.Open(filepath.Join(cfg.System.StateDir, "5gws.db"))
	if err != nil {
		return err
	}
	defer state.Close()
	if _, err := state.Initialize(context.Background(), store.Bundle{Config: cfg, Rules: file, ResolvedRules: norm.Rules}); err != nil {
		return err
	}
	password, err := auth.GeneratePassword()
	if err != nil {
		return err
	}
	admin, err := auth.New(state.DB(), 24*time.Hour).ResetAdmin(context.Background(), password)
	if err != nil {
		return err
	}
	if err := os.WriteFile("/etc/systemd/system/5gws.service", []byte(systemdUnit), 0o644); err != nil {
		return err
	}
	if err := command(out, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := command(out, "systemctl", "enable", "--now", "5gws.service"); err != nil {
		return err
	}
	fmt.Fprintln(out, "\n安装完成")
	fmt.Fprintln(out, "  服务状态: sudo 5gws status")
	printAdminCredentials(out, admin.Username, password)
	fmt.Fprintf(out, "  Nginx upstream: http://%s\n", cfg.Panel.Listen)
	return nil
}

func printAdminCredentials(out io.Writer, username, password string) {
	fmt.Fprintln(out, "  管理员账号（仅显示一次）")
	fmt.Fprintf(out, "    Username: %s\n", username)
	fmt.Fprintf(out, "    Password: %s\n", password)
}

const systemdUnit = `[Unit]
Description=5gws gateway daemon
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/5gws daemon --database /var/lib/5gws/5gws.db
Restart=on-failure
RestartSec=2s
KillMode=control-group
RuntimeDirectory=5gws
RuntimeDirectoryMode=0700
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
`

func ensureCertificate(cfg config.Config, out io.Writer) error {
	if _, err := os.Stat(cfg.DNS.CertFile); err == nil {
		if _, err := os.Stat(cfg.DNS.KeyFile); err == nil {
			return allowSmartDNSCertificateRead(cfg)
		}
	}
	if _, err := exec.LookPath("certbot"); err != nil {
		if err := command(out, "apt-get", "update"); err != nil {
			return err
		}
		if err := command(out, "apt-get", "install", "-y", "certbot"); err != nil {
			return err
		}
	}
	if err := command(out, "certbot", "certonly", "--standalone", "-d", cfg.DNS.DOTDomain, "--non-interactive", "--agree-tos", "--register-unsafely-without-email", "--keep-until-expiring"); err != nil {
		return err
	}
	live := filepath.Join("/etc/letsencrypt/live", cfg.DNS.DOTDomain)
	if err := os.MkdirAll(filepath.Dir(cfg.DNS.CertFile), 0o750); err != nil {
		return err
	}
	if err := copy(filepath.Join(live, "fullchain.pem"), cfg.DNS.CertFile, 0o644); err != nil {
		return err
	}
	if err := copy(filepath.Join(live, "privkey.pem"), cfg.DNS.KeyFile, 0o600); err != nil {
		return err
	}
	return allowSmartDNSCertificateRead(cfg)
}

func allowSmartDNSCertificateRead(cfg config.Config) error {
	group, err := smartDNSGroup()
	if err != nil {
		return err
	}
	for _, dir := range []string{cfg.System.ConfigDir, filepath.Dir(cfg.DNS.CertFile)} {
		if err := os.Chown(dir, 0, group); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o750); err != nil {
			return err
		}
	}
	for _, file := range []string{cfg.DNS.CertFile, cfg.DNS.KeyFile} {
		if err := os.Chown(file, 0, group); err != nil {
			return err
		}
		if err := os.Chmod(file, 0o640); err != nil {
			return err
		}
	}
	return nil
}

func ensureSmartDNSLogDir(dryRun bool, out io.Writer) error {
	if dryRun {
		fmt.Fprintln(out, "dry-run: would prepare /var/log/smartdns for smartdns logs")
		return nil
	}
	uid, err := smartDNSUser()
	if err != nil {
		return err
	}
	gid, err := smartDNSGroup()
	if err != nil {
		return err
	}
	if err := os.MkdirAll("/var/log/smartdns", 0o750); err != nil {
		return err
	}
	if err := os.Chown("/var/log/smartdns", uid, gid); err != nil {
		return err
	}
	return os.Chmod("/var/log/smartdns", 0o750)
}

func smartDNSUser() (int, error) {
	account, err := user.Lookup("nobody")
	if err != nil {
		return 0, err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return 0, err
	}
	return uid, nil
}

func smartDNSGroup() (int, error) {
	for _, name := range []string{"nogroup", "nobody"} {
		group, err := user.LookupGroup(name)
		if err != nil {
			continue
		}
		gid, err := strconv.Atoi(group.Gid)
		if err != nil {
			return 0, err
		}
		return gid, nil
	}
	return 0, errors.New("smartdns certificate group not found: expected nogroup or nobody")
}

func runUninstall(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := flags.Bool("purge", false, "remove database, rendered state, and certificates")
	yes := flags.Bool("yes", false, "confirm uninstall")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !*yes {
		return errors.New("uninstall requires --yes")
	}
	if os.Geteuid() != 0 {
		return errors.New("uninstall must run as root")
	}
	var result error
	if err := command(out, "systemctl", "disable", "--now", "5gws.service"); err != nil {
		result = errors.Join(result, err)
	}
	if err := os.Remove("/etc/systemd/system/5gws.service"); err != nil && !errors.Is(err, os.ErrNotExist) {
		result = errors.Join(result, err)
	}
	if err := command(out, "nft", "destroy", "table", "inet", "fivegws"); err != nil {
		result = errors.Join(result, err)
	}
	if *purge {
		if err := os.RemoveAll("/var/lib/5gws"); err != nil {
			return err
		}
		if err := os.RemoveAll("/etc/5gws"); err != nil {
			return err
		}
	}
	if err := command(out, "systemctl", "daemon-reload"); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

func prompt(reader *bufio.Reader, out io.Writer, label, help, fallback string, required bool) (string, error) {
	for {
		fmt.Fprintf(out, "\n%s\n  %s\n", label, help)
		if fallback == "" {
			fmt.Fprint(out, "> ")
		} else {
			fmt.Fprintf(out, "[%s] > ", fallback)
		}
		value, err := reader.ReadString('\n')
		value = strings.TrimSpace(value)
		if value != "" {
			return value, nil
		}
		if fallback != "" {
			return fallback, nil
		}
		if err != nil {
			return "", fmt.Errorf("%s为必填项且输入已结束", label)
		}
		if required {
			fmt.Fprintf(out, "%s不能为空，请重新输入。\n", label)
		}
	}
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func defaultInterface() string {
	data, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return "eth0"
	}
	fields := strings.Fields(string(data))
	for i := range fields {
		if fields[i] == "dev" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return "eth0"
}

func command(out io.Writer, name string, args ...string) error {
	fmt.Fprintf(out, "+ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}

func copy(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}
