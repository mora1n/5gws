package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	case "update":
		return cmdUpdate(args[1:], stdout)
	case "install-smartdns":
		return cmdInstallSmartDNS(args[1:], stdout)
	case "install-ssrust":
		return cmdInstallSSRust(args[1:], stdout)
	case "uninstall":
		return cmdUninstall(args[1:], stdout)
	case "doctor":
		return cmdDoctor(args[1:], stdout)
	case "logs":
		return cmdLogs(args[1:], stdout)
	case "detect-cidr":
		return cmdDetectCIDR(args[1:], stdout)
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
  update            update binary with sha256 verification and rollback
  apply             render config and restart services
  doctor            validate config and runtime deps
  logs              show journald logs for 5gws services
  detect-cidr       observe client source IPs for internal_cidr
  status            show service status
  uninstall         remove 5gws services and state

Client:
  ios-link          generate iOS profile link and QR code

Tools:
  render            render files for inspection
  install-smartdns  install smartdns-rs explicitly
  install-ssrust    install shadowsocks-rust explicitly

Runtime/debug:
  cert-server       serve iOS files; usually managed by apply
  quicgw            UDP/443 QUIC and STUN proxy; usually managed by apply
  bot               Telegram bot; usually managed by apply
`)
}

func cmdRender(args []string, out io.Writer) error {
	fs := newCommandFlags("render")
	cfgPath, rulesPath, outDir := commonPaths(fs)
	if err := fs.parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	printRuleWarnings(out, norm.Warnings)
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
	fs := newCommandFlags("apply")
	cfgPath, rulesPath, _ := commonPaths(fs)
	dryRun := fs.Bool("dry-run", "n", false, "render only")
	skipBotRestart := fs.Bool("skip-bot-restart", "", false, "do not restart 5gws-bot.service")
	if err := fs.parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	printRuleWarnings(out, norm.Warnings)
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
	return runApplyCommands(cfg, out, *skipBotRestart)
}

func cmdInstall(args []string, stdin io.Reader, out io.Writer) error {
	fs := newCommandFlags("install")
	cfgPath, rulesPath, _ := commonPaths(fs)
	assumeYes := fs.Bool("assume-yes", "y", false, "skip confirmation")
	dryRun := fs.Bool("dry-run", "n", false, "show actions without writing system state")
	reconfigure := fs.Bool("reconfigure", "R", false, "rerun guided config and overwrite generated inputs")
	if err := fs.parse(args); err != nil {
		return err
	}
	reader := bufio.NewReader(stdin)
	cfg, norm, generated, err := loadOrWizard(*cfgPath, *rulesPath, reader, out, *assumeYes, *reconfigure)
	if err != nil {
		return err
	}
	printInstallSummary(out, cfg, norm)
	printRuleWarnings(out, norm.Warnings)
	if !*assumeYes && !confirm(reader, out, "Continue install?") {
		return errors.New("install cancelled")
	}
	if *dryRun {
		if err := installer.EnsureRuntime(cfg, true, out); err != nil {
			return err
		}
		if err := ensureDOTCertificate(cfg, true, out); err != nil {
			return err
		}
		printIOSInstallHint(out, cfg, *cfgPath, true)
		fmt.Fprintln(out, "dry-run: no files or services changed")
		return nil
	}
	if err := requireRoot(); err != nil {
		return err
	}
	if err := installer.EnsureRuntime(cfg, false, out); err != nil {
		return err
	}
	if err := ensureDOTCertificate(cfg, false, out); err != nil {
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
	if err := runApplyCommands(cfg, out, false); err != nil {
		return err
	}
	return printIOSInstallHint(out, cfg, *cfgPath, false)
}

func cmdInstallSmartDNS(args []string, out io.Writer) error {
	fs := newCommandFlags("install-smartdns")
	dryRun := fs.Bool("dry-run", "n", false, "show actions without installing")
	yes := fs.Bool("yes", "y", false, "install without confirmation")
	version := fs.String("version", "v", installer.DefaultSmartDNSVersion, "smartdns-rs version")
	if err := fs.parse(args); err != nil {
		return err
	}
	return installer.InstallSmartDNS(installer.Options{DryRun: *dryRun, Yes: *yes, Version: *version}, out)
}

func cmdInstallSSRust(args []string, out io.Writer) error {
	fs := newCommandFlags("install-ssrust")
	dryRun := fs.Bool("dry-run", "n", false, "show actions without installing")
	yes := fs.Bool("yes", "y", false, "install without confirmation")
	version := fs.String("version", "v", installer.DefaultSSRustVersion, "shadowsocks-rust version")
	if err := fs.parse(args); err != nil {
		return err
	}
	return installer.InstallSSRust(installer.Options{DryRun: *dryRun, Yes: *yes, Version: *version}, out)
}

func cmdUninstall(args []string, out io.Writer) error {
	fs := newCommandFlags("uninstall")
	purge := fs.Bool("purge", "p", false, "remove config and state")
	yes := fs.Bool("yes", "y", false, "confirm destructive uninstall")
	dryRun := fs.Bool("dry-run", "n", false, "show actions without changing system state")
	if err := fs.parse(args); err != nil {
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
	fs := newCommandFlags("doctor")
	cfgPath, rulesPath, _ := commonPaths(fs)
	if err := fs.parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	printRuleWarnings(out, norm.Warnings)
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
	writeDoctorRuntime(out, cfg)
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
	fs := newCommandFlags("ios-link")
	cfgPath := fs.String("config", "c", defaultConfigPath, "config.toml path")
	outDir := fs.String("out", "o", "", "output directory")
	noQR := fs.Bool("no-qr", "q", false, "print links only")
	if err := fs.parse(args); err != nil {
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
	fmt.Fprintf(out, "profile: %s\nprofile_qr: %s\n", links.ProfileURL, links.ProfileQR)
	if *noQR {
		return nil
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
	fs := newCommandFlags("cert-server")
	cfgPath := fs.String("config", "c", defaultConfigPath, "config.toml path")
	dir := fs.String("dir", "d", "", "directory to serve")
	if err := fs.parse(args); err != nil {
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
	fs := newCommandFlags("quicgw")
	cfgPath, rulesPath, _ := commonPaths(fs)
	if err := fs.parse(args); err != nil {
		return err
	}
	cfg, norm, err := loadAll(*cfgPath, *rulesPath)
	if err != nil {
		return err
	}
	logRuleWarnings(norm.Warnings)
	return quic.Run(context.Background(), cfg, norm)
}

func cmdBot(args []string) error {
	fs := newCommandFlags("bot")
	cfgPath := fs.String("config", "c", defaultConfigPath, "config.toml path")
	rulesPath := fs.String("rules", "r", defaultRulesPath, "rules.toml path")
	if err := fs.parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	return telegram.Run(context.Background(), cfg, *cfgPath, *rulesPath)
}

func commonPaths(fs *commandFlags) (*string, *string, *string) {
	cfg := fs.String("config", "c", defaultConfigPath, "config.toml path")
	rules := fs.String("rules", "r", defaultRulesPath, "rules.toml path")
	out := fs.String("out", "o", "./rendered", "render output directory")
	return cfg, rules, out
}

type commandFlags struct {
	*flag.FlagSet
	aliases map[string]string
}

func newCommandFlags(name string) *commandFlags {
	return &commandFlags{
		FlagSet: flag.NewFlagSet(name, flag.ContinueOnError),
		aliases: map[string]string{},
	}
}

func (f *commandFlags) String(name, short, value, usage string) *string {
	out := f.FlagSet.String(name, value, usage)
	if short != "" {
		f.FlagSet.StringVar(out, short, value, usage)
		f.aliases[name] = short
	}
	return out
}

func (f *commandFlags) Bool(name, short string, value bool, usage string) *bool {
	out := f.FlagSet.Bool(name, value, usage)
	if short != "" {
		f.FlagSet.BoolVar(out, short, value, usage)
		f.aliases[name] = short
	}
	return out
}

func (f *commandFlags) Int(name, short string, value int, usage string) *int {
	out := f.FlagSet.Int(name, value, usage)
	if short != "" {
		f.FlagSet.IntVar(out, short, value, usage)
		f.aliases[name] = short
	}
	return out
}

func (f *commandFlags) parse(args []string) error {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		name, ok := singleDashLongName(arg)
		if !ok || f.Lookup(name) == nil {
			continue
		}
		msg := fmt.Sprintf("long flag -%s must use --%s", name, name)
		if short := f.aliases[name]; short != "" {
			msg += fmt.Sprintf(" or -%s", short)
		}
		return errors.New(msg)
	}
	return f.FlagSet.Parse(args)
}

func singleDashLongName(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") || arg == "-" {
		return "", false
	}
	name := strings.TrimPrefix(arg, "-")
	if i := strings.IndexByte(name, '='); i >= 0 {
		name = name[:i]
	}
	return name, len(name) > 1
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

func printRuleWarnings(out io.Writer, warnings []rules.Warning) {
	for _, warning := range warnings {
		fmt.Fprintf(out, "warning: %s\n", warning.String())
	}
}

func logRuleWarnings(warnings []rules.Warning) {
	for _, warning := range warnings {
		log.Printf("warning: %s", warning.String())
	}
}

type generatedInputs struct {
	config string
	rules  string
}

func loadOrWizard(cfgPath, rulesPath string, reader *bufio.Reader, out io.Writer, assumeYes, reconfigure bool) (config.Config, rules.Normalized, generatedInputs, error) {
	existing, hasExisting := loadExistingDraft(cfgPath)
	if reconfigure {
		fmt.Fprintln(out, "reconfigure requested; starting guided bootstrap")
		cfgText, rulesText := wizard(reader, out, assumeYes, existing, hasExisting)
		cfg, norm, err := parseGenerated(cfgText, rulesText)
		return cfg, norm, generatedInputs{config: cfgText, rules: rulesText}, err
	}
	cfgText, rulesText := "", ""
	if hasExisting {
		fmt.Fprintf(out, "using existing %s as install defaults\n", cfgPath)
		cfgText, _ = wizard(reader, out, assumeYes, existing, true)
	} else {
		fmt.Fprintln(out, "config file missing; starting guided bootstrap")
		cfgText, _ = wizard(reader, out, assumeYes, config.Config{}, false)
	}
	if !fileExists(rulesPath) {
		rulesText = defaultRulesText()
	}
	cfg, norm, err := parseGeneratedOrExisting(cfgPath, rulesPath, cfgText, rulesText)
	return cfg, norm, generatedInputs{config: cfgText, rules: rulesText}, err
}

func loadExistingDraft(cfgPath string) (config.Config, bool) {
	cfg, err := config.LoadDraft(cfgPath)
	return cfg, err == nil
}

func wizard(reader *bufio.Reader, out io.Writer, assumeYes bool, existing config.Config, hasExisting bool) (string, string) {
	return wizardWithDefaults(reader, out, assumeYes, detectWizardDefaults(existing, hasExisting))
}

type wizardDefaults struct {
	GatewayIP    string
	InternalCIDR string
	IngressIface string
	DOTDomain    string
	AppleEnabled bool
	HasApple     bool
	Config       config.Config
}

func wizardWithDefaults(reader *bufio.Reader, out io.Writer, assumeYes bool, defaults wizardDefaults) (string, string) {
	gateway := prompt(reader, out, "gateway IP", defaults.GatewayIP, assumeYes)
	cidr := prompt(reader, out, "carrier internal CIDR", defaults.InternalCIDR, assumeYes)
	iface := prompt(reader, out, "ingress interface", defaults.IngressIface, assumeYes)
	dotDomain := promptRequired(reader, out, "DoT domain", defaults.DOTDomain, assumeYes)
	appleDefault := true
	if defaults.HasApple {
		appleDefault = defaults.AppleEnabled
	}
	apple := promptBool(reader, out, "enable Apple/iOS profile flow", appleDefault, assumeYes)
	cfg := defaults.Config
	cfg.Network.GatewayIP = gateway
	cfg.Network.InternalCIDR = cidr
	cfg.Network.IngressIface = iface
	cfg.DNS.DOTDomain = strings.ToLower(strings.TrimSuffix(dotDomain, "."))
	cfg.DNS.CertFile = "/etc/5gws/certs/fullchain.pem"
	cfg.DNS.KeyFile = "/etc/5gws/certs/privkey.pem"
	cfg.IOS.Enabled = apple
	if apple {
		cfg.IOS.Listen = valueOr(cfg.IOS.Listen, "0.0.0.0:8088")
		cfg.IOS.BaseURL = "http://" + gateway + ":8088"
		cfg.IOS.Organization = valueOr(cfg.IOS.Organization, "5gws")
		cfg.IOS.ProfileIdentifier = valueOr(cfg.IOS.ProfileIdentifier, "dev.5gws.dot")
	}
	if len(cfg.Exits) == 0 {
		cfg.Exits = []config.ExitConfig{{Name: "direct", Type: "direct", FWMark: 0}}
	}
	cfg.ApplyDefaults()
	configText, err := encodeConfig(cfg)
	if err != nil {
		return "", ""
	}
	return configText, defaultRulesText()
}

func detectWizardDefaults(existing config.Config, hasExisting bool) wizardDefaults {
	defaults := wizardDefaults{
		GatewayIP:    "10.0.0.1",
		InternalCIDR: "172.22.0.0/16",
		IngressIface: "eth0",
		AppleEnabled: true,
	}
	if hasExisting {
		defaults.GatewayIP = existing.Network.GatewayIP
		defaults.InternalCIDR = existing.Network.InternalCIDR
		defaults.IngressIface = existing.Network.IngressIface
		defaults.DOTDomain = existing.DNS.DOTDomain
		defaults.AppleEnabled = existing.IOS.Enabled
		defaults.HasApple = true
		defaults.Config = existing
		if defaults.GatewayIP != "" && defaults.InternalCIDR != "" && defaults.IngressIface != "" {
			return defaults
		}
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

func encodeConfig(cfg config.Config) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "[network]\ngateway_ip = %q\ninternal_cidr = %q\ningress_iface = %q\n", cfg.Network.GatewayIP, cfg.Network.InternalCIDR, cfg.Network.IngressIface)
	writeNonZeroInt(&b, "http_redirect_port", cfg.Network.HTTPRedirectPort, 18080)
	writeNonZeroInt(&b, "https_redirect_port", cfg.Network.HTTPSRedirectPort, 18443)
	writeNonZeroInt(&b, "quic_redirect_port", cfg.Network.QUICRedirectPort, 18443)
	writeNonZeroInt(&b, "tcp_redirect_port", cfg.Network.TCPRedirectPort, 18082)
	if cfg.Network.QUICPolicy != "" && cfg.Network.QUICPolicy != "reject" {
		fmt.Fprintf(&b, "quic_policy = %q\n", cfg.Network.QUICPolicy)
	}
	if cfg.Network.EncryptedDNSPolicy != "" && cfg.Network.EncryptedDNSPolicy != "reject" {
		fmt.Fprintf(&b, "encrypted_dns_policy = %q\n", cfg.Network.EncryptedDNSPolicy)
	}
	fmt.Fprintf(&b, "\n[routing]\nfallback_exit = %q\n", cfg.Routing.FallbackExit)
	fmt.Fprintf(&b, "\n[dns]\ndot_domain = %q\ncert_file = %q\nkey_file = %q\n", cfg.DNS.DOTDomain, cfg.DNS.CertFile, cfg.DNS.KeyFile)
	writeStringSlice(&b, "backend_resolvers", cfg.DNS.BackendResolvers, nil)
	writeStringSlice(&b, "upstreams_cn", cfg.DNS.UpstreamsCN, nil)
	writeStringSlice(&b, "upstreams_overseas_private", cfg.DNS.UpstreamsOverseasPrivate, nil)
	writeStringSlice(&b, "upstreams_overseas_public", cfg.DNS.UpstreamsOverseasPublic, nil)
	fmt.Fprintf(&b, "\n[logging]\nlevel = %q\naccess = %t\n", cfg.Logging.Level, cfg.Logging.AccessEnabled())
	fmt.Fprintf(&b, "\n[ios]\nenabled = %t\n", cfg.IOS.Enabled)
	if cfg.IOS.Enabled {
		fmt.Fprintf(&b, "listen = %q\nbase_url = %q\norganization = %q\nprofile_identifier = %q\n", cfg.IOS.Listen, cfg.IOS.BaseURL, cfg.IOS.Organization, cfg.IOS.ProfileIdentifier)
	}
	if cfg.Telegram.Enabled || cfg.Telegram.BotEnv != "" || len(cfg.Telegram.AllowedUsers) > 0 {
		fmt.Fprintf(&b, "\n[telegram]\nenabled = %t\n", cfg.Telegram.Enabled)
		if cfg.Telegram.BotEnv != "" {
			fmt.Fprintf(&b, "bot_env = %q\n", cfg.Telegram.BotEnv)
		}
		writeStringSlice(&b, "allowed_users", cfg.Telegram.AllowedUsers, nil)
	}
	writeTCPProxies(&b, cfg.TCPProxies)
	writeUDPProxies(&b, cfg.UDPProxies)
	for _, exit := range cfg.Exits {
		writeExitConfig(&b, exit)
	}
	return b.String(), nil
}

func valueOr(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func writeNonZeroInt(b *strings.Builder, name string, value, defaultValue int) {
	if value != 0 && value != defaultValue {
		fmt.Fprintf(b, "%s = %d\n", name, value)
	}
}

func writeStringSlice(b *strings.Builder, name string, values, defaultValues []string) {
	if len(values) == 0 || sameStrings(values, defaultValues) {
		return
	}
	fmt.Fprintf(b, "%s = [", name)
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(b, "%q", value)
	}
	b.WriteString("]\n")
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeTCPProxies(b *strings.Builder, proxies []config.TCPProxyConfig) {
	if sameTCPProxies(proxies, config.DefaultTCPProxies()) {
		return
	}
	for _, proxy := range proxies {
		fmt.Fprintf(b, "\n[[tcp_proxies]]\nname = %q\nclient_port = %d\nlisten_port = %d\nexit = %q\n",
			proxy.Name, proxy.ClientPort, proxy.ListenPort, proxy.Exit)
	}
}

func sameTCPProxies(a, b []config.TCPProxyConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeUDPProxies(b *strings.Builder, proxies []config.UDPProxyConfig) {
	if sameUDPProxies(proxies, config.DefaultUDPProxies()) {
		return
	}
	for _, proxy := range proxies {
		fmt.Fprintf(b, "\n[[udp_proxies]]\nname = %q\nclient_port = %d\nlisten_port = %d\ntarget = %q\nexit = %q\n",
			proxy.Name, proxy.ClientPort, proxy.ListenPort, proxy.Target, proxy.Exit)
	}
}

func sameUDPProxies(a, b []config.UDPProxyConfig) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeExitConfig(b *strings.Builder, exit config.ExitConfig) {
	fmt.Fprintf(b, "\n[[exits]]\nname = %q\ntype = %q\n", exit.Name, exit.Type)
	if exit.FWMark != 0 {
		fmt.Fprintf(b, "fwmark = %d\n", exit.FWMark)
	}
	if exit.Type != "shadowsocks-rust" {
		return
	}
	fmt.Fprintf(b, "server = %q\nserver_port = %d\nmethod = %q\npassword = %q\n", exit.Server, exit.ServerPort, exit.Method, exit.Password)
	if exit.Username != "" {
		fmt.Fprintf(b, "username = %q\n", exit.Username)
	}
	fmt.Fprintf(b, "listen_address = %q\nlisten_port = %d\n", exit.ListenAddress, exit.ListenPort)
	if exit.TCP != nil {
		fmt.Fprintf(b, "tcp = %t\n", *exit.TCP)
	}
	if exit.UDP != nil {
		fmt.Fprintf(b, "udp = %t\n", *exit.UDP)
	}
	if exit.TimeoutSeconds != 0 {
		fmt.Fprintf(b, "timeout_seconds = %d\n", exit.TimeoutSeconds)
	}
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

func promptRequired(reader *bufio.Reader, out io.Writer, label, fallback string, assumeYes bool) string {
	if assumeYes {
		if fallback == "" {
			return ""
		}
		fmt.Fprintf(out, "%s: %s\n", label, fallback)
		return fallback
	}
	for {
		if fallback == "" {
			fmt.Fprintf(out, "%s: ", label)
		} else {
			fmt.Fprintf(out, "%s [%s]: ", label, fallback)
		}
		line, err := reader.ReadString('\n')
		value := strings.TrimSpace(line)
		if value != "" {
			return value
		}
		if fallback != "" {
			return fallback
		}
		if err != nil {
			return ""
		}
		fmt.Fprintf(out, "%s is required\n", label)
	}
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
	return `[[rules]]
