package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/installer"
	"github.com/morain/5gws/internal/ios"
	"github.com/morain/5gws/internal/quic"
	"github.com/morain/5gws/internal/render"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/telegram"
)

const (
	defaultConfigPath = "/etc/5gws/config.toml"
	defaultRulesPath  = "/etc/5gws/rules.toml"
)

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stdout)
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(stdout)
		return nil
	case "render":
		return cmdRender(args[1:], stdout)
	case "apply":
		return cmdApply(args[1:], stdout)
	case "install":
		return cmdInstall(args[1:], stdin, stdout)
	case "install-smartdns":
		return cmdInstallSmartDNS(args[1:], stdout)
	case "install-ssrust":
		return cmdInstallSSRust(args[1:], stdout)
	case "uninstall":
		return cmdUninstall(args[1:], stdout)
	case "doctor":
		return cmdDoctor(args[1:], stdout)
	case "status":
		return cmdStatus(stdout)
	case "ios-link":
		return cmdIOSLink(args[1:], stdout)
	case "cert-server":
		return cmdCertServer(args[1:])
	case "quicgw":
		return cmdQUICGW(args[1:])
	case "bot":
		return cmdBot(args[1:])
	default:
		return fmt.Errorf("unknown command %q\nRun '5gws --help' for usage.", args[0])
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: 5gws <command> [flags]

Main:
  install           guided install and enable services
  apply             render config and restart services
  doctor            validate config and runtime deps
  status            show service status
  uninstall         remove 5gws services and state

Client:
  ios-link          generate iOS cert/profile links and QR codes

Tools:
  render            render files for inspection
  install-smartdns  install smartdns-rs explicitly
  install-ssrust    install shadowsocks-rust explicitly

Runtime/debug:
  cert-server       serve iOS files; usually managed by apply
  quicgw            UDP/443 QUIC gateway; usually managed by apply
  bot               Telegram bot; usually managed by apply
