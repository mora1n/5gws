package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/morain/5gws/internal/auth"
	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/engine"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/service"
	"github.com/morain/5gws/internal/store"
)

type apiRuntime struct{}

func (apiRuntime) Prepare(_ context.Context, _ int64, _ store.Bundle) (string, error) {
	return "/tmp/revision", nil
}
func (apiRuntime) Apply(_ context.Context, _ string, _ store.Bundle) error { return nil }

func TestBootstrapLoginAndProtectedDraft(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	router := server.Router(false)

	claim := request(t, http.MethodPost, "/api/v1/bootstrap", map[string]string{
		"token": "setup", "username": "admin", "password": "correct-horse-battery",
	})
	claim.RemoteAddr = "192.0.2.10:4000"
	if response := serve(router, claim); response.Code != http.StatusCreated {
		t.Fatalf("bootstrap: %d %s", response.Code, response.Body.String())
	}
	login := request(t, http.MethodPost, "/api/v1/session", map[string]string{"username": "admin", "password": "correct-horse-battery"})
	login.RemoteAddr = "192.0.2.10:4001"
	response := serve(router, login)
	if response.Code != http.StatusOK {
		t.Fatalf("login: %d %s", response.Code, response.Body.String())
	}
	result := response.Result()
	var session *http.Cookie
	for _, cookie := range result.Cookies() {
		if cookie.Name == sessionCookie {
			session = cookie
		}
	}
	if session == nil || !session.HttpOnly || !session.Secure {
		t.Fatalf("session cookie = %#v", session)
	}
	draft := request(t, http.MethodGet, "/api/v1/draft", nil)
	draft.RemoteAddr = "192.0.2.10:4002"
	draft.AddCookie(session)
	if response := serve(router, draft); response.Code != http.StatusOK {
		t.Fatalf("draft: %d %s", response.Code, response.Body.String())
	}
}

func TestCIDRRestrictionRunsBeforeAPI(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	req := request(t, http.MethodGet, "/api/v1/health", nil)
	req.RemoteAddr = "198.51.100.10:4000"
	if response := serve(server.Router(false), req); response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func testServer(t *testing.T) (*Server, func()) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Panel:   config.PanelConfig{Listen: "127.0.0.1:19443", AllowedCIDRs: []string{"192.0.2.0/24"}},
		Network: config.NetworkConfig{GatewayIP: "10.0.0.1", InternalCIDR: "172.22.0.0/16", IngressIface: "eth0"},
		DNS:     config.DNSConfig{DOTDomain: "dot.example.com"}, Logging: config.LoggingConfig{Level: "info"},
		Exits: []config.ExitConfig{{Name: "direct", Type: "direct"}},
	}
	cfg.ApplyDefaults()
	rule := rules.Rule{Name: "test", Exit: "direct", DomainSuffix: []string{"example.com"}}
	_, err = state.Initialize(context.Background(), store.Bundle{Config: cfg, Rules: rules.File{Rules: []rules.Rule{rule}}, ResolvedRules: []rules.Rule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	logs := engine.NewLogBuffer(1024)
	supervisor := engine.NewSupervisor(t.TempDir(), logs)
	return &Server{Service: service.New(state, apiRuntime{}), Auth: auth.New(state.DB(), time.Hour), Supervisor: supervisor, SetupToken: "setup", Version: "test"}, func() { state.Close() }
}

func request(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
