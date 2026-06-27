package telegram

import (
	"strings"
	"testing"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/rules"
)

func TestHandleIOSUsesConfigAndNoQR(t *testing.T) {
	h, calls := testHandler()
	out := h.handleText("/ios")
	if out.Text != "/usr/local/bin/5gws ios-link --config /etc/5gws/config.toml --no-qr" {
		t.Fatalf("unexpected output: %q", out.Text)
	}
	if got := strings.Join(calls()[0], " "); got != "/usr/local/bin/5gws ios-link --config /etc/5gws/config.toml --no-qr" {
		t.Fatalf("unexpected command: %s", got)
	}
}

func TestHandleDoctorUsesConfigAndRules(t *testing.T) {
	h, calls := testHandler()
	_ = h.handleText("/doctor")
	got := strings.Join(calls()[0], " ")
	want := "/usr/local/bin/5gws doctor --config /etc/5gws/config.toml --rules /etc/5gws/rules.toml"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestApplyRequiresConfirmation(t *testing.T) {
	h, calls := testHandler()
	prompt := h.handleText("/apply")
	if len(calls()) != 0 {
		t.Fatalf("apply prompt executed command: %v", calls())
	}
	if !strings.Contains(prompt.Text, "确认执行") || prompt.Markup == nil {
		t.Fatalf("apply prompt missing confirmation: %#v", prompt)
	}

	done := h.handleCallback("confirm:apply")
	if !strings.Contains(done.Text, "--skip-bot-restart") {
		t.Fatalf("apply callback output missing skip flag: %q", done.Text)
	}
	got := strings.Join(calls()[0], " ")
	want := "/usr/local/bin/5gws apply --config /etc/5gws/config.toml --rules /etc/5gws/rules.toml --skip-bot-restart"
	if got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestRestartSkipsBotService(t *testing.T) {
	h, calls := testHandler()
	h.loadConfig = func() (config.Config, error) {
		return config.Config{
			IOS:      config.IOSConfig{Enabled: true},
			Telegram: config.TelegramConfig{Enabled: true},
			Exits: []config.ExitConfig{
				{Name: "direct", Type: "direct"},
				{Name: "ss1", Type: "shadowsocks-rust"},
			},
		}, nil
	}
	out := h.handleCallback("confirm:restart")
	if strings.Contains(out.Text, "5gws-bot.service") {
		t.Fatalf("restart output includes bot service:\n%s", out.Text)
	}
	for _, call := range calls() {
		if strings.Contains(strings.Join(call, " "), "5gws-bot.service") {
			t.Fatalf("restart command includes bot service: %v", call)
		}
	}
	if len(calls()) != 5 {
		t.Fatalf("restart call count = %d, want 5: %#v", len(calls()), calls())
	}
}

func TestConfigSummaryRedactsPassword(t *testing.T) {
	h, _ := testHandler()
	h.loadConfig = func() (config.Config, error) {
		tcp, udp := true, true
		return config.Config{
			Network: config.NetworkConfig{
				GatewayIP:         "10.0.0.1",
				InternalCIDR:      "172.22.0.0/16",
				IngressIface:      "eth0",
				HTTPRedirectPort:  18080,
				HTTPSRedirectPort: 18443,
				QUICRedirectPort:  18443,
			},
			DNS: config.DNSConfig{
				Binary:                   "smartdns",
				ListenUDP:                "127.0.0.1:1053",
				ListenTCP:                "127.0.0.1:1053",
				ListenDOT:                "127.0.0.1:1853",
				ListenPublicDOT:          "0.0.0.0:853",
				UpstreamsCN:              []string{"223.5.5.5"},
				UpstreamsOverseasPrivate: []string{"22.22.22.22"},
				UpstreamsOverseasPublic:  []string{"1.1.1.1"},
			},
			Exits: []config.ExitConfig{{
				Name:           "ss1",
				Type:           "shadowsocks-rust",
				Server:         "198.51.100.10",
				ServerPort:     8388,
				Method:         "aes-256-gcm",
				Password:       "secret",
				Username:       "user1",
				ListenAddress:  "127.0.0.1",
				ListenPort:     1080,
				TCP:            &tcp,
				UDP:            &udp,
				TimeoutSeconds: 300,
			}},
		}, nil
	}
	out := h.handleText("/config")
	if strings.Contains(out.Text, "secret") || strings.Contains(out.Text, "Password") || strings.Contains(out.Text, "password") {
		t.Fatalf("config summary leaked password:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "username=user1") {
		t.Fatalf("config summary missing non-secret exit fields:\n%s", out.Text)
	}
}

func TestRulesSummaryUsesLocalFileOnly(t *testing.T) {
	h, _ := testHandler()
	h.loadRules = func() (rules.File, error) {
		return rules.File{
			Imports: []rules.Import{{
				Name:    "gfw",
				Type:    "sing-box",
				URL:     "https://example.invalid/gfw.json",
				Exit:    "direct",
				DNSPool: "",
			}},
			Rules: []rules.Rule{{
				Name:         "openai",
				Exit:         "ss1",
				DomainSuffix: []string{"openai.com", "chatgpt.com"},
			}},
		}, nil
	}
	out := h.handleText("/rules")
	for _, want := range []string{"imports: 1", "inline rules: 1", "exit:direct: 1", "exit:ss1: 1"} {
		if !strings.Contains(out.Text, want) {
			t.Fatalf("rules summary missing %q:\n%s", want, out.Text)
		}
	}
}

func TestTruncateTextMarksOutput(t *testing.T) {
	got := truncateText(strings.Repeat("a", telegramMessageLimit+10))
	if len(got) > telegramMessageLimit {
		t.Fatalf("truncated text too long: %d", len(got))
	}
	if !strings.Contains(got, "[output truncated]") {
		t.Fatalf("truncated text missing marker: %q", got)
	}
}

func testHandler() (handler, func() [][]string) {
	var calls [][]string
	h := newHandler("/etc/5gws/config.toml", "/etc/5gws/rules.toml")
	h.runner = func(name string, args ...string) string {
		call := append([]string{name}, args...)
		calls = append(calls, call)
		return strings.Join(call, " ")
	}
	return h, func() [][]string {
		return calls
	}
}