`)
}

func cmdRender(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("render", flag.ContinueOnError)
	cfgPath, rulesPath, outDir := commonPaths(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	files, err := render.Generate(cfg, norm)
	if err != nil {
		return err
	}
	if err := render.WriteAll(*outDir, files); err != nil {
		return err
	}
	fmt.Fprintf(out, "rendered %d files into %s\n", len(files), *outDir)
	return nil
}

func cmdApply(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	cfgPath, rulesPath, _ := commonPaths(fs)
	dryRun := fs.Bool("dry-run", false, "render only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	outDir := filepath.Join(cfg.System.StateDir, "rendered")
	files, err := render.Generate(cfg, norm)
	if err != nil {
		return err
	}
	if *dryRun {
		fmt.Fprintf(out, "dry-run: would render %d files into %s\n", len(files), outDir)
		return nil
	}
	if err := requireRoot(); err != nil {
		return err
	}
	if err := render.WriteAll(outDir, files); err != nil {
		return err
	}
	return runApplyCommands(cfg, out)
}

func cmdInstall(args []string, stdin io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	cfgPath, rulesPath, _ := commonPaths(fs)
	assumeYes := fs.Bool("assume-yes", false, "skip confirmation")
	dryRun := fs.Bool("dry-run", false, "show actions without writing system state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, norm, generated, err := loadOrWizard(*cfgPath, *rulesPath, stdin, out, *assumeYes)
	if err != nil {
		return err
	}
	printInstallSummary(out, cfg, norm)
	if !*assumeYes && !confirm(stdin, out, "Continue install?") {
		return errors.New("install cancelled")
	}
	if *dryRun {
		if err := installer.EnsureRuntime(cfg, true, out); err != nil {
			return err
		}
		fmt.Fprintln(out, "dry-run: no files or services changed")
		return nil
	}
	if err := requireRoot(); err != nil {
		return err
	}
	if err := installer.EnsureRuntime(cfg, false, out); err != nil {
		return err
	}
	if generated.config != "" || generated.rules != "" {
		if err := writeGeneratedInputs(*cfgPath, *rulesPath, generated); err != nil {
			return err
		}
	}
	files, err := render.Generate(cfg, norm)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.System.ConfigDir, 0o700); err != nil {
		return err
	}
	if err := render.WriteAll(filepath.Join(cfg.System.StateDir, "rendered"), files); err != nil {
		return err
	}
	return runApplyCommands(cfg, out)
}

func cmdInstallSmartDNS(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("install-smartdns", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show actions without installing")
	yes := fs.Bool("yes", false, "install without confirmation")
	version := fs.String("version", installer.DefaultSmartDNSVersion, "smartdns-rs version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return installer.InstallSmartDNS(installer.Options{DryRun: *dryRun, Yes: *yes, Version: *version}, out)
}

func cmdInstallSSRust(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("install-ssrust", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show actions without installing")
	yes := fs.Bool("yes", false, "install without confirmation")
	version := fs.String("version", installer.DefaultSSRustVersion, "shadowsocks-rust version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return installer.InstallSSRust(installer.Options{DryRun: *dryRun, Yes: *yes, Version: *version}, out)
}

func cmdUninstall(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "remove config and state")
	yes := fs.Bool("yes", false, "confirm destructive uninstall")
	dryRun := fs.Bool("dry-run", false, "show actions without changing system state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*yes && !*dryRun {
		return errors.New("uninstall requires --yes or --dry-run")
	}
	steps := uninstallSteps(*purge)
	for _, step := range steps {
		fmt.Fprintln(out, step)
	}
	if *dryRun {
		return nil
	}
	if err := requireRoot(); err != nil {
		return err
	}
	return runUninstall(*purge, out)
}

func cmdDoctor(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	cfgPath, rulesPath, _ := commonPaths(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	if _, err := render.Generate(cfg, norm); err != nil {
		return err
	}
	fmt.Fprintf(out, "config: ok\nrules: %d normalized rules\n", len(norm.Rules))
	for _, bin := range []string{"haproxy", "nft", "systemctl"} {
		if path, err := exec.LookPath(bin); err == nil {
			fmt.Fprintf(out, "%s: %s\n", bin, path)
		} else {
			fmt.Fprintf(out, "%s: missing\n", bin)
		}
	}
	if path, err := exec.LookPath(cfg.DNS.Binary); err == nil {
		fmt.Fprintf(out, "smartdns-rs: %s\n", path)
	} else {
		fmt.Fprintf(out, "smartdns-rs: missing (%s)\n", cfg.DNS.Binary)
	}
	for _, exit := range cfg.Exits {
		if exit.Type != "shadowsocks-rust" {
			continue
		}
		if path, err := exec.LookPath("sslocal"); err == nil {
			fmt.Fprintf(out, "shadowsocks-rust %s: %s\n", exit.Name, path)
		} else {
			fmt.Fprintf(out, "shadowsocks-rust %s: missing (sslocal)\n", exit.Name)
		}
	}
	return nil
}

func cmdStatus(out io.Writer) error {
	for _, svc := range allServices() {
		cmd := exec.Command("systemctl", "is-active", svc)
		data, err := cmd.CombinedOutput()
		status := strings.TrimSpace(string(data))
		if err != nil && status == "" {
			status = err.Error()
		}
		fmt.Fprintf(out, "%s: %s\n", svc, status)
	}
	return nil
}

func cmdIOSLink(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ios-link", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "config.toml path")
	outDir := fs.String("out", "", "output directory")
	noQR := fs.Bool("no-qr", false, "print links only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	dir := *outDir
	if dir == "" {
		dir = filepath.Join(cfg.System.StateDir, "ios")
	}
	links, err := ios.Generate(dir, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "cert: %s\nprofile: %s\ncert_qr: %s\nprofile_qr: %s\n", links.CertURL, links.ProfileURL, links.CertQR, links.ProfileQR)
	if *noQR {
		return nil
	}
	if err := printTerminalQR(out, "CA certificate QR", links.CertURL); err != nil {
		return err
	}
	return printTerminalQR(out, "iOS profile QR", links.ProfileURL)
}

func printTerminalQR(out io.Writer, label, value string) error {
	qr, err := ios.TerminalQRCode(value)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%s:\n%s", label, qr)
	return nil
}

func cmdCertServer(args []string) error {
	fs := flag.NewFlagSet("cert-server", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "config.toml path")
	dir := fs.String("dir", "", "directory to serve")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	serveDir := *dir
	if serveDir == "" {
		serveDir = filepath.Join(cfg.System.StateDir, "ios")
	}
	return ios.Serve(serveDir, cfg.IOS.Listen, cfg.Network.InternalCIDR)
}

func cmdQUICGW(args []string) error {
	fs := flag.NewFlagSet("quicgw", flag.ContinueOnError)
	cfgPath, rulesPath, _ := commonPaths(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	return quic.Run(context.Background(), cfg, norm)
}

func cmdBot(args []string) error {
	fs := flag.NewFlagSet("bot", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath, "config.toml path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	return telegram.Run(context.Background(), cfg)
}

func commonPaths(fs *flag.FlagSet) (*string, *string, *string) {
	cfg := fs.String("config", defaultConfigPath, "config.toml path")
	rules := fs.String("rules", defaultRulesPath, "rules.toml path")
	out := fs.String("out", "./rendered", "render output directory")
	return cfg, rules, out
}

func loadAll(cfgPath, rulesPath string) (config.Config, rules.Normalized, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return config.Config{}, rules.Normalized{}, err
	}
	ruleFile, err := rules.Load(rulesPath)
	if err != nil {
		return config.Config{}, rules.Normalized{}, err
	}
	norm, err := rules.Normalize(ruleFile)
	return cfg, norm, err
}

type generatedInputs struct {
	config string
	rules  string
}

func loadOrWizard(cfgPath, rulesPath string, stdin io.Reader, out io.Writer, assumeYes bool) (config.Config, rules.Normalized, generatedInputs, error) {
	cfg, norm, err := loadAll(cfgPath, rulesPath)
	if err == nil {
		return cfg, norm, generatedInputs{}, nil
	}
	if !os.IsNotExist(err) {
		return config.Config{}, rules.Normalized{}, generatedInputs{}, err
	}
	fmt.Fprintln(out, "config or rules file missing; starting guided bootstrap")
	cfgText, rulesText := "", ""
	if !fileExists(cfgPath) {
		cfgText, _ = wizard(stdin, out, assumeYes)
	}
	if !fileExists(rulesPath) {
		rulesText = defaultRulesText()
	}
	cfg, norm, err = parseGeneratedOrExisting(cfgPath, rulesPath, cfgText, rulesText)
	return cfg, norm, generatedInputs{config: cfgText, rules: rulesText}, err
}

func wizard(stdin io.Reader, out io.Writer, assumeYes bool) (string, string) {
	return wizardWithDefaults(stdin, out, assumeYes, detectWizardDefaults())
}

type wizardDefaults struct {
	GatewayIP    string
	InternalCIDR string
	IngressIface string
}

func wizardWithDefaults(stdin io.Reader, out io.Writer, assumeYes bool, defaults wizardDefaults) (string, string) {
	reader := bufio.NewReader(stdin)
	gateway := prompt(reader, out, "gateway IP", defaults.GatewayIP, assumeYes)
	cidr := prompt(reader, out, "carrier internal CIDR", defaults.InternalCIDR, assumeYes)
	iface := prompt(reader, out, "ingress interface", defaults.IngressIface, assumeYes)
	apple := promptBool(reader, out, "enable Apple/iOS profile flow", true, assumeYes)
	iosConfig := `[ios]
