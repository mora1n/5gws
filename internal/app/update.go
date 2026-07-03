package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/morain/5gws/internal/config"
)

const defaultUpdateRepo = "mora1n/5gws"

type updateOptions struct {
	Repo       string
	Version    string
	Binary     string
	ConfigPath string
	RulesPath  string
	DryRun     bool
}

type updateCommandRunner interface {
	Run(out io.Writer, name string, args ...string) error
	Output(name string, args ...string) ([]byte, error)
}

type osUpdateRunner struct{}

func (osUpdateRunner) Run(out io.Writer, name string, args ...string) error {
	return run(out, name, args...)
}

func (osUpdateRunner) Output(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func cmdUpdate(args []string, out io.Writer) error {
	fs := newCommandFlags("update")
	cfgPath := fs.String("config", "c", defaultConfigPath, "config.toml path")
	rulesPath := fs.String("rules", "r", defaultRulesPath, "rules.toml path")
	version := fs.String("version", "v", "", "release version; empty means latest")
	repo := fs.String("repo", "", updateDefaultRepo(), "GitHub repo owner/name")
	binary := fs.String("binary", "", "/usr/local/bin/5gws", "installed 5gws binary path")
	dryRun := fs.Bool("dry-run", "n", false, "show update plan without changing system state")
	if err := fs.parse(args); err != nil {
		return err
	}
	opts := updateOptions{
		Repo:       *repo,
		Version:    *version,
		Binary:     *binary,
		ConfigPath: *cfgPath,
		RulesPath:  *rulesPath,
		DryRun:     *dryRun,
	}
	return runUpdateCommand(context.Background(), opts, out, osUpdateRunner{}, time.Now())
}

func runUpdateCommand(ctx context.Context, opts updateOptions, out io.Writer, runner updateCommandRunner, now time.Time) error {
	if err := opts.validate(); err != nil {
		return err
	}
	cfg, norm, err := loadAll(opts.ConfigPath, opts.RulesPath)
	if err != nil {
		return err
	}
	printRuleWarnings(out, norm.Warnings)
	if err := validateUpdatePlatform(); err != nil {
		return err
	}
	if err := validateInstalledBinary(opts.Binary); err != nil {
		return err
	}
	if !opts.DryRun {
		if err := requireRoot(); err != nil {
			return err
		}
	}
	rel, err := resolve5gwsRelease(ctx, defaultUpdateHTTPClient, defaultUpdateAPIBase, opts.Repo, opts.Version)
	if err != nil {
		return err
	}
	printUpdateRelease(out, opts, rel)
	backupPath := updateBackupPath(cfg, opts, now)
	if opts.DryRun {
		printUpdateDryRun(out, opts, cfg, rel, backupPath)
		return nil
	}
	tmp, err := os.MkdirTemp("", "5gws-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	candidate, err := downloadVerifiedCandidate(ctx, defaultUpdateHTTPClient, rel, tmp, out)
	if err != nil {
		return err
	}
	if err := checkCandidateVersion(candidate, rel.Version, runner); err != nil {
		return err
	}
	return executeVerifiedUpdate(opts, cfg, candidate, backupPath, out, runner)
}

func (o updateOptions) validate() error {
	if o.Repo == "" || strings.Count(o.Repo, "/") != 1 {
		return fmt.Errorf("repo must be owner/name, got %q", o.Repo)
	}
	if o.Binary == "" || !filepath.IsAbs(o.Binary) {
		return fmt.Errorf("binary path must be absolute, got %q", o.Binary)
	}
	if o.ConfigPath == "" || o.RulesPath == "" {
		return errors.New("config and rules paths are required")
	}
	return nil
}

func updateDefaultRepo() string {
	if repo := os.Getenv("FIVEGWS_REPO"); repo != "" {
		return repo
	}
	return defaultUpdateRepo
}

func validateUpdatePlatform() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("unsupported OS %s; update supports linux-amd64 release assets only", runtime.GOOS)
	}
	if runtime.GOARCH != "amd64" {
		return fmt.Errorf("unsupported architecture %s; update supports linux-amd64 release assets only", runtime.GOARCH)
	}
	return nil
}

func validateInstalledBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("installed binary %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("installed binary %s is a directory", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("installed binary %s is not executable", path)
	}
	return nil
}

func printUpdateRelease(out io.Writer, opts updateOptions, rel updateRelease) {
	fmt.Fprintf(out, "release: %s\n", rel.Tag)
	fmt.Fprintf(out, "asset: %s\n", rel.AssetURL)
	fmt.Fprintf(out, "checksum: %s\n", rel.ChecksumURL)
	fmt.Fprintf(out, "binary: %s\n", opts.Binary)
}

