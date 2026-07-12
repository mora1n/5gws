package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPruneRevisionDirsKeepsReferencedDirectories(t *testing.T) {
	stateDir := t.TempDir()
	for _, name := range []string{"1", "2", "staging"} {
		if err := os.MkdirAll(filepath.Join(stateDir, "revisions", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := PruneRevisionDirs(stateDir, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "revisions", "1")); !os.IsNotExist(err) {
		t.Fatalf("revision 1 still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "revisions", "2")); err != nil {
		t.Fatalf("revision 2 removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "revisions", "staging")); err != nil {
		t.Fatalf("non-revision directory removed: %v", err)
	}
}

func TestPrunePreflightDirs(t *testing.T) {
	stateDir := t.TempDir()
	stale := filepath.Join(stateDir, "preflight-stale")
	keep := filepath.Join(stateDir, "other")
	for _, path := range []string{stale, keep} {
		if err := os.MkdirAll(path, 0o700); err != nil { t.Fatal(err) }
	}
	if err := PrunePreflightDirs(stateDir); err != nil { t.Fatal(err) }
	if _, err := os.Stat(stale); !os.IsNotExist(err) { t.Fatalf("stale preflight remains: %v", err) }
	if _, err := os.Stat(keep); err != nil { t.Fatalf("unrelated directory removed: %v", err) }
}