enabled = false
`
	if apple {
		iosConfig = fmt.Sprintf(`[ios]
enabled = true
listen = "0.0.0.0:8088"
base_url = "http://%s:8088"
organization = "5gws"
profile_identifier = "dev.5gws.dot"
`, gateway)
	}
	configText := fmt.Sprintf(`[network]
gateway_ip = %q
internal_cidr = %q
ingress_iface = %q

%s
[[exits]]
name = "direct"
type = "direct"
fwmark = 0
`, gateway, cidr, iface, iosConfig)
	return configText, defaultRulesText()
}

func detectWizardDefaults() wizardDefaults {
	defaults := wizardDefaults{
		GatewayIP:    "10.0.0.1",
		InternalCIDR: "172.22.0.0/16",
		IngressIface: "eth0",
	}
	data, err := exec.Command("ip", "route", "show", "default").Output()
	if err != nil {
		return defaults
	}
	iface := parseDefaultIface(string(data))
	if iface == "" {
		return defaults
	}
	defaults.IngressIface = iface
	if ip := interfaceIPv4(iface); ip != "" {
		defaults.GatewayIP = ip
	}
	return defaults
}

func parseDefaultIface(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] == "dev" {
				return fields[i+1]
			}
		}
	}
	return ""
}

func interfaceIPv4(name string) string {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil || ip.IsLoopback() {
			continue
		}
		return ip.String()
	}
	return ""
}

func prompt(reader *bufio.Reader, out io.Writer, label, fallback string, assumeYes bool) string {
	if assumeYes {
		fmt.Fprintf(out, "%s: %s\n", label, fallback)
		return fallback
	}
	fmt.Fprintf(out, "%s [%s]: ", label, fallback)
	line, _ := reader.ReadString('\n')
	value := strings.TrimSpace(line)
	if value == "" {
		return fallback
	}
	return value
}

func promptBool(reader *bufio.Reader, out io.Writer, label string, fallback, assumeYes bool) bool {
	fallbackText := "n"
	if fallback {
		fallbackText = "Y"
	}
	if assumeYes {
		fmt.Fprintf(out, "%s: %s\n", label, yesNo(fallback))
		return fallback
	}
	for {
		fmt.Fprintf(out, "%s [%s]: ", label, fallbackText)
		line, err := reader.ReadString('\n')
		value := strings.ToLower(strings.TrimSpace(line))
		if value == "" {
			return fallback
		}
		switch value {
		case "y", "yes", "true", "1":
			return true
		case "n", "no", "false", "0":
			return false
		default:
			if err != nil {
				return fallback
			}
			fmt.Fprintln(out, "please answer y or n")
		}
	}
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func parseGeneratedOrExisting(cfgPath, rulesPath, cfgText, rulesText string) (config.Config, rules.Normalized, error) {
	tmp, err := os.MkdirTemp("", "5gws-install-*")
	if err != nil {
		return config.Config{}, rules.Normalized{}, err
	}
	defer os.RemoveAll(tmp)
	testCfg := cfgPath
	testRules := rulesPath
	if cfgText != "" {
		testCfg = filepath.Join(tmp, "config.toml")
	}
	if rulesText != "" {
		testRules = filepath.Join(tmp, "rules.toml")
	}
	if cfgText != "" {
		if err := os.WriteFile(testCfg, []byte(cfgText), 0o600); err != nil {
			return config.Config{}, rules.Normalized{}, err
		}
	}
	if rulesText != "" {
		if err := os.WriteFile(testRules, []byte(rulesText), 0o600); err != nil {
			return config.Config{}, rules.Normalized{}, err
		}
	}
	return loadAll(testCfg, testRules)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func defaultRulesText() string {
	return `[[imports]]
