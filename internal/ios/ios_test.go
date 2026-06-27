package ios

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/morain/5gws/internal/config"
)

func TestGenerateWritesReadableServerKeyForSmartDNS(t *testing.T) {
	dir := t.TempDir()
	writeExistingFile(t, filepath.Join(dir, "privkey.pem"), 0o600)
	cfg := config.Config{
		Network: config.NetworkConfig{
			GatewayIP: "10.0.0.1",
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

	assertFileMode(t, filepath.Join(dir, "5gws-ca.key"), 0o600)
	assertFileMode(t, filepath.Join(dir, "privkey.pem"), 0o644)
}

func writeExistingFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("old"), mode); err != nil {
		t.Fatal(err)
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
