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
	"path/filepath"
	"strings"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/installer"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/store"
)

func runInstall(args []string, input io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	gateway := flags.String("gateway-ip", "10.0.0.1", "gateway IP returned by DNS rules")
	cidr := flags.String("internal-cidr", "172.22.0.0/16", "carrier internal CIDR")
	iface := flags.String("ingress-iface", defaultInterface(), "ingress interface")
	domain := flags.String("dot-domain", "", "DoT and panel domain")
	panel := flags.String("panel-listen", "0.0.0.0:8443", "panel HTTPS listen address")
	adminCIDR := flags.String("admin-cidr", "", "additional panel management CIDR")
	iosEnabled := flags.Bool("ios", true, "enable iOS profile")
	yes := flags.Bool("assume-yes", false, "use flag/default values without prompting")
	dryRun := flags.Bool("dry-run", false, "validate and show actions only")
	if err := flags.Parse(args); err != nil {
		return err
	}
	reader := bufio.NewReader(input)
	if !*yes {
		*gateway = prompt(reader, out, "gateway IP", *gateway)
		*cidr = prompt(reader, out, "carrier internal CIDR", *cidr)
		*iface = prompt(reader, out, "ingress interface", *iface)
		*domain = prompt(reader, out, "DoT domain", *domain)
		*adminCIDR = prompt(reader, out, "additional management CIDR", *adminCIDR)
	}
	if strings.TrimSpace(*domain) == "" {
		return errors.New("--dot-domain is required")
	}
	cfg := installConfig(*gateway, *cidr, *iface, *domain, *panel, *adminCIDR, *iosEnabled)
	if err := cfg.Validate(); err != nil {
		return err
	}
	ruleFile := defaultRuleFile()
	norm, err := (rules.Resolver{}).Normalize(context.Background(), ruleFile)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "gateway=%s internal=%s iface=%s panel=%s rules=%d\n", *gateway, *cidr, *iface, *panel, len(norm.Rules))
	if err := installer.EnsureRuntime(cfg, *dryRun, out); err != nil {
		return err
	}
	if *dryRun {
		fmt.Fprintln(out, "dry-run: would initialize SQLite, obtain the TLS certificate, and install 5gws.service")
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("install must run as root")
	}
	if err := refuseLegacyInstall(cfg.System.StateDir); err != nil {
		return err
	}
	if err := ensureCertificate(cfg, out); err != nil {
		return err
	}
	return initializeService(cfg, ruleFile, norm, out)
}

func installConfig(gateway, cidr, iface, domain, panel, adminCIDR string, iosEnabled bool) config.Config {
	access := true
	cfg := config.Config{
		Panel:   config.PanelConfig{Listen: panel},
		Network: config.NetworkConfig{GatewayIP: gateway, InternalCIDR: cidr, IngressIface: iface},
		DNS:     config.DNSConfig{DOTDomain: strings.ToLower(strings.TrimSuffix(domain, "."))},
		Logging: config.LoggingConfig{Level: "info", Access: &access},
		IOS:     config.IOSConfig{Enabled: iosEnabled, Listen: "0.0.0.0:8088", BaseURL: "http://" + gateway + ":8088", Organization: "5gws", ProfileIdentifier: "dev.5gws.dot"},
		Exits:   []config.ExitConfig{{Name: "direct", Type: "direct"}},
	}
	cfg.ApplyDefaults()
	if adminCIDR != "" {
		cfg.Panel.AllowedCIDRs = append(cfg.Panel.AllowedCIDRs, adminCIDR)
	}
	return cfg
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
	if err := os.WriteFile("/etc/systemd/system/5gws.service", []byte(systemdUnit), 0o644); err != nil {
		return err
	}
	if err := command(out, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := command(out, "systemctl", "enable", "--now", "5gws.service"); err != nil {
		return err
	}
	fmt.Fprintln(out, "setup token: journalctl -u 5gws.service -n 30 --no-pager")
	return nil
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
			return nil
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
	return copy(filepath.Join(live, "privkey.pem"), cfg.DNS.KeyFile, 0o600)
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

func prompt(reader *bufio.Reader, out io.Writer, label, fallback string) string {
	fmt.Fprintf(out, "%s [%s]: ", label, fallback)
	value, _ := reader.ReadString('\n')
	if value = strings.TrimSpace(value); value != "" {
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