name = "ip-check"
exit = "direct"
domain_suffix = [
  "icanhazip.com",
  "ipinfo.io",
  "ippure.com",
]

[[rules]]
name = "ippure-stun"
exit = "direct"
domain = [
  "stun.chat.bilibili.com",
  "stun.cloudflare.com",
  "stun.hitv.com",
  "stun.l.google.com",
  "stun.miwifi.com",
]

[[imports]]
name = "speedtest"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/category-speedtest.json"
exit = "direct"

[[imports]]
name = "cn"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/cn.json"
dns_pool = "cn"

[[imports]]
name = "gfw"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/gfw.json"
exit = "direct"

[[imports]]
name = "ip-geo-detect"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/category-ip-geo-detect.json"
exit = "direct"

[[imports]]
name = "stun"
type = "sing-box"
url = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/sing/geo/geosite/category-stun.json"
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
	fmt.Fprintf(w, "dot_domain: %s\n", cfg.DNS.DOTDomain)
	fmt.Fprintf(w, "redirect: tcp/80->%d tcp/443->%d tcp/other->%d udp/443=%s encrypted_dns=%s\n", cfg.Network.HTTPRedirectPort, cfg.Network.HTTPSRedirectPort, cfg.Network.TCPRedirectPort, cfg.Network.QUICPolicy, cfg.Network.EncryptedDNSPolicy)
	for _, proxy := range cfg.TCPProxies {
		fmt.Fprintf(w, "tcp_proxy: tcp/%d->%d exit=%s\n", proxy.ClientPort, proxy.ListenPort, proxy.Exit)
	}
	for _, proxy := range cfg.UDPProxies {
		fmt.Fprintf(w, "udp_proxy: udp/%d->%d target=%s exit=%s\n", proxy.ClientPort, proxy.ListenPort, proxy.Target, proxy.Exit)
	}
	fmt.Fprintf(w, "rules: %d\n", len(norm.Rules))
}

