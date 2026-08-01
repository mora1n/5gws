package installer

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/morain/5gws/internal/checksum"
	"github.com/morain/5gws/internal/config"
)

const (
	DefaultSmartDNSVersion = "v0.13.1"
	DefaultSSRustVersion   = "v1.24.0"
	installDir             = "/usr/local/bin"
)

type Options struct {
	DryRun  bool
	Yes     bool
	Version string
}

func EnsureRuntime(cfg config.Config, dryRun bool, out io.Writer) error {
	if err := ensureSystemPackages(dryRun, out); err != nil {
		return err
	}
	if err := ensureSmartDNS(cfg, dryRun, out); err != nil {
		return err
	}
	if hasSSRustExit(cfg) {
		return ensureSSRust(dryRun, out)
	}
	return nil
}

func ensureSystemPackages(dryRun bool, out io.Writer) error {
	var missing []string
	for _, name := range []string{"haproxy", "nft"} {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if dryRun {
		fmt.Fprintf(out, "dry-run: would install system packages for %s\n", strings.Join(missing, ", "))
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("installing %s requires root", strings.Join(missing, ", "))
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return fmt.Errorf("missing required runtime commands %s and apt-get is unavailable", strings.Join(missing, ", "))
	}
	if err := run(out, "", "apt-get", "update"); err != nil {
		return err
	}
	return run(out, "", "apt-get", "install", "-y", "haproxy", "nftables")
}

func InstallSmartDNS(opts Options, out io.Writer) error {
	version := versionOr(opts.Version, DefaultSmartDNSVersion)
	replaceExisting := false
	destinationDir := installDir
	if path, err := exec.LookPath("smartdns"); err == nil {
		installedVersion, err := installedSmartDNSVersion(path)
		if err != nil {
			return err
		}
		if installedVersion == version {
			fmt.Fprintf(out, "smartdns-rs: version %s already installed at %s\n", version, path)
			return nil
		}
		fmt.Fprintf(out, "smartdns-rs: replacing %s with %s at %s\n", installedVersion, version, path)
		replaceExisting = true
		destinationDir = filepath.Dir(path)
	}
	asset, err := smartDNSAsset(version)
	if err != nil {
		return err
	}
	spec := installSpec{
		Name:            "smartdns-rs",
		Binary:          "smartdns",
		Repo:            "mokeyish/smartdns-rs",
		Version:         version,
		Asset:           asset,
		Checksum:        asset + "-sha256sum.txt",
		TarArgs:         []string{"-xzf"},
		Installed:       []string{"smartdns"},
		ReplaceExisting: replaceExisting,
		DestinationDir:  destinationDir,
	}
	return installArchive(spec, opts, out)
}

func InstallSSRust(opts Options, out io.Writer) error {
	version := versionOr(opts.Version, DefaultSSRustVersion)
	asset, err := ssRustAsset(version)
	if err != nil {
		return err
	}
	spec := installSpec{
		Name:      "shadowsocks-rust",
		Binary:    "sslocal",
		Repo:      "shadowsocks/shadowsocks-rust",
		Version:   version,
		Asset:     asset,
		Checksum:  asset + ".sha256",
		TarArgs:   []string{"-xJf"},
		Installed: []string{"sslocal", "ssserver", "ssmanager", "ssservice", "ssurl"},
	}
	return installArchive(spec, opts, out)
}

type installSpec struct {
	Name            string
	Binary          string
	Repo            string
	Version         string
	Asset           string
	Checksum        string
	TarArgs         []string
	Installed       []string
	ReplaceExisting bool
	DestinationDir  string
}

func ensureSmartDNS(cfg config.Config, dryRun bool, out io.Writer) error {
	if path, err := exec.LookPath(cfg.DNS.Binary); err == nil {
		fmt.Fprintf(out, "smartdns-rs: %s\n", path)
		return nil
	}
	fmt.Fprintf(out, "smartdns-rs: missing (%s)\n", cfg.DNS.Binary)
	return InstallSmartDNS(Options{DryRun: dryRun, Yes: true}, out)
}

func ensureSSRust(dryRun bool, out io.Writer) error {
	if path, err := exec.LookPath("sslocal"); err == nil {
		fmt.Fprintf(out, "shadowsocks-rust: %s\n", path)
		return nil
	}
	fmt.Fprintln(out, "shadowsocks-rust: missing (sslocal)")
	return InstallSSRust(Options{DryRun: dryRun, Yes: true}, out)
}

func hasSSRustExit(cfg config.Config) bool {
	for _, exit := range cfg.Exits {
		if exit.Type == "shadowsocks-rust" {
			return true
		}
	}
	return false
}

func installArchive(spec installSpec, opts Options, out io.Writer) error {
	if path, err := exec.LookPath(spec.Binary); err == nil && !spec.ReplaceExisting {
		fmt.Fprintf(out, "%s: already installed at %s\n", spec.Name, path)
		return nil
	}
	if !opts.Yes && !opts.DryRun {
		return fmt.Errorf("%s install requires --yes", spec.Name)
	}
	assetURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", spec.Repo, spec.Version, spec.Asset)
	checksumURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", spec.Repo, spec.Version, spec.Checksum)
	destinationDir := spec.DestinationDir
	if destinationDir == "" {
		destinationDir = installDir
	}
	if opts.DryRun {
		fmt.Fprintf(out, "dry-run: would download %s\n", assetURL)
		fmt.Fprintf(out, "dry-run: would verify %s\n", checksumURL)
		fmt.Fprintf(out, "dry-run: would install %s to %s\n", strings.Join(spec.Installed, ", "), destinationDir)
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("%s install must run as root; use --dry-run for validation", spec.Name)
	}
	tmp, err := os.MkdirTemp("", "5gws-install-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := run(out, tmp, "curl", "-fL", "-o", spec.Asset, assetURL); err != nil {
		return err
	}
	if err := run(out, tmp, "curl", "-fL", "-o", spec.Checksum, checksumURL); err != nil {
		return err
	}
	if err := prepareChecksumFile(tmp, spec.Checksum, spec.Asset); err != nil {
		return err
	}
	if err := run(out, tmp, "sha256sum", "-c", spec.Checksum); err != nil {
		return err
	}
	tarArgs := append(append([]string{}, spec.TarArgs...), spec.Asset)
	if err := run(out, tmp, "tar", tarArgs...); err != nil {
		return err
	}
	for _, name := range spec.Installed {
		src, err := findFile(tmp, name)
		if err != nil {
			return err
		}
		if err := installFileAtomic(src, filepath.Join(destinationDir, name)); err != nil {
			return err
		}
	}
	return nil
}

var smartDNSVersionPattern = regexp.MustCompile(`\bv?(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\b`)

func installedSmartDNSVersion(path string) (string, error) {
	data, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("inspect installed smartdns-rs version: %w: %s", err, strings.TrimSpace(string(data)))
	}
	match := smartDNSVersionPattern.FindStringSubmatch(string(data))
	if len(match) != 2 {
		return "", fmt.Errorf("inspect installed smartdns-rs version: unexpected output %q", strings.TrimSpace(string(data)))
	}
	return "v" + match[1], nil
}

