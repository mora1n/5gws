package rules

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type memoryCache struct {
	mu      sync.Mutex
	entries map[string]CacheEntry
}

func (c *memoryCache) GetImportCache(_ context.Context, url string) (CacheEntry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[url]
	return entry, ok, nil
}

func (c *memoryCache) PutImportCache(_ context.Context, entry CacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[entry.URL] = entry
	return nil
}

func TestResolverUsesConditionalCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(`{"version":2,"rules":[{"domain_suffix":["example.com"]}]}`))
	}))
	defer server.Close()
	cache := &memoryCache{entries: map[string]CacheEntry{}}
	resolver := Resolver{Cache: cache, Client: server.Client()}
	file := File{Imports: []Import{{Name: "one", Type: "sing-box", URL: server.URL, Exit: "direct"}}}
	for i := 0; i < 2; i++ {
		norm, err := resolver.Normalize(context.Background(), file)
		if err != nil || len(norm.Rules) != 1 {
			t.Fatalf("normalize #%d: rules=%d err=%v", i, len(norm.Rules), err)
		}
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestResolverFetchesImportsConcurrentlyInDeclaredOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(120 * time.Millisecond)
		}
		_, _ = w.Write([]byte(`{"version":2,"rules":[{"domain_suffix":["` + r.URL.Path[1:] + `.example"]}]}`))
	}))
	defer server.Close()
	file := File{Imports: []Import{
		{Name: "slow", Type: "sing-box", URL: server.URL + "/slow", Exit: "direct"},
		{Name: "fast", Type: "sing-box", URL: server.URL + "/fast", Exit: "direct"},
	}}
	started := time.Now()
	norm, err := (Resolver{Client: server.Client()}).Normalize(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 220*time.Millisecond {
		t.Fatalf("imports did not run concurrently: %s", elapsed)
	}
	if norm.Rules[0].Name != "slow-1" || norm.Rules[1].Name != "fast-1" {
		t.Fatalf("import order changed: %q, %q", norm.Rules[0].Name, norm.Rules[1].Name)
	}
}
