package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/morain/5gws/internal/config"
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
	if err := s.Activate(ctx, draft.ID); err != nil {
		t.Fatal(err)
	}
	current, err = s.Active(ctx)
	if err != nil || current.ID != draft.ID || current.Bundle.Config.Logging.Level != "debug" {
		t.Fatalf("active after apply: %+v, %v", current, err)
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
