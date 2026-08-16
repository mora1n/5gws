package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
)

func TestInstallSmartDNSSkipsCurrentVersion(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "smartdns"), "#!/bin/sh\necho 'smartdns 0.13.1'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	if err := InstallSmartDNS(Options{}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "version v0.13.1 already installed") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestEnsureRuntimeDryRunIncludesSSRustWithoutConfiguredExit(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"haproxy", "nft", "smartdns"} {
		writeExecutable(t, filepath.Join(dir, name), "#!/bin/sh\n")
	}
	t.Setenv("PATH", dir)

	var out bytes.Buffer
	if err := EnsureRuntime(config.Config{DNS: config.DNSConfig{Binary: "smartdns"}}, true, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"shadowsocks-rust/releases/download/v1.24.0/", "dry-run: would install sslocal"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q does not contain %q", out.String(), want)
		}
	}
}

func TestInstallSmartDNSDryRunReplacesDifferentVersion(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "smartdns"), "#!/bin/sh\necho 'smartdns 0.13.0'\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out bytes.Buffer
	if err := InstallSmartDNS(Options{DryRun: true}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"replacing v0.13.0 with v0.13.1", "smartdns-rs/releases/download/v0.13.1/"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q does not contain %q", out.String(), want)
		}
	}
}

func TestInstalledSmartDNSVersionRejectsUnexpectedOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smartdns")
	writeExecutable(t, path, "#!/bin/sh\necho unknown\n")
	if _, err := installedSmartDNSVersion(path); err == nil {
		t.Fatal("expected unexpected version output to fail")
	}
}

func TestInstallFileAtomicReplacesDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "smartdns")
	writeTestFile(t, dir, "source", "new-binary")
	writeTestFile(t, dir, "smartdns", "old-binary")
	if err := installFileAtomic(source, destination); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, dir, "smartdns"); got != "new-binary" {
		t.Fatalf("destination = %q", got)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("destination mode = %o, want 755", info.Mode().Perm())
	}
}

func TestInstallFileAtomicPreservesDestinationOnFailure(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "smartdns")
	writeTestFile(t, dir, "smartdns", "old-binary")
	if err := installFileAtomic(filepath.Join(dir, "missing"), destination); err == nil {
		t.Fatal("expected missing source to fail")
	}
	if got := readTestFile(t, dir, "smartdns"); got != "old-binary" {
		t.Fatalf("destination changed to %q", got)
	}
}

func TestPrepareChecksumFileNormalizesBareSHA256(t *testing.T) {
	dir := t.TempDir()
	checksum := "asset.tar.gz-sha256sum.txt"
	bare := "FC9FEF3687E66108351B1E5E7D54A7DF0EA394FDE1EE7F20127D71FCBAFE9E37\n"
	writeTestFile(t, dir, checksum, bare)

	if err := prepareChecksumFile(dir, checksum, "asset.tar.gz"); err != nil {
		t.Fatal(err)
	}

	got := readTestFile(t, dir, checksum)
	want := "FC9FEF3687E66108351B1E5E7D54A7DF0EA394FDE1EE7F20127D71FCBAFE9E37  asset.tar.gz\n"
	if got != want {
		t.Fatalf("checksum file mismatch\nwant: %q\n got: %q", want, got)
	}
}

func TestPrepareChecksumFileKeepsFormattedChecksum(t *testing.T) {
	dir := t.TempDir()
	checksum := "asset.tar.gz.sha256"
	formatted := "fc9fef3687e66108351b1e5e7d54a7df0ea394fde1ee7f20127d71fcbafe9e37  asset.tar.gz\n"
	writeTestFile(t, dir, checksum, formatted)

	if err := prepareChecksumFile(dir, checksum, "asset.tar.gz"); err != nil {
		t.Fatal(err)
	}

	if got := readTestFile(t, dir, checksum); got != formatted {
		t.Fatalf("checksum file should not be rewritten\nwant: %q\n got: %q", formatted, got)
	}
}

func TestPrepareChecksumFileRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	checksum := "asset.tar.gz.sha256"
	writeTestFile(t, dir, checksum, "\n")

	if err := prepareChecksumFile(dir, checksum, "asset.tar.gz"); err == nil {
		t.Fatal("expected error for empty checksum file")
	}
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