name = "cn"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.json"
dns_pool = "cn"

[[imports]]
name = "gfw"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.json"
exit = "direct"
`
}

func parseGenerated(cfgText, rulesText string) (config.Config, rules.Normalized, error) {
	tmp, err := os.MkdirTemp("", "5gws-install-*")
	if err != nil {
		return config.Config{}, rules.Normalized{}, err
	}
	defer os.RemoveAll(tmp)
	cfgPath := filepath.Join(tmp, "config.toml")
	rulesPath := filepath.Join(tmp, "rules.toml")
	if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
		return config.Config{}, rules.Normalized{}, err
	}
	if err := os.WriteFile(rulesPath, []byte(rulesText), 0o600); err != nil {
		return config.Config{}, rules.Normalized{}, err
	}
	return loadAll(cfgPath, rulesPath)
}

func writeGeneratedInputs(cfgPath, rulesPath string, generated generatedInputs) error {
	if generated.config != "" {
		if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(cfgPath, []byte(generated.config), 0o600); err != nil {
			return err
		}
	}
	if generated.rules != "" {
		if err := os.MkdirAll(filepath.Dir(rulesPath), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(rulesPath, []byte(generated.rules), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func printInstallSummary(w io.Writer, cfg config.Config, norm rules.Normalized) {
	fmt.Fprintf(w, "5gws install summary\n")
	fmt.Fprintf(w, "config_dir: %s\nstate_dir: %s\n", cfg.System.ConfigDir, cfg.System.StateDir)
	fmt.Fprintf(w, "internal_cidr: %s via %s\n", cfg.Network.InternalCIDR, cfg.Network.IngressIface)
	fmt.Fprintf(w, "redirect: tcp/80->%d tcp/443->%d udp/443->%d\n", cfg.Network.HTTPRedirectPort, cfg.Network.HTTPSRedirectPort, cfg.Network.QUICRedirectPort)
	fmt.Fprintf(w, "rules: %d\n", len(norm.Rules))
}

func confirm(stdin io.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y")
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("this command must run as root; use --dry-run for validation")
	}
	return nil
}

func runApplyCommands(cfg config.Config, out io.Writer) error {
	rendered := filepath.Join(cfg.System.StateDir, "rendered")
	if err := os.MkdirAll(cfg.System.RunDir, 0o755); err != nil {
		return err
	}
	if cfg.IOS.Enabled {
		if _, err := ios.Generate(cfg.DNS.CertDir, cfg); err != nil {
			return err
		}
	}
	if err := run(out, "nft", "-f", filepath.Join(rendered, "nftables", "5gws.nft")); err != nil {
		return err
	}
	for _, unit := range activeServices(cfg) {
		src := filepath.Join(rendered, "systemd", unit)
		dst := filepath.Join("/etc/systemd/system", unit)
		if err := copyFile(src, dst, 0o644); err != nil {
			return err
		}
	}
	if err := run(out, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, svc := range activeServices(cfg) {
		if err := run(out, "systemctl", "enable", svc); err != nil {
			return err
		}
		if err := run(out, "systemctl", "restart", svc); err != nil {
			return err
		}
	}
	return nil
}

func uninstallSteps(purge bool) []string {
	steps := []string{
		"stop and disable 5gws services",
		"destroy nft table inet fivegws",
		"remove /etc/systemd/system/5gws-*.service",
	}
	if purge {
		steps = append(steps, "remove /etc/5gws, /var/lib/5gws, /run/5gws")
	}
	return steps
}

func runUninstall(purge bool, out io.Writer) error {
	for _, svc := range allServices() {
		_ = run(out, "systemctl", "disable", "--now", svc)
		_ = os.Remove(filepath.Join("/etc/systemd/system", svc))
	}
	matches, err := filepath.Glob("/etc/systemd/system/5gws-ssrust-*.service")
	if err != nil {
		return err
	}
	for _, path := range matches {
		name := filepath.Base(path)
		_ = run(out, "systemctl", "disable", "--now", name)
		_ = os.Remove(path)
	}
	_ = run(out, "nft", "destroy", "table", "inet", "fivegws")
	if purge {
		for _, path := range []string{"/etc/5gws", "/var/lib/5gws", "/run/5gws"} {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
	}
	return run(out, "systemctl", "daemon-reload")
}

func activeServices(cfg config.Config) []string {
	services := []string{"5gws-smartdns.service", "5gws-haproxy.service", "5gws-quic.service"}
	for _, exit := range cfg.Exits {
		if exit.Type == "shadowsocks-rust" {
			services = append(services, "5gws-ssrust-"+exit.Name+".service")
		}
	}
	if cfg.IOS.Enabled {
		services = append(services, "5gws-cert.service")
	}
	if cfg.Telegram.Enabled {
		services = append(services, "5gws-bot.service")
	}
	return services
}

func allServices() []string {
	return []string{"5gws-smartdns.service", "5gws-haproxy.service", "5gws-quic.service", "5gws-cert.service", "5gws-bot.service"}
}

func run(out io.Writer, name string, args ...string) error {
	fmt.Fprintf(out, "+ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	data, err := cmd.CombinedOutput()
	if len(data) > 0 {
		fmt.Fprint(out, string(data))
	}
	return err
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}
