package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/morain/5gws/internal/config"
)

func TestResolve5gwsReleaseLatestFindsAssetAndChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/mora1n/5gws/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"tag_name":"v0.1.9",
			"assets":[
				{"name":"5gws-linux-amd64-0.1.9.tar.gz","browser_download_url":"https://example.com/5gws.tar.gz"},
				{"name":"5gws-linux-amd64-0.1.9.tar.gz.sha256","browser_download_url":"https://example.com/5gws.tar.gz.sha256"}
			]
		}`)
	}))
	defer server.Close()

	got, err := resolve5gwsRelease(context.Background(), server.Client(), server.URL, "mora1n/5gws", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tag != "v0.1.9" || got.Version != "0.1.9" || got.AssetName != "5gws-linux-amd64-0.1.9.tar.gz" {
		t.Fatalf("release = %#v", got)
	}
	if got.ChecksumName != "5gws-linux-amd64-0.1.9.tar.gz.sha256" {
		t.Fatalf("checksum = %q", got.ChecksumName)
	}
}

func TestResolve5gwsReleaseRequiresChecksum(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"tag_name":"v0.1.9",
			"assets":[{"name":"5gws-linux-amd64-0.1.9.tar.gz","browser_download_url":"https://example.com/5gws.tar.gz"}]
		}`)
	}))
	defer server.Close()

	_, err := resolve5gwsRelease(context.Background(), server.Client(), server.URL, "mora1n/5gws", "")
	if err == nil || !strings.Contains(err.Error(), ".sha256") {
		t.Fatalf("expected missing checksum error, got %v", err)
	}
}

func TestExtractReleaseBinaryRequiresSafeExecutable5gws(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	writeTarGz(t, archive, []tarEntry{
		{name: "5gws", mode: 0o755, body: "#!/bin/sh\necho 0.1.9\n"},
		{name: "config.example.toml", mode: 0o644, body: ""},
		{name: "rules.example.toml", mode: 0o644, body: ""},
	})

	path, err := extractReleaseBinary(archive, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "echo 0.1.9") {
		t.Fatalf("extracted binary mismatch:\n%s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("extracted binary is not executable: %v", info.Mode())
	}
}

func TestExtractReleaseBinaryRejectsUnsafePath(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "release.tar.gz")
	writeTarGz(t, archive, []tarEntry{{name: "../5gws", mode: 0o755, body: "bad"}})

	if _, err := extractReleaseBinary(archive, dir); err == nil {
		t.Fatal("expected unsafe path error")
	}
}

func TestVerifyArchiveChecksumRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "5gws-linux-amd64-0.1.9.tar.gz")
	checksum := archive + ".sha256"
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := strings.Repeat("0", 64) + "  " + filepath.Base(archive) + "\n"
	if err := os.WriteFile(checksum, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	err := verifyArchiveChecksum(archive, checksum, filepath.Base(archive))
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected mismatch error, got %v", err)
	}
}

func TestExecuteVerifiedUpdateReplacesBinaryAndChecksHealth(t *testing.T) {
	dir := t.TempDir()
	target := writeExecutable(t, filepath.Join(dir, "5gws"), "old")
	candidate := writeExecutable(t, filepath.Join(dir, "candidate"), "new")
	cfg := config.Config{System: config.SystemConfig{StateDir: filepath.Join(dir, "state")}}
	opts := updateOptions{Binary: target, ConfigPath: "/etc/5gws/config.toml", RulesPath: "/etc/5gws/rules.toml"}
	backupPath := updateBackupPath(cfg, opts, fixedUpdateTime())
	runner := &fakeUpdateRunner{version: "0.1.9"}
	var out bytes.Buffer

	if err := executeVerifiedUpdate(opts, cfg, candidate, backupPath, &out, runner); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, target); got != "new" {
		t.Fatalf("target = %q, want new", got)
	}
	if got := readFile(t, backupPath); got != "old" {
		t.Fatalf("backup = %q, want old", got)
	}
	for _, want := range []string{
		target + " apply --config /etc/5gws/config.toml --rules /etc/5gws/rules.toml",
		target + " doctor --config /etc/5gws/config.toml --rules /etc/5gws/rules.toml",
		"systemctl is-active --quiet 5gws-smartdns.service",
	} {
		if !runner.hasCall(want) {
			t.Fatalf("missing call %q in %#v", want, runner.calls)
		}
	}
}

func TestExecuteVerifiedUpdateRollsBackAfterHealthFailure(t *testing.T) {
	dir := t.TempDir()
	target := writeExecutable(t, filepath.Join(dir, "5gws"), "old")
	candidate := writeExecutable(t, filepath.Join(dir, "candidate"), "new")
	cfg := config.Config{System: config.SystemConfig{StateDir: filepath.Join(dir, "state")}}
	opts := updateOptions{Binary: target, ConfigPath: "/etc/5gws/config.toml", RulesPath: "/etc/5gws/rules.toml"}
	backupPath := updateBackupPath(cfg, opts, fixedUpdateTime())
	runner := &fakeUpdateRunner{version: "0.1.9", failNextServiceCheck: true}
	var out bytes.Buffer

	err := executeVerifiedUpdate(opts, cfg, candidate, backupPath, &out, runner)
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected rolled back error, got %v", err)
	}
	if got := readFile(t, target); got != "old" {
		t.Fatalf("target = %q, want old after rollback", got)
	}
	if !strings.Contains(out.String(), "rollback: ok") {
		t.Fatalf("missing rollback success output:\n%s", out.String())
	}
}

func TestCheckCandidateVersionRequiresReleaseVersion(t *testing.T) {
	runner := &fakeUpdateRunner{version: "0.1.8"}
	err := checkCandidateVersion("/tmp/5gws", "0.1.9", runner)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected version mismatch, got %v", err)
	}
}

type fakeUpdateRunner struct {
	version              string
	failNextServiceCheck bool
	calls                []string
}

func (r *fakeUpdateRunner) Run(_ io.Writer, name string, args ...string) error {
	call := strings.TrimSpace(name + " " + strings.Join(args, " "))
	r.calls = append(r.calls, call)
	if r.failNextServiceCheck && name == "systemctl" {
		r.failNextServiceCheck = false
		return errors.New("inactive")
	}
	return nil
}

func (r *fakeUpdateRunner) Output(name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return []byte(r.version + "\n"), nil
}

func (r *fakeUpdateRunner) hasCall(want string) bool {
	for _, call := range r.calls {
		if call == want {
			return true
		}
	}
	return false
}

func fixedUpdateTime() time.Time {
	return time.Date(2026, 7, 3, 12, 34, 56, 0, time.UTC)
}

type tarEntry struct {
	name string
	mode int64
	body string
}

func writeTarGz(t *testing.T, path string, entries []tarEntry) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		hdr := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeExecutable(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