func printIOSInstallHint(out io.Writer, cfg config.Config, cfgPath string, dryRun bool) error {
	if !cfg.IOS.Enabled {
		fmt.Fprintf(out, "iOS profile flow disabled; enable [ios] and run: 5gws ios-link --config %s\n", cfgPath)
		return nil
	}
	if dryRun {
		fmt.Fprintf(out, "dry-run: would print iOS QR codes with: 5gws ios-link --config %s\n", cfgPath)
		return nil
	}
	fmt.Fprintln(out, "\niOS profile links:")
	return cmdIOSLink([]string{"--config", cfgPath}, out)
}

func confirm(reader *bufio.Reader, out io.Writer, prompt string) bool {
	fmt.Fprintf(out, "%s [y/N] ", prompt)
	line, _ := reader.ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y")
}

func ensureDOTCertificate(cfg config.Config, dryRun bool, out io.Writer) error {
	if cfg.DNS.ListenPublicDOT == "" {
		return nil
	}
	if err := verifyDOTDomainA(cfg.DNS.DOTDomain, cfg.Network.GatewayIP); err != nil {
		return err
	}
	liveDir := filepath.Join("/etc/letsencrypt/live", cfg.DNS.DOTDomain)
	if dryRun {
		fmt.Fprintf(out, "dry-run: would ensure certbot certificate for %s\n", cfg.DNS.DOTDomain)
		fmt.Fprintf(out, "dry-run: would deploy cert to %s and %s\n", cfg.DNS.CertFile, cfg.DNS.KeyFile)
		return nil
	}
	if err := ensureCertbot(out); err != nil {
		return err
	}
	if !certificateValid(filepath.Join(liveDir, "fullchain.pem"), 30*24*time.Hour) {
		if err := run(out, "certbot", "certonly", "--standalone", "-d", cfg.DNS.DOTDomain, "--non-interactive", "--agree-tos", "--register-unsafely-without-email", "--keep-until-expiring"); err != nil {
			return err
		}
	}
	if err := deployDOTCertificate(liveDir, cfg); err != nil {
		return err
	}
	return installCertDeployHook(cfg, out)
}

