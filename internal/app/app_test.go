package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpAndUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if err := Run(nil, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"install", "status", "rollback", "export"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q", want)
		}
	}
	if err := Run([]string{"bot"}, strings.NewReader(""), &out, &out); err == nil {
		t.Fatal("removed bot command was accepted")
	}
}

func TestRefuseLegacyInstallWithExistingDatabase(t *testing.T) {
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "5gws.db"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refuseLegacyInstall(stateDir); err == nil || !strings.Contains(err.Error(), "fresh installation only") {
		t.Fatalf("expected explicit fresh-install rejection, got %v", err)
	}
}

func TestInstallUsesOneServiceUnit(t *testing.T) {
	if strings.Count(systemdUnit, "[Service]") != 1 || !strings.Contains(systemdUnit, "5gws daemon") {
		t.Fatalf("invalid daemon unit:\n%s", systemdUnit)
	}
	for _, removed := range []string{"5gws-smartdns.service", "5gws-haproxy.service", "5gws-quic.service", "5gws-bot.service"} {
		if strings.Contains(systemdUnit, removed) {
			t.Fatalf("unit references removed child service %q", removed)
		}
	}
}
