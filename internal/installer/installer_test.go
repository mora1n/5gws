package installer

import (
	"os"
	"path/filepath"
	"testing"
)

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