func verifyDOTDomainA(domain, gatewayIP string) error {
	addrs, err := net.LookupHost(domain)
	if err != nil {
		return fmt.Errorf("dns.dot_domain %q does not resolve: %w", domain, err)
	}
	for _, addr := range addrs {
		if addr == gatewayIP {
			return nil
		}
	}
	return fmt.Errorf("dns.dot_domain %q resolves to %s, want gateway_ip %s", domain, strings.Join(addrs, ", "), gatewayIP)
}

func ensureCertbot(out io.Writer) error {
	if path, err := exec.LookPath("certbot"); err == nil {
		fmt.Fprintf(out, "certbot: %s\n", path)
		return nil
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return errors.New("certbot is missing and apt-get is unavailable; install certbot manually")
	}
	if err := run(out, "apt-get", "update"); err != nil {
		return err
	}
	return run(out, "apt-get", "install", "-y", "certbot")
}

func certificateValid(path string, minRemaining time.Duration) bool {
	if _, err := os.Stat(path); err != nil {
		return false
	}
	cmd := exec.Command("openssl", "x509", "-checkend", fmt.Sprint(int(minRemaining.Seconds())), "-noout", "-in", path)
	return cmd.Run() == nil
}

func deployDOTCertificate(liveDir string, cfg config.Config) error {
	certFile := cfg.DNS.CertFile
	keyFile := cfg.DNS.KeyFile
	if err := os.MkdirAll(filepath.Dir(certFile), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o755); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(liveDir, "fullchain.pem"), certFile, 0o644); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(liveDir, "privkey.pem"), keyFile, 0o640); err != nil {
		return err
	}
	return setDOTCertificatePermissions(cfg)
}

