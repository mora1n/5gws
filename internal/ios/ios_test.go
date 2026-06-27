package ios

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
)

func TestGenerateWritesDOTProfileOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		Network: config.NetworkConfig{
			GatewayIP: "10.0.0.1",
		},
		DNS: config.DNSConfig{
			DOTDomain: "dot.example.com",
		},
		IOS: config.IOSConfig{
			BaseURL:           "http://10.0.0.1:8088",
			Organization:      "5gws",
			ProfileIdentifier: "dev.5gws.dot",
		},
	}

	if _, err := Generate(dir, cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "5gws-dot.mobileconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<string>dot.example.com</string>") {
		t.Fatalf("profile does not use DoT domain:\n%s", string(data))
	}
	assertFileMode(t, filepath.Join(dir, "5gws-dot.mobileconfig"), 0o644)
	if _, err := os.Stat(filepath.Join(dir, "5gws-ca.key")); !os.IsNotExist(err) {
		t.Fatalf("Generate should not write local CA key, err=%v", err)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode mismatch: want %o, got %o", path, want, got)
	}
}
