package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
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

	if _, err := server.Auth.ResetAdmin(context.Background(), "correct-horse-battery"); err != nil {
		t.Fatal(err)
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

func TestIOSProfileRoutesUsePanelHTTPSOrigin(t *testing.T) {
	server, closeState := testServerIOS(t, true)
	defer closeState()
	router := server.Router(false)

	profile := request(t, http.MethodGet, "/ios/5gws-dot.mobileconfig", nil)
	profile.RemoteAddr = "192.0.2.10:4000"
	response := serve(router, profile)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("profile response = %d, %q", response.Code, response.Header().Get("Content-Type"))
	}
	if !strings.Contains(response.Body.String(), "<string>dot.example.com</string>") {
		t.Fatalf("profile does not contain DoT domain: %s", response.Body.String())
	}

	qr := request(t, http.MethodGet, "/ios/5gws-dot.png", nil)
	qr.RemoteAddr = "192.0.2.10:4000"
	response = serve(router, qr)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(response.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("QR response = %d, %q", response.Code, response.Header().Get("Content-Type"))
	}

	info := serve(server.Router(true), request(t, http.MethodGet, "/api/v1/ios/profile", nil))
	if !strings.Contains(info.Body.String(), `"profile_url":"https://dot.example.com/ios/5gws-dot.mobileconfig"`) {
		t.Fatalf("profile info = %s", info.Body.String())
	}
}

func TestDisabledIOSProfileReturnsNotFound(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	response := serve(server.Router(true), request(t, http.MethodGet, "/ios/5gws-dot.mobileconfig", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestDraftOmitsResolvedRulesAndActiveRulesAreSummarized(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	router := server.Router(true)

	draft := serve(router, request(t, http.MethodGet, "/api/v1/draft", nil))
	if strings.Contains(draft.Body.String(), "resolved_rules") {
		t.Fatalf("draft includes resolved rules: %s", draft.Body.String())
	}
	summary := serve(router, request(t, http.MethodGet, "/api/v1/active/rules", nil))
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"title":"出口规则 · exit:direct"`) {
		t.Fatalf("active rules summary = %d %s", summary.Code, summary.Body.String())
	}
	if strings.Contains(summary.Body.String(), "0001-01-01") {
		t.Fatalf("active rules summary has zero timestamp: %s", summary.Body.String())
	}
}

func TestSPAAssetCacheHeaders(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	server.Web = fstest.MapFS{
		"index.html":    {Data: []byte("index")},
		"assets/app.js": {Data: []byte("script")},
	}
	asset := request(t, http.MethodGet, "/assets/app.js", nil)
	asset.RemoteAddr = "192.0.2.10:4000"
	response := serve(server.Router(false), asset)
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset Cache-Control = %q", got)
	}
}

func testServer(t *testing.T) (*Server, func()) {
	return testServerIOS(t, false)
}

func testServerIOS(t *testing.T, iosEnabled bool) (*Server, func()) {
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
		IOS:   config.IOSConfig{Enabled: iosEnabled},
	}
	cfg.ApplyDefaults()
	rule := rules.Rule{Name: "test", Exit: "direct", DomainSuffix: []string{"example.com"}}
	active, err := state.Initialize(context.Background(), store.Bundle{Config: cfg, Rules: rules.File{Rules: []rules.Rule{rule}}, ResolvedRules: []rules.Rule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	logs := engine.NewLogBuffer(1024)
	supervisor := engine.NewSupervisor(t.TempDir(), logs)
	return &Server{Service: service.New(state, apiRuntime{}, active, active), Auth: auth.New(state.DB(), time.Hour), Supervisor: supervisor, Version: "test"}, func() { state.Close() }
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