func setDOTCertificatePermissions(cfg config.Config) error {
	for _, dir := range uniqueStrings([]string{filepath.Dir(cfg.DNS.CertFile), filepath.Dir(cfg.DNS.KeyFile)}) {
		if err := os.Chown(dir, 0, 65534); err != nil {
			return err
		}
		if err := os.Chmod(dir, 0o750); err != nil {
			return err
		}
	}
	if pathWithin(cfg.System.ConfigDir, cfg.DNS.CertFile) || pathWithin(cfg.System.ConfigDir, cfg.DNS.KeyFile) {
		if err := os.Chmod(cfg.System.ConfigDir, 0o711); err != nil {
			return err
		}
	}
	if err := os.Chown(cfg.DNS.KeyFile, 0, 65534); err != nil {
		return err
	}
	return os.Chmod(cfg.DNS.KeyFile, 0o640)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func installCertDeployHook(cfg config.Config, out io.Writer) error {
	hookDir := "/etc/letsencrypt/renewal-hooks/deploy"
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		return err
	}
	hook := fmt.Sprintf(`#!/bin/sh
set -eu
DOMAIN=%q
LIVE=/etc/letsencrypt/live/$DOMAIN
CERT_FILE=%q
KEY_FILE=%q
CONFIG_DIR=%q
if [ -d "$LIVE" ]; then
  chmod 711 "$CONFIG_DIR"
  install -d -m 755 "$(dirname "$CERT_FILE")"
  install -d -m 755 "$(dirname "$KEY_FILE")"
  install -m 644 "$LIVE/fullchain.pem" "$CERT_FILE"
  install -m 640 "$LIVE/privkey.pem" "$KEY_FILE"
  chown root:65534 "$(dirname "$CERT_FILE")" "$(dirname "$KEY_FILE")" "$KEY_FILE"
  chmod 750 "$(dirname "$CERT_FILE")" "$(dirname "$KEY_FILE")"
  systemctl is-active --quiet 5gws-smartdns.service && systemctl restart 5gws-smartdns.service
fi
`, cfg.DNS.DOTDomain, cfg.DNS.CertFile, cfg.DNS.KeyFile, cfg.System.ConfigDir)
	path := filepath.Join(hookDir, "99-5gws-dot-cert.sh")
	if err := os.WriteFile(path, []byte(hook), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(out, "cert deploy hook: %s\n", path)
	return nil
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("this command must run as root; use --dry-run for validation")
	}
	return nil
}

func runApplyCommands(cfg config.Config, out io.Writer, skipBotRestart bool) error {
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
		if skipBotRestart && svc == "5gws-bot.service" {
			fmt.Fprintln(out, "skip restart 5gws-bot.service")
			continue
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
