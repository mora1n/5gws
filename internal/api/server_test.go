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

	"github.com/pelletier/go-toml/v2"

	"github.com/morain/5gws/internal/auth"
	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/diagnostics"
	"github.com/morain/5gws/internal/engine"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/service"
	"github.com/morain/5gws/internal/store"
)

type apiRuntime struct{}

type apiRuleResolver struct{}

func (apiRuleResolver) Normalize(_ context.Context, file rules.File) (rules.Normalized, error) {
	return rules.Normalized{Rules: append([]rules.Rule(nil), file.Rules...)}, nil
}

func (apiRuntime) Preflight(_ context.Context, _ store.Bundle) error { return nil }

func (apiRuntime) Prepare(_ context.Context, _ int64, _ store.Bundle) (string, error) {
	return "/tmp/revision", nil
}
func (apiRuntime) Apply(_ context.Context, _ string, _ store.Bundle) error { return nil }

func TestBootstrapLoginAndProtectedConfig(t *testing.T) {
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
	configRequest := request(t, http.MethodGet, "/api/v1/config", nil)
	configRequest.RemoteAddr = "192.0.2.10:4002"
	configRequest.AddCookie(session)
	if response := serve(router, configRequest); response.Code != http.StatusOK {
		t.Fatalf("config: %d %s", response.Code, response.Body.String())
	}
	for _, path := range []string{"/api/v1/active", "/api/v1/draft", "/api/v1/revisions", "/api/v1/revisions/1/rollback"} {
		req := request(t, http.MethodGet, path, nil)
		req.RemoteAddr = "192.0.2.10:4003"
		req.AddCookie(session)
		if response := serve(router, req); response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d, want 404", path, response.Code)
		}
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

func TestWebConfigValidateAndNoChangeApplyDoNotCreateRevisions(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	active, err := server.Service.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	active.Bundle.ResolvedRules = nil
	router := server.Router(true)
	validate := serve(router, request(t, http.MethodPost, "/api/v1/config/validate", active.Bundle))
	if validate.Code != http.StatusOK {
		t.Fatalf("validate=%d %s", validate.Code, validate.Body.String())
	}
	applyRequest := request(t, http.MethodPost, "/api/v1/config/apply", active.Bundle)
	applyRequest.Header.Set(applyOperationHeader, "3e2f3ef7-9ac5-4ca8-8a19-4ca54c51c10c")
	apply := serve(router, applyRequest)
	if apply.Code != http.StatusAccepted {
		t.Fatalf("apply=%d %s", apply.Code, apply.Body.String())
	}
	operation := waitApplyOperation(t, router, "3e2f3ef7-9ac5-4ca8-8a19-4ca54c51c10c")
	if operation.Status != "succeeded" || operation.Changed {
		t.Fatalf("operation=%+v", operation)
	}
	var count int
	if err := server.Service.Store().DB().QueryRow("SELECT COUNT(*) FROM revisions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("revision count=%d, want 1", count)
	}
	configResponse := serve(router, request(t, http.MethodGet, "/api/v1/config", nil))
	if strings.Contains(configResponse.Body.String(), "resolved_rules") {
		t.Fatalf("config leaked resolved rules: %s", configResponse.Body.String())
	}
	if strings.Contains(configResponse.Body.String(), `"Rules"`) || !strings.Contains(configResponse.Body.String(), `"rules":{"imports"`) {
		t.Fatalf("config rules do not use lowercase JSON keys: %s", configResponse.Body.String())
	}
	defaults := serve(router, request(t, http.MethodGet, "/api/v1/rules/defaults", nil))
	for _, name := range []string{"ip-check", "speedtest", "cn", "gfw"} {
		if defaults.Code != http.StatusOK || !strings.Contains(defaults.Body.String(), `"name":"`+name+`"`) {
			t.Fatalf("defaults missing %q: %d %s", name, defaults.Code, defaults.Body.String())
		}
	}
}

func TestCurrentConfigSupplementsMissingManagedRulesWithoutMutatingActive(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	active, err := server.Service.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	corrupted := active.Bundle
	corrupted.Rules = rules.File{Rules: []rules.Rule{{Name: "uhd", Exit: "direct", DomainSuffix: []string{"uhdnow.com"}}}}
	if _, err := server.Service.Store().DB().Exec(`UPDATE revisions SET payload_json = ? WHERE id = ?`, mustJSON(t, corrupted), active.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := server.Service.Store().Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	server.Service = service.New(service.Options{Context: context.Background(), Store: server.Service.Store(), Runtime: apiRuntime{}, Resolver: apiRuleResolver{}, Active: loaded, Draft: loaded})
	response := serve(server.Router(true), request(t, http.MethodGet, "/api/v1/config", nil))
	for _, name := range []string{"uhd", "ip-check", "speedtest", "cn", "gfw"} {
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"`+name+`"`) {
			t.Fatalf("supplemented config missing %q: %d %s", name, response.Code, response.Body.String())
		}
	}
	backup := serve(server.Router(true), request(t, http.MethodGet, "/api/v1/backup", nil))
	for _, name := range []string{"uhd", "ip-check", "speedtest", "cn", "gfw"} {
		if backup.Code != http.StatusOK || !strings.Contains(backup.Body.String(), `name = '`+name+`'`) {
			t.Fatalf("backup missing %q: %d %s", name, backup.Code, backup.Body.String())
		}
	}
	stored, err := server.Service.Store().Active(context.Background())
	if err != nil || len(stored.Bundle.Rules.Imports) != 0 || len(stored.Bundle.Rules.Rules) != 1 {
		t.Fatalf("active bundle was mutated: %+v, %v", stored.Bundle.Rules, err)
	}
}

func TestAsyncApplyRejectsMissingManagedRuleWithoutActivation(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	active, err := server.Service.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundle := active.Bundle
	bundle.Rules.Rules = bundle.Rules.Rules[:1]
	id := "6c231346-0d26-47d3-b54e-f36b49c7f147"
	req := request(t, http.MethodPost, "/api/v1/config/apply", bundle)
	req.Header.Set(applyOperationHeader, id)
	if response := serve(server.Router(true), req); response.Code != http.StatusAccepted {
		t.Fatalf("apply=%d %s", response.Code, response.Body.String())
	}
	operation := waitApplyOperation(t, server.Router(true), id)
	if operation.Status != "failed" || !strings.Contains(operation.Error, "managed rule") {
		t.Fatalf("operation=%+v", operation)
	}
	current, err := server.Service.Active(context.Background())
	if err != nil || current.ID != active.ID {
		t.Fatalf("active=%+v error=%v", current, err)
	}
}

func TestAsyncApplyRejectsModifiedManagedImportWithoutActivation(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	active, err := server.Service.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundle := active.Bundle
	bundle.Rules.Imports[0].URL = "https://example.com/modified.json"
	id := "4b762719-544b-42da-a6d5-b5fc91719d5c"
	req := request(t, http.MethodPost, "/api/v1/config/apply", bundle)
	req.Header.Set(applyOperationHeader, id)
	if response := serve(server.Router(true), req); response.Code != http.StatusAccepted {
		t.Fatalf("apply=%d %s", response.Code, response.Body.String())
	}
	operation := waitApplyOperation(t, server.Router(true), id)
	if operation.Status != "failed" || !strings.Contains(operation.Error, "read-only") {
		t.Fatalf("operation=%+v", operation)
	}
	current, err := server.Service.Active(context.Background())
	if err != nil || current.ID != active.ID {
		t.Fatalf("active=%+v error=%v", current, err)
	}
}

func TestConfigImportSupplementsManagedRules(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	active, err := server.Service.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundle := active.Bundle
	bundle.Rules = rules.File{Rules: []rules.Rule{{Name: "uhd", Exit: "direct", DomainSuffix: []string{"uhdnow.com"}}}}
	data, err := toml.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/config/import", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/toml")
	response := serve(server.Router(true), req)
	for _, name := range []string{"uhd", "ip-check", "speedtest", "cn", "gfw"} {
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"`+name+`"`) {
			t.Fatalf("import missing %q: %d %s", name, response.Code, response.Body.String())
		}
	}
}

func TestApplyOperationHTTPValidationAndConflict(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	applier := &blockingApplier{started: make(chan struct{}), release: make(chan struct{})}
	server.applyOnce.Do(func() { server.applies = newApplyCoordinator(applier) })
	router := server.Router(true)

	missing := serve(router, request(t, http.MethodPost, "/api/v1/config/apply", serviceBundleForAPI()))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing ID=%d %s", missing.Code, missing.Body.String())
	}
	id := "3e2f3ef7-9ac5-4ca8-8a19-4ca54c51c10c"
	begin := request(t, http.MethodPost, "/api/v1/config/apply", serviceBundleForAPI())
	begin.Header.Set(applyOperationHeader, id)
	if response := serve(router, begin); response.Code != http.StatusAccepted {
		t.Fatalf("begin=%d %s", response.Code, response.Body.String())
	}
	<-applier.started
	retry := request(t, http.MethodPost, "/api/v1/config/apply", serviceBundleForAPI())
	retry.Header.Set(applyOperationHeader, id)
	if response := serve(router, retry); response.Code != http.StatusAccepted {
		t.Fatalf("retry=%d %s", response.Code, response.Body.String())
	}
	conflict := request(t, http.MethodPost, "/api/v1/config/apply", serviceBundleForAPI())
	conflict.Header.Set(applyOperationHeader, "d57f56b8-774a-4dd7-b34e-bd62338ac8e4")
	if response := serve(router, conflict); response.Code != http.StatusConflict {
		t.Fatalf("conflict=%d %s", response.Code, response.Body.String())
	}
	if response := serve(router, request(t, http.MethodGet, "/api/v1/config/apply/bad-id", nil)); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d %s", response.Code, response.Body.String())
	}
	if response := serve(router, request(t, http.MethodGet, "/api/v1/config/apply/d57f56b8-774a-4dd7-b34e-bd62338ac8e4", nil)); response.Code != http.StatusNotFound {
		t.Fatalf("unknown status=%d %s", response.Code, response.Body.String())
	}
	close(applier.release)
	if operation := waitApplyOperation(t, router, id); operation.Status != "succeeded" {
		t.Fatalf("operation=%+v", operation)
	}
}

