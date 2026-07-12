package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/morain/5gws/internal/auth"
	"github.com/morain/5gws/internal/client"
	"github.com/morain/5gws/internal/daemon"
	"github.com/morain/5gws/internal/installer"
	"github.com/morain/5gws/internal/store"
)

var BuildVersion = "dev"

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		usage(stdout)
		return nil
	}
	switch args[0] {
	case "daemon":
		return runDaemon(args[1:])
	case "install":
		return runInstall(args[1:], stdin, stdout)
	case "reset-admin":
		return resetAdmin(args[1:], stdout)
	case "uninstall":
		return runUninstall(args[1:], stdout)
	case "install-smartdns":
		return runInstallComponent(args[1:], stdout, true)
	case "install-ssrust":
		return runInstallComponent(args[1:], stdout, false)
	case "status":
		return online(http.MethodGet, "/api/v1/dashboard", stdout)
	case "doctor":
		return online(http.MethodGet, "/api/v1/diagnostics", stdout)
	case "logs":
		return logs(stdout)
	case "compact":
		return compact(args[1:], stdout)
	case "apply":
		return online(http.MethodPost, "/api/v1/apply", stdout)
	case "update":
		return online(http.MethodPost, "/api/v1/update", stdout)
	case "ios-link":
		return online(http.MethodGet, "/api/v1/ios/profile", stdout)
	case "export":
		return exportBackup(args[1:], stdout)
	case "import":
		return importBackup(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command %q; run '5gws help'", args[0])
	}
}

func usage(out io.Writer) {
	fmt.Fprint(out, `Usage: 5gws <command>

Setup:
  install             initialize SQLite and 5gws.service
  reset-admin         create or reset the admin login and print a new password
  uninstall           remove the service and optionally all state
  install-smartdns    install the pinned smartdns-rs runtime
  install-ssrust      install the pinned shadowsocks-rust runtime

Daemon operations (root, via /run/5gws/control.sock):
  status              show active configuration and managed processes
  doctor              show runtime diagnostics
  logs                show recent daemon and child-process logs
  compact             compact SQLite after stopping 5gws.service
  apply               validate and apply the pending CLI configuration
  export FILE         export the active configuration as TOML
  import FILE         import TOML as pending CLI configuration
  update              install the latest verified release
  ios-link            show the generated iOS profile links
`)
}

func runDaemon(args []string) error {
	flags := flag.NewFlagSet("daemon", flag.ContinueOnError)
	database := flags.String("database", "/var/lib/5gws/5gws.db", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return daemon.Run(ctx, daemon.Options{Database: *database, Version: BuildVersion})
}

func online(method, path string, out io.Writer) error {
	data, err := control().Do(context.Background(), method, path, nil)
	if err != nil {
		return err
	}
	var value any
	if json.Unmarshal(data, &value) == nil {
		data, _ = json.MarshalIndent(value, "", "  ")
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

func logs(out io.Writer) error {
	data, err := control().Do(context.Background(), http.MethodGet, "/api/v1/logs?lines=500", nil)
	if err != nil {
		return err
	}
	var payload struct {
		Logs string `json:"logs"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	_, err = fmt.Fprint(out, payload.Logs)
	return err
}

func resetAdmin(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("reset-admin", flag.ContinueOnError)
	database := flags.String("database", "/var/lib/5gws/5gws.db", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: 5gws reset-admin [--database PATH]")
	}
	if os.Geteuid() != 0 {
		return errors.New("reset-admin must run as root")
	}
	password, err := auth.GeneratePassword()
	if err != nil {
		return err
	}
	state, err := store.Open(filepath.Clean(*database))
	if err != nil {
		return err
	}
	defer state.Close()
	user, err := auth.New(state.DB(), 24*time.Hour).ResetAdmin(context.Background(), password)
	if err != nil {
		return err
	}
	printAdminCredentials(out, user.Username, password)
	return nil
}

func compact(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("compact", flag.ContinueOnError)
	database := flags.String("database", "/var/lib/5gws/5gws.db", "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: 5gws compact [--database PATH]")
	}
	if os.Geteuid() != 0 {
		return errors.New("compact must run as root")
	}
	state, err := store.Open(filepath.Clean(*database))
	if err != nil {
		return err
	}
	defer state.Close()
	if err := state.Compact(context.Background()); err != nil {
		return err
	}
	fmt.Fprintln(out, "database compacted")
	return nil
}

func exportBackup(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: 5gws export FILE")
	}
	data, err := control().DoRaw(context.Background(), http.MethodGet, "/api/v1/backup", "", nil)
	if err != nil {
		return err
	}
	if err := os.WriteFile(args[0], data, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(out, "exported %s\n", args[0])
	return nil
}

func importBackup(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: 5gws import FILE")
	}
	file, err := os.Open(args[0])
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := control().DoRaw(context.Background(), http.MethodPost, "/api/v1/backup", "application/toml", file)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, strings.TrimSpace(string(data)))
	return nil
}

func runInstallComponent(args []string, out io.Writer, smartDNS bool) error {
	flags := flag.NewFlagSet("install-component", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "show actions only")
	yes := flags.Bool("yes", false, "install without confirmation")
	version := flags.String("version", "", "release version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	opts := installer.Options{DryRun: *dryRun, Yes: *yes, Version: *version}
	if smartDNS {
		return installer.InstallSmartDNS(opts, out)
	}
	return installer.InstallSSRust(opts, out)
}

func control() *client.Client { return client.New("/run/5gws/control.sock") }