func printUpdateDryRun(out io.Writer, opts updateOptions, cfg config.Config, rel updateRelease, backupPath string) {
	fmt.Fprintf(out, "dry-run: would download %s\n", rel.AssetName)
	fmt.Fprintf(out, "dry-run: would verify %s\n", rel.ChecksumName)
	fmt.Fprintf(out, "dry-run: would backup %s to %s\n", opts.Binary, backupPath)
	fmt.Fprintf(out, "dry-run: would atomically replace %s\n", opts.Binary)
	fmt.Fprintf(out, "dry-run: would run %s apply --config %s --rules %s\n", opts.Binary, opts.ConfigPath, opts.RulesPath)
	fmt.Fprintf(out, "dry-run: would run %s doctor --config %s --rules %s\n", opts.Binary, opts.ConfigPath, opts.RulesPath)
	fmt.Fprintf(out, "dry-run: would check services: %s\n", strings.Join(activeServices(cfg), ", "))
}

func updateBackupPath(cfg config.Config, opts updateOptions, now time.Time) string {
	name := "update-" + now.UTC().Format("20060102-150405")
	return filepath.Join(cfg.System.StateDir, "backups", name, filepath.Base(opts.Binary))
}

func checkCandidateVersion(binary, want string, runner updateCommandRunner) error {
	data, err := runner.Output(binary, "version")
	if err != nil {
		return fmt.Errorf("candidate version check failed: %w: %s", err, strings.TrimSpace(string(data)))
	}
	got := strings.TrimSpace(string(data))
	if got != want {
		return fmt.Errorf("candidate version %q does not match release version %q", got, want)
	}
	return nil
}

func executeVerifiedUpdate(opts updateOptions, cfg config.Config, candidate, backupPath string, out io.Writer, runner updateCommandRunner) error {
	if err := backupInstalledBinary(opts.Binary, backupPath); err != nil {
		return err
	}
	fmt.Fprintf(out, "backup: %s\n", backupPath)
	if err := atomicReplaceFile(candidate, opts.Binary, 0o755); err != nil {
		return err
	}
	fmt.Fprintf(out, "installed: %s\n", opts.Binary)
	if err := runUpdateHealth(opts, cfg, out, runner); err != nil {
		fmt.Fprintf(out, "update health-check failed: %v\n", err)
		if rollbackErr := rollbackUpdate(opts, cfg, backupPath, out, runner); rollbackErr != nil {
			return fmt.Errorf("update failed: %w; rollback failed: %v", err, rollbackErr)
		}
		return fmt.Errorf("update failed: %w; rolled back to %s", err, backupPath)
	}
	fmt.Fprintln(out, "update: ok")
	return nil
}

func runUpdateHealth(opts updateOptions, cfg config.Config, out io.Writer, runner updateCommandRunner) error {
	if err := runner.Run(out, opts.Binary, "apply", "--config", opts.ConfigPath, "--rules", opts.RulesPath); err != nil {
		return fmt.Errorf("apply failed: %w", err)
	}
	if err := runner.Run(out, opts.Binary, "doctor", "--config", opts.ConfigPath, "--rules", opts.RulesPath); err != nil {
		return fmt.Errorf("doctor failed: %w", err)
	}
	for _, svc := range activeServices(cfg) {
		if err := runner.Run(out, "systemctl", "is-active", "--quiet", svc); err != nil {
			return fmt.Errorf("service %s is not active: %w", svc, err)
		}
	}
	return nil
}

func rollbackUpdate(opts updateOptions, cfg config.Config, backupPath string, out io.Writer, runner updateCommandRunner) error {
	mode := os.FileMode(0o755)
	if info, err := os.Stat(backupPath); err == nil {
		mode = info.Mode().Perm()
	}
	fmt.Fprintf(out, "rollback: restoring %s\n", backupPath)
	if err := atomicReplaceFile(backupPath, opts.Binary, mode); err != nil {
		return err
	}
	if err := runUpdateHealth(opts, cfg, out, runner); err != nil {
		return err
	}
	fmt.Fprintln(out, "rollback: ok")
	return nil
}

func backupInstalledBinary(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return copyFileWithMode(src, dst, info.Mode().Perm())
}

func atomicReplaceFile(src, dst string, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := copyOpenFileWithMode(src, tmp, mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, dst)
}

func copyFileWithMode(src, dst string, mode os.FileMode) error {
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if err := copyOpenFileWithMode(src, file, mode); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func copyOpenFileWithMode(src string, dst *os.File, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if _, err := io.Copy(dst, in); err != nil {
		return err
	}
	return dst.Chmod(mode)
}
