package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/store"
)

type testRuntime struct {
	preflightErr   error
	prepareErr     error
	applyErr       error
	preflightCalls int
	prepareCalls   int
	applyCalls     int
	applyContext   context.Context
}

type testRuleResolver struct{}

func (testRuleResolver) Normalize(_ context.Context, file rules.File) (rules.Normalized, error) {
	return rules.Normalized{Rules: append([]rules.Rule(nil), file.Rules...)}, nil
}

func (r *testRuntime) Preflight(_ context.Context, _ store.Bundle) error {
	r.preflightCalls++
	return r.preflightErr
}

func (r *testRuntime) Prepare(_ context.Context, id int64, _ store.Bundle) (string, error) {
	r.prepareCalls++
	return filepath.Join("/tmp", "revision"), r.prepareErr
}

func TestValidateBundleDoesNotPersistRevision(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx := context.Background()
	if _, err := state.Initialize(ctx, serviceBundle()); err != nil {
		t.Fatal(err)
	}
	runtime := &testRuntime{}
	if _, err := newTestService(t, state, runtime).ValidateBundle(ctx, serviceBundle()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := state.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM revisions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || runtime.preflightCalls != 1 || runtime.prepareCalls != 0 {
		t.Fatalf("revisions=%d preflight=%d prepare=%d", count, runtime.preflightCalls, runtime.prepareCalls)
	}
}
func (r *testRuntime) Apply(ctx context.Context, _ string, _ store.Bundle) error {
	r.applyCalls++
	r.applyContext = ctx
	return r.applyErr
}

func TestApplyUsesDaemonContext(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	ctx, cancel := context.WithCancel(context.Background())
	active, err := state.Initialize(ctx, serviceBundle())
	if err != nil {
		t.Fatal(err)
	}
	bundle := active.Bundle
	bundle.Config.Logging.Level = "debug"
	runtime := &testRuntime{}
	app := New(Options{Context: ctx, Store: state, Runtime: runtime, Resolver: testRuleResolver{}, Active: active, Draft: active})
	if _, err := app.ApplyBundle(bundle); err != nil {
		t.Fatal(err)
	}
	if runtime.applyContext == nil || runtime.applyContext.Err() != nil {
		t.Fatalf("runtime context=%v", runtime.applyContext)
	}
	cancel()
	select {
	case <-runtime.applyContext.Done():
	case <-time.After(time.Second):
		t.Fatal("runtime context did not follow daemon cancellation")
	}
}

func TestApplyBundleNoChangeDoesNotTouchRuntime(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	active, err := state.Initialize(context.Background(), serviceBundle())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.SaveDraft(context.Background(), active.Bundle); err != nil {
		t.Fatal(err)
	}
	runtime := &testRuntime{}
	result, err := newTestService(t, state, runtime).ApplyBundle(active.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || result.RevisionID != active.ID || runtime.preflightCalls != 0 || runtime.prepareCalls != 0 || runtime.applyCalls != 0 {
		t.Fatalf("result=%+v runtime=%+v", result, runtime)
	}
	var count int
	if err := state.DB().QueryRow("SELECT COUNT(*) FROM revisions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("revision count=%d, %v", count, err)
	}
}

func TestApplyBundleRejectsMissingManagedRuleBeforeRuntime(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	active, err := state.Initialize(context.Background(), serviceBundle())
	if err != nil {
		t.Fatal(err)
	}
	bundle := active.Bundle
	bundle.Rules.Rules = bundle.Rules.Rules[:1]
	runtime := &testRuntime{}
	_, err = newTestService(t, state, runtime).ApplyBundle(bundle)
	if err == nil || runtime.preflightCalls != 0 || runtime.prepareCalls != 0 || runtime.applyCalls != 0 {
		t.Fatalf("error=%v runtime=%+v", err, runtime)
	}
	current, err := state.Active(context.Background())
	if err != nil || current.ID != active.ID {
		t.Fatalf("active=%+v error=%v", current, err)
	}
}

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
	app := newTestService(t, state, runtime)
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
	draft, err := state.Draft(ctx)
	if err != nil || draft.ID != active.ID {
		t.Fatalf("draft was not restored to active: %+v, %v", draft, err)
	}
	var count int
	if err := state.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM revisions").Scan(&count); err != nil || count != 1 {
		t.Fatalf("revision count=%d, %v", count, err)
	}
}

func TestServiceReadsPersistedSnapshotsWithoutDatabaseQueries(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	active, err := state.Initialize(ctx, serviceBundle())
	if err != nil {
		t.Fatal(err)
	}
	app := New(Options{Context: context.Background(), Store: state, Runtime: &testRuntime{}, Active: active, Draft: active})
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	gotActive, err := app.Active(ctx)
	if err != nil || gotActive.ID != active.ID {
		t.Fatalf("cached active = %+v, %v", gotActive, err)
	}
	gotDraft, err := app.Draft(ctx)
	if err != nil || gotDraft.ID != active.ID {
		t.Fatalf("cached draft = %+v, %v", gotDraft, err)
	}
}

func newTestService(t *testing.T, state *store.Store, runtime Runtime) *Service {
	t.Helper()
	active, err := state.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	draft, err := state.Draft(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return New(Options{Context: context.Background(), Store: state, Runtime: runtime, Resolver: testRuleResolver{}, Active: active, Draft: draft})
}

func serviceBundle() store.Bundle {
	cfg := config.Config{Network: config.NetworkConfig{GatewayIP: "10.0.0.1", InternalCIDR: "172.22.0.0/16", IngressIface: "eth0"}, DNS: config.DNSConfig{DOTDomain: "dot.example.com"}, Logging: config.LoggingConfig{Level: "info"}, Exits: []config.ExitConfig{{Name: "direct", Type: "direct"}}}
	cfg.ApplyDefaults()
	rule := rules.Rule{Name: "test", Exit: "direct", DomainSuffix: []string{"example.com"}}
	file := rules.EnsureManaged(rules.File{Rules: []rules.Rule{rule}})
	return store.Bundle{Config: cfg, Rules: file, ResolvedRules: []rules.Rule{rule}}
}