func serviceBundleForAPI() store.Bundle {
	cfg := config.Config{
		Network: config.NetworkConfig{GatewayIP: "10.0.0.1", InternalCIDR: "172.22.0.0/16", IngressIface: "eth0"},
		DNS:     config.DNSConfig{DOTDomain: "dot.example.com"}, Exits: []config.ExitConfig{{Name: "direct", Type: "direct"}},
	}
	cfg.ApplyDefaults()
	return store.Bundle{Config: cfg, Rules: rules.File{Rules: []rules.Rule{{Name: "test", Exit: "direct", DomainSuffix: []string{"example.com"}}}}}
}

func waitApplyOperation(t *testing.T, router http.Handler, id string) ApplyOperation {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response := serve(router, request(t, http.MethodGet, "/api/v1/config/apply/"+id, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("apply status=%d %s", response.Code, response.Body.String())
		}
		var operation ApplyOperation
		if err := json.Unmarshal(response.Body.Bytes(), &operation); err != nil {
			t.Fatal(err)
		}
		if terminalApplyStatus(operation.Status) {
			return operation
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("apply operation did not finish")
	return ApplyOperation{}
}

func TestActiveRuleSummaryNormalizesOnlyLegacySingleImports(t *testing.T) {
	bundle := store.Bundle{
		Rules: rules.File{Imports: []rules.Import{
			{Name: "cn"},
			{Name: "multiple"},
		}},
		ResolvedRules: []rules.Rule{
			{Name: "cn-1", DNSPool: "cn", DomainSuffix: []string{"example.cn"}},
			{Name: "multiple-1", Exit: "direct", DomainSuffix: []string{"one.example"}},
			{Name: "multiple-2", Exit: "direct", DomainSuffix: []string{"two.example"}},
		},
	}
	summary := summarizeActiveRules(bundle)
	var names []string
	for _, group := range summary.Groups {
		for _, rule := range group.Rules {
			names = append(names, rule.Name)
		}
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"cn", "multiple-1", "multiple-2"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("summary names = %q, missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "cn-1") {
		t.Fatalf("legacy single import was not normalized: %q", joined)
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
	unknownAPI := request(t, http.MethodGet, "/api/v1/revisions", nil)
	unknownAPI.RemoteAddr = "192.0.2.10:4000"
	if response := serve(server.Router(false), unknownAPI); response.Code != http.StatusNotFound {
		t.Fatalf("unknown API status = %d, want 404", response.Code)
	}
	historyRoute := request(t, http.MethodGet, "/rules", nil)
	historyRoute.RemoteAddr = "192.0.2.10:4000"
	if response := serve(server.Router(false), historyRoute); response.Code != http.StatusOK || response.Body.String() != "index" {
		t.Fatalf("history route = %d %q", response.Code, response.Body.String())
	}
}

func TestMetricsAreChronologicalAndExposeDNSStatus(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	now := time.Now().Unix()
	for _, metric := range []engine.Metrics{
		{Timestamp: now, Interface: "eth0", DNSOK: false},
		{Timestamp: now + 1, Interface: "eth0", DNSOK: true, DNSLatencyMS: 4.2},
	} {
		if err := server.Service.Store().PutMetric(context.Background(), metric.Timestamp, metric); err != nil {
			t.Fatal(err)
		}
	}
	response := serve(server.Router(true), request(t, http.MethodGet, "/api/v1/metrics", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("metrics: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Metrics []engine.Metrics `json:"metrics"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Metrics) != 2 || body.Metrics[0].Timestamp != now || body.Metrics[1].Timestamp != now+1 || body.Metrics[0].DNSOK || !body.Metrics[1].DNSOK {
		t.Fatalf("metrics = %+v", body.Metrics)
	}
}

func TestRunDiagnosticsValidatesScopeAndOmitsConfigurationSecrets(t *testing.T) {
	server, closeState := testServer(t)
	defer closeState()
	invalid := serve(server.Router(true), request(t, http.MethodPost, "/api/v1/diagnostics/run?scope=invalid", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid scope = %d %s", invalid.Code, invalid.Body.String())
	}
	egress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("203.0.113.30")) }))
	defer egress.Close()
	server.Diagnostics = diagnostics.Runner{EgressURL: egress.URL}
	response := serve(server.Router(true), request(t, http.MethodPost, "/api/v1/diagnostics/run?scope=exits", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"egress_ip":"203.0.113.30"`) {
		t.Fatalf("diagnostics = %d %s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"password", "private_key", "session"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("diagnostics contains %q: %s", secret, response.Body.String())
		}
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
	file := rules.EnsureManaged(rules.File{Rules: []rules.Rule{rule}})
	active, err := state.Initialize(context.Background(), store.Bundle{Config: cfg, Rules: file, ResolvedRules: []rules.Rule{rule}})
	if err != nil {
		t.Fatal(err)
	}
	logs := engine.NewLogBuffer(1024)
	supervisor := engine.NewSupervisor(context.Background(), t.TempDir(), logs)
	application := service.New(service.Options{
		Context: context.Background(), Store: state, Runtime: apiRuntime{}, Resolver: apiRuleResolver{}, Active: active, Draft: active,
	})
	return &Server{Service: application, Auth: auth.New(state.DB(), time.Hour), Supervisor: supervisor, Version: "test"}, func() { state.Close() }
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
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