func installFileAtomic(src, destination string) error {
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open install source %s: %w", src, err)
	}
	defer source.Close()
	temporary, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+"-5gws-*")
	if err != nil {
		return fmt.Errorf("create temporary install file for %s: %w", destination, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o755); err != nil {
		temporary.Close()
		return fmt.Errorf("set mode on temporary install file: %w", err)
	}
	if _, err := io.Copy(temporary, source); err != nil {
		temporary.Close()
		return fmt.Errorf("copy %s to temporary install file: %w", src, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary install file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary install file: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("replace %s atomically: %w", destination, err)
	}
	return nil
}

func smartDNSAsset(version string) (string, error) {
	arch, err := releaseArch()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("smartdns-%s-unknown-linux-gnu-%s.tar.gz", arch, version), nil
}

func ssRustAsset(version string) (string, error) {
	arch, err := releaseArch()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("shadowsocks-%s.%s-unknown-linux-gnu.tar.xz", version, arch), nil
}

func releaseArch() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64", nil
	case "arm64":
		return "aarch64", nil
	default:
		return "", fmt.Errorf("unsupported architecture %q", runtime.GOARCH)
	}
}

func versionOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	if strings.HasPrefix(value, "v") {
		return value
	}
	return "v" + value
}

func findFile(root, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("archive did not contain %s", name)
	}
	return found, nil
}

func prepareChecksumFile(dir, checksumFile, asset string) error {
	return checksum.NormalizeBareSHA256File(filepath.Join(dir, checksumFile), asset)
}

func run(out io.Writer, dir, name string, args ...string) error {
	fmt.Fprintf(out, "+ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	data, err := cmd.CombinedOutput()
	if len(data) > 0 {
		fmt.Fprint(out, string(data))
	}
	return err
}
