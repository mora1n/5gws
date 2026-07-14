package rules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type CacheEntry struct {
	URL          string
	ETag         string
	LastModified string
	SHA256       string
	Content      []byte
}

type ImportCache interface {
	GetImportCache(context.Context, string) (CacheEntry, bool, error)
	PutImportCache(context.Context, CacheEntry) error
}

type Resolver struct {
	Client *http.Client
	Cache  ImportCache
}

type importResult struct {
	index    int
	rules    []Rule
	warnings []Warning
	err      error
}

func (r Resolver) Normalize(ctx context.Context, file File) (Normalized, error) {
	out := make([]Rule, 0, len(file.Rules))
	for _, rule := range file.Rules {
		if err := validateRule(rule); err != nil {
			return Normalized{}, err
		}
		out = append(out, rule)
	}
	results := make(chan importResult, len(file.Imports))
	for i, item := range file.Imports {
		go func(index int, imp Import) {
			rules, warnings, err := r.resolveImport(ctx, imp)
			results <- importResult{index: index, rules: rules, warnings: warnings, err: err}
		}(i, item)
	}
	ordered := make([]importResult, len(file.Imports))
	for range file.Imports {
		result := <-results
		ordered[result.index] = result
	}
	var warnings []Warning
	for i, result := range ordered {
		if result.err != nil {
			return Normalized{}, fmt.Errorf("import %q: %w", file.Imports[i].Name, result.err)
		}
		out = append(out, result.rules...)
		warnings = append(warnings, result.warnings...)
	}
	return Normalized{Rules: out, Warnings: warnings}, nil
}

func (r Resolver) resolveImport(ctx context.Context, imp Import) ([]Rule, []Warning, error) {
	if imp.Name == "" || imp.Type == "" {
		return nil, nil, errors.New("import name and type are required")
	}
	if (imp.Exit == "") == (imp.DNSPool == "") {
		return nil, nil, errors.New("exactly one of exit or dns_pool is required")
	}
	if imp.DNSPool != "" && !validDNSPoolName(imp.DNSPool) {
		return nil, nil, fmt.Errorf("invalid dns_pool %q", imp.DNSPool)
	}
	data, err := r.read(ctx, imp)
	if err != nil {
		return nil, nil, err
	}
	switch imp.Type {
	case "sing-box", "singbox":
		return parseSingBox(imp, data)
	case "mihomo", "clash", "clash-meta", "mimoho":
		return parseMihomo(imp, data)
	default:
		return nil, nil, fmt.Errorf("unsupported type %q", imp.Type)
	}
}

func (r Resolver) read(ctx context.Context, imp Import) ([]byte, error) {
	if imp.Path != "" {
		return os.ReadFile(imp.Path)
	}
	if imp.URL == "" {
		return nil, errors.New("path or url is required")
	}
	var cached CacheEntry
	if r.Cache != nil {
		entry, ok, err := r.Cache.GetImportCache(ctx, imp.URL)
		if err != nil {
			return nil, err
		}
		if ok {
			cached = entry
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imp.URL, nil)
	if err != nil {
		return nil, err
	}
	if cached.ETag != "" {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	if cached.LastModified != "" {
		req.Header.Set("If-Modified-Since", cached.LastModified)
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		if len(cached.Content) == 0 {
			return nil, errors.New("server returned 304 without a cached body")
		}
		return append([]byte(nil), cached.Content...), nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if r.Cache != nil {
		sum := sha256.Sum256(data)
		entry := CacheEntry{
			URL: imp.URL, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"),
			SHA256: hex.EncodeToString(sum[:]), Content: data,
		}
		if err := r.Cache.PutImportCache(ctx, entry); err != nil {
			return nil, err
		}
	}
	return data, nil
}
