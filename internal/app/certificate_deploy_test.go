package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morain/5gws/internal/config"
)

func certificateFixture(t *testing.T, domain string, expired bool) ([]byte, []byte) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{domain}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	if expired {
		template.NotBefore = now.Add(-2 * time.Hour)
		template.NotAfter = now.Add(-time.Hour)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func TestCertificateDeploymentRejectsInvalidPairBeforeWriting(t *testing.T) {
	cert, key := certificateFixture(t, "dot.example.com", false)
	_, otherKey := certificateFixture(t, "dot.example.com", false)
	expired, expiredKey := certificateFixture(t, "dot.example.com", true)
	if err := validateCertificatePair(cert, key, "dot.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		cert, key []byte
		domain    string
	}{
		{"mismatched key", cert, otherKey, "dot.example.com"},
		{"wrong domain", cert, key, "wrong.example.com"},
		{"expired", expired, expiredKey, "dot.example.com"},
		{"malformed", []byte("invalid certificate"), key, "dot.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			lineage := filepath.Join(root, "lineage")
			if err := os.Mkdir(lineage, 0o700); err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{DNS: config.DNSConfig{DOTDomain: tc.domain, CertFile: filepath.Join(root, "cert.pem"), KeyFile: filepath.Join(root, "key.pem")}}
			for path, data := range map[string][]byte{filepath.Join(lineage, "fullchain.pem"): tc.cert, filepath.Join(lineage, "privkey.pem"): tc.key, cfg.DNS.CertFile: []byte("old certificate"), cfg.DNS.KeyFile: []byte("old key")} {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := deployCertificateFiles(cfg, lineage); err == nil {
				t.Fatal("invalid certificate accepted")
			}
			for path, want := range map[string]string{cfg.DNS.CertFile: "old certificate", cfg.DNS.KeyFile: "old key"} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("existing certificate changed: %s, %v", path, err)
				}
			}
		})
	}
}

func TestCertificateHookQuotesDatabasePath(t *testing.T) {
	cfg := config.Config{System: config.SystemConfig{StateDir: "/tmp/state with 'quotes' $(printf unexpected)"}}
	script := strings.Replace(certificateHook(cfg), "exec /usr/local/bin/5gws", "printf '<%s>\\n'", 1)
	cmd := exec.Command("sh", "-s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook failed: %v: %s", err, out)
	}
	want := "<deploy-certificate>\n<--database>\n<" + filepath.Join(cfg.System.StateDir, "5gws.db") + ">\n"
	if string(out) != want {
		t.Fatalf("hook changed shell argument: %q, want %q", out, want)
	}
}
