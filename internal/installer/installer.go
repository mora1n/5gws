package installer

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/morain/5gws/internal/checksum"
	"github.com/morain/5gws/internal/config"
)

const (
	DefaultSmartDNSVersion = "v0.13.0"
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
	asset, err := smartDNSAsset(version)
	if err != nil {
		return err
	}
	spec := installSpec{
		Name:      "smartdns-rs",
		Binary:    "smartdns",
		Repo:      "mokeyish/smartdns-rs",
		Version:   version,
		Asset:     asset,
		Checksum:  asset + "-sha256sum.txt",
		TarArgs:   []string{"-xzf"},
		Installed: []string{"smartdns"},
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
	Name      string
	Binary    string
	Repo      string
	Version   string
	Asset     string
	Checksum  string
	TarArgs   []string
	Installed []string
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
	if path, err := exec.LookPath(spec.Binary); err == nil {
		fmt.Fprintf(out, "%s: already installed at %s\n", spec.Name, path)
		return nil
	}
	if !opts.Yes && !opts.DryRun {
		return fmt.Errorf("%s install requires --yes", spec.Name)
	}
	assetURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", spec.Repo, spec.Version, spec.Asset)
	checksumURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", spec.Repo, spec.Version, spec.Checksum)
	if opts.DryRun {
		fmt.Fprintf(out, "dry-run: would download %s\n", assetURL)
		fmt.Fprintf(out, "dry-run: would verify %s\n", checksumURL)
		fmt.Fprintf(out, "dry-run: would install %s to %s\n", strings.Join(spec.Installed, ", "), installDir)
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
		if err := run(out, tmp, "install", "-m", "755", src, filepath.Join(installDir, name)); err != nil {
			return err
		}
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
