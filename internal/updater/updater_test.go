package updater

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVerifiedApplyAndPendingRollback(t *testing.T) {
	candidate := []byte("#!/bin/sh\necho 2.0.0\n")
	sum := sha256.Sum256(candidate)
	var base string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/repo/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v2.0.0","assets":[{"name":"5gws-linux-%s","browser_download_url":"%s/bin"},{"name":"5gws-linux-%s.sha256","browser_download_url":"%s/sum"}]}`, runtime.GOARCH, base, runtime.GOARCH, base)
		case "/bin":
			_, _ = w.Write(candidate)
		case "/sum":
			fmt.Fprintf(w, "%x  5gws-linux-%s\n", sum, runtime.GOARCH)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base = server.URL
	dir := t.TempDir()
	binary := filepath.Join(dir, "5gws")
	if err := os.WriteFile(binary, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTP: server.Client(), Repository: "test/repo", APIBase: server.URL}
	info, err := client.Apply(context.Background(), "1.0.0", binary, dir)
	if err != nil || info.Latest != "2.0.0" {
		t.Fatalf("apply: %+v %v", info, err)
	}
	if data, _ := os.ReadFile(binary); string(data) != string(candidate) {
		t.Fatalf("candidate not installed: %q", data)
	}
	if err := RollbackPending(dir); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(binary); string(data) != "old" {
		t.Fatalf("rollback = %q", data)
	}
}
