package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/store"
)

type testRuntime struct {
	prepareErr error
	applyErr   error
}

func (r *testRuntime) Prepare(_ context.Context, id int64, _ store.Bundle) (string, error) {
	return filepath.Join("/tmp", "revision"), r.prepareErr
}

func TestValidateMarksFailedRevision(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	if _, err := state.Initialize(ctx, serviceBundle()); err != nil {
		t.Fatal(err)
	}
	runtime := &testRuntime{prepareErr: errors.New("haproxy validation failed")}
	if _, err := New(state, runtime).ValidateDraft(ctx); err == nil {
		t.Fatal("expected validation failure")
	}
	revisions, err := state.Revisions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if revisions[0].Status != "failed" || !strings.Contains(revisions[0].Error, "haproxy validation failed") {
		t.Fatalf("prepared revision was not marked failed: %#v", revisions[0])
	}
}
func (r *testRuntime) Apply(_ context.Context, _ string, _ store.Bundle) error { return r.applyErr }

func TestApplyDoesNotActivateRuntimeFailure(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	bundle := serviceBundle()
	active, err := state.Initialize(ctx, bundle)
	if err != nil {
		t.Fatal(err)
	}
	bundle.Config.Logging.Level = "debug"
	if _, err := state.SaveDraft(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	runtime := &testRuntime{applyErr: errors.New("readiness failed")}
	app := New(state, runtime)
	if _, err := app.Apply(ctx); err == nil {
		t.Fatal("expected apply failure")
	}
	current, err := state.Active(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.ID != active.ID {
		t.Fatalf("active changed to %d", current.ID)
	}
}

func serviceBundle() store.Bundle {
	cfg := config.Config{Network: config.NetworkConfig{GatewayIP: "10.0.0.1", InternalCIDR: "172.22.0.0/16", IngressIface: "eth0"}, DNS: config.DNSConfig{DOTDomain: "dot.example.com"}, Logging: config.LoggingConfig{Level: "info"}, Exits: []config.ExitConfig{{Name: "direct", Type: "direct"}}}
	cfg.ApplyDefaults()
	rule := rules.Rule{Name: "test", Exit: "direct", DomainSuffix: []string{"example.com"}}
	return store.Bundle{Config: cfg, Rules: rules.File{Rules: []rules.Rule{rule}}, ResolvedRules: []rules.Rule{rule}}
}
