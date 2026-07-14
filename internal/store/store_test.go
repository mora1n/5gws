package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
	"github.com/pelletier/go-toml/v2"
)

func TestRevisionLifecycle(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "5gws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	if _, err := s.Active(ctx); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("active before init = %v", err)
	}
	bundle := validBundle()
	active, err := s.Initialize(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Config.Logging.Level = "debug"
	draft, err := s.SaveDraft(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if draft.ID == active.ID {
		t.Fatal("draft did not create a revision")
	}
	current, err := s.Active(ctx)
	if err != nil || current.ID != active.ID {
		t.Fatalf("active changed before apply: %+v, %v", current, err)
	}
	if _, err := s.Activate(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	current, err = s.Active(ctx)
	if err != nil || current.ID != draft.ID || current.Bundle.Config.Logging.Level != "debug" {
		t.Fatalf("active after apply: %+v, %v", current, err)
	}
}

func TestResetDraftAndPruneRevisions(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	active, err := s.Initialize(ctx, validBundle())
	if err != nil {
		t.Fatal(err)
	}
	bundle := validBundle()
	bundle.Config.Logging.Level = "debug"
	draft, err := s.SaveDraft(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetDraftToActive(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneRevisions(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM revisions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("revision count=%d, want 1", count)
	}
	current, err := s.Draft(ctx)
	if err != nil || current.ID != active.ID || current.ID == draft.ID {
		t.Fatalf("draft=%+v, err=%v", current, err)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("compact: %v", err)
	}
}

func TestRevisionReadAppliesNewConfigDefaults(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "5gws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	active, err := s.Initialize(ctx, validBundle())
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := s.db.QueryRowContext(ctx, "SELECT payload_json FROM revisions WHERE id = ?", active.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	network := value["config"].(map[string]any)["network"].(map[string]any)
	delete(network, "haproxy_max_connections")
	payload, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE revisions SET payload_json = ? WHERE id = ?", payload, active.ID); err != nil {
		t.Fatal(err)
	}

	reloaded, err := s.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Bundle.Config.Network.HAProxyMaxConnections
	if got == nil || *got != config.DefaultHAProxyMaxConnections {
		t.Fatalf("haproxy max connections = %v, want %d", got, config.DefaultHAProxyMaxConnections)
	}
}

func TestRevisionReadSeedsOptionalDNSPoolAndRuleOnlyWhenFieldIsMissing(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "5gws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	bundle := validBundle()
	bundle.Config.DNS.CustomPools = []config.DNSPoolConfig{}
	active, err := s.Initialize(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := s.db.QueryRowContext(ctx, "SELECT payload_json FROM revisions WHERE id = ?", active.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	dnsConfig := value["config"].(map[string]any)["dns"].(map[string]any)
	delete(dnsConfig, "custom_pools")
	payload, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE revisions SET payload_json = ? WHERE id = ?", payload, active.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Bundle.Config.DNS.CustomPools) != 1 || !containsRule(reloaded.Bundle.Rules.Rules, "netease-music") {
		t.Fatalf("legacy defaults not seeded: pools=%#v rules=%#v", reloaded.Bundle.Config.DNS.CustomPools, reloaded.Bundle.Rules.Rules)
	}

	reloaded.Bundle.Config.DNS.CustomPools = []config.DNSPoolConfig{}
	reloaded.Bundle.Rules.Rules = nil
	saved, err := s.SaveDraft(ctx, reloaded.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Bundle.Config.DNS.CustomPools == nil || len(saved.Bundle.Config.DNS.CustomPools) != 0 || containsRule(saved.Bundle.Rules.Rules, "netease-music") {
		t.Fatalf("explicit deletion was restored: pools=%#v rules=%#v", saved.Bundle.Config.DNS.CustomPools, saved.Bundle.Rules.Rules)
	}
}

func TestBundleTOMLPreservesExplicitlyEmptyCustomPools(t *testing.T) {
	bundle := validBundle()
	bundle.Config.DNS.CustomPools = []config.DNSPoolConfig{}
	data, err := toml.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Bundle
	if err := toml.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Config.DNS.CustomPools == nil || len(decoded.Config.DNS.CustomPools) != 0 {
		t.Fatalf("custom pools = %#v after TOML round trip\n%s", decoded.Config.DNS.CustomPools, data)
	}
}

func containsRule(items []rules.Rule, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}

func TestRevisionReadAcceptsLegacyUppercaseRulesJSON(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "5gws.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	bundle := validBundle()
	bundle.Rules = rules.ManagedFile()
	active, err := s.Initialize(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := s.db.QueryRowContext(ctx, "SELECT payload_json FROM revisions WHERE id = ?", active.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	ruleFile := value["rules"].(map[string]any)
	ruleFile["Imports"] = ruleFile["imports"]
	ruleFile["Rules"] = ruleFile["rules"]
	delete(ruleFile, "imports")
	delete(ruleFile, "rules")
	for _, item := range ruleFile["Imports"].([]any) {
		fields := item.(map[string]any)
		for _, name := range []string{"name", "type", "path", "url", "format", "exit", "dns_pool"} {
			legacy := map[string]string{"name": "Name", "type": "Type", "path": "Path", "url": "URL", "format": "Format", "exit": "Exit", "dns_pool": "DNSPool"}[name]
			fields[legacy] = fields[name]
			delete(fields, name)
		}
	}
	payload, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE revisions SET payload_json = ? WHERE id = ?", payload, active.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.ValidateManaged(reloaded.Bundle.Rules); err != nil {
		t.Fatalf("legacy rules were not decoded: %v", err)
	}
}

func validBundle() Bundle {
	access := true
	cfg := config.Config{
		Network: config.NetworkConfig{GatewayIP: "10.0.0.1", InternalCIDR: "172.22.0.0/16", IngressIface: "eth0"},
		DNS:     config.DNSConfig{DOTDomain: "dot.example.com"},
		Logging: config.LoggingConfig{Level: "info", Access: &access},
		Exits:   []config.ExitConfig{{Name: "direct", Type: "direct"}},
	}
	cfg.ApplyDefaults()
	return Bundle{Config: cfg}
}
