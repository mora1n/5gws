package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/store"
)

func runDeployCertificate(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("deploy-certificate", flag.ContinueOnError)
	database := flags.String("database", "/var/lib/5gws/5gws.db", "active configuration database")
	lineage := flags.String("lineage", os.Getenv("RENEWED_LINEAGE"), "renewed certificate directory")
	installHook := flags.Bool("install-hook", false, "install the Certbot deploy hook only")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return errors.New("certificate deployment must run as root")
	}
	cfg, err := certificateConfig(*database)
	if err != nil {
		return err
	}
	if *installHook {
		return ensureCertificateDeployHook(cfg, out)
	}
	expected := filepath.Join("/etc/letsencrypt/live", cfg.DNS.DOTDomain)
	if *lineage == "" {
		return errors.New("--lineage or RENEWED_LINEAGE is required")
	}
	if filepath.Clean(*lineage) != expected {
		fmt.Fprintf(out, "Skipping unrelated certificate lineage %s; expected %s\n", *lineage, expected)
		return nil
	}
	if err := deployCertificateFiles(cfg, *lineage); err != nil {
		return err
	}
	return reloadCertificateServices(out)
}

func certificateConfig(database string) (config.Config, error) {
	state, err := store.Open(database)
	if err != nil {
		return config.Config{}, err
	}
	defer state.Close()
	active, err := state.Active(context.Background())
	if err != nil {
		return config.Config{}, err
	}
	return active.Bundle.Config, active.Bundle.Config.Validate()
}

func reloadCertificateServices(out io.Writer) error {
	if _, err := exec.LookPath("nginx"); err == nil {
		if err := command(out, "nginx", "-t"); err != nil {
			return err
		}
		if err := command(out, "systemctl", "reload", "nginx.service"); err != nil {
			return err
		}
	} else if !errors.Is(err, exec.ErrNotFound) {
		return err
	} else {
		fmt.Fprintln(out, "Nginx is not installed; deploying the DoT certificate only")
	}
	return command(out, "systemctl", "restart", "5gws.service")
}

func certificateHookPath(domain string) string {
	return filepath.Join("/etc/letsencrypt/renewal-hooks/deploy", "5gws-"+domain)
}

func certificateHook(cfg config.Config) string {
	database := filepath.Join(cfg.System.StateDir, "5gws.db")
	quoted := "'" + strings.ReplaceAll(database, "'", "'\\''") + "'"
	return "#!/bin/sh\nset -eu\nexec /usr/local/bin/5gws deploy-certificate --database " + quoted + "\n"
}

func ensureCertificateDeployHook(cfg config.Config, out io.Writer) error {
	path := certificateHookPath(cfg.DNS.DOTDomain)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(certificateHook(cfg)), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	fmt.Fprintf(out, "Certbot deploy hook installed: %s\n", path)
	return nil
}

func deployCertificateFiles(cfg config.Config, lineage string) error {
	cert, err := os.ReadFile(filepath.Join(lineage, "fullchain.pem"))
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(lineage, "privkey.pem"))
	if err != nil {
		return err
	}
	if err := validateCertificatePair(cert, key, cfg.DNS.DOTDomain); err != nil {
		return err
	}
	group, err := smartDNSGroup()
	if err != nil {
		return err
	}
	for _, file := range []struct {
		path string
		data []byte
	}{{cfg.DNS.CertFile, cert}, {cfg.DNS.KeyFile, key}} {
		if err := writeCertificateFile(file.path, file.data, group); err != nil {
			return err
		}
	}
	return allowSmartDNSCertificateRead(cfg)
}

func validateCertificatePair(cert, key []byte, domain string) error {
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return fmt.Errorf("certificate and private key: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return err
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return fmt.Errorf("certificate for %s is outside its validity period", domain)
	}
	return nil
}

func writeCertificateFile(path string, data []byte, group int) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".5gws-certificate-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o640); err != nil {
		return err
	}
	if err := file.Chown(0, group); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func removeCertificateDeployHook(stateDir string) error {
	bundle, err := loadExistingBundle(stateDir)
	if err != nil || bundle == nil {
		return err
	}
	if err := bundle.Config.Validate(); err != nil {
		return err
	}
	err = os.Remove(certificateHookPath(bundle.Config.DNS.DOTDomain))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
