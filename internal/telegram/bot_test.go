package telegram

import (
	"errors"
	"os"
	"path/filepath"
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

func TestGroupMessagesRequireExplicitMention(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
		ok   bool
	}{
		{name: "plain text", text: "hello", ok: false},
		{name: "bare command", text: "/status", ok: false},
		{name: "other bot command", text: "/status@otherbot", ok: false},
		{name: "command mention", text: "/status@fivegwsbot", want: "/status", ok: true},
		{name: "leading mention", text: "@fivegwsbot /doctor", want: "/doctor", ok: true},
		{name: "mention only", text: "@fivegwsbot", want: "/menu", ok: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := testTelegramMessage("supergroup", tc.text)
			got, ok := messageCommandText(msg, "FiveGWSBot")
			if ok != tc.ok || got != tc.want {
				t.Fatalf("messageCommandText = %q/%t, want %q/%t", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestPrivateMessageDoesNotRequireMention(t *testing.T) {
	got, ok := messageCommandText(testTelegramMessage("private", "/status"), "fivegwsbot")
	if !ok || got != "/status" {
		t.Fatalf("private message = %q/%t, want /status/true", got, ok)
	}
}

func TestSendValuesIncludesMessageThread(t *testing.T) {
	values, err := sendValues(100, 200, "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("message_thread_id") != "200" {
		t.Fatalf("message_thread_id = %q, want 200", values.Get("message_thread_id"))
	}
}

func TestMenuHidesDangerousOperations(t *testing.T) {
	text := menuText()
	keyboard := menuKeyboard()
	if strings.Contains(text, "/apply") || strings.Contains(text, "/restart") {
		t.Fatalf("menu text exposes hidden operations:\n%s", text)
	}
	for _, row := range keyboard.InlineKeyboard {
		for _, button := range row {
			if strings.Contains(button.CallbackData, "apply") || strings.Contains(button.CallbackData, "restart") {
				t.Fatalf("menu exposes dangerous callback: %#v", button)
			}
		}
	}
}

func TestRuleAddAndDeleteManagedRule(t *testing.T) {
	h := testRuleHandler(t, nil)
	added := h.handleText("/rule_add example.com direct")
	if !strings.Contains(added.Text, "added rule tg_example_com_direct") || added.Markup == nil {
		t.Fatalf("add output missing confirmation:\n%#v", added)
	}
	data := readText(t, h.rulesPath)
	for _, want := range []string{
		managedRulesBegin,
		`name = "tg_example_com_direct"`,
		`exit = "direct"`,
		`domain_suffix = ["example.com"]`,
		`name = "manual"`,
	} {
		if !strings.Contains(data, want) {
			t.Fatalf("rules.toml missing %q:\n%s", want, data)
		}
	}
	list := h.handleText("/rule_list")
	if !strings.Contains(list.Text, "tg_example_com_direct") {
		t.Fatalf("rule list missing managed rule:\n%s", list.Text)
	}
	deleted := h.handleText("/rule_del tg_example_com_direct")
	if !strings.Contains(deleted.Text, "deleted rule tg_example_com_direct") {
		t.Fatalf("delete output missing rule:\n%s", deleted.Text)
	}
	data = readText(t, h.rulesPath)
	if strings.Contains(data, managedRulesBegin) || !strings.Contains(data, `name = "manual"`) {
		t.Fatalf("managed block not removed or manual rule lost:\n%s", data)
	}
	backups, err := filepath.Glob(h.rulesPath + ".botbak-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) < 2 {
		t.Fatalf("backup count = %d, want at least 2", len(backups))
	}
}

func TestRuleAddSupportsDNSPoolTarget(t *testing.T) {
	h := testRuleHandler(t, nil)
	out := h.handleText("/rule_add speedtest.net pool:overseas_private")
	if !strings.Contains(out.Text, "added rule tg_speedtest_net_pool_overseas_private") {
		t.Fatalf("add pool output:\n%s", out.Text)
	}
	data := readText(t, h.rulesPath)
	if !strings.Contains(data, `dns_pool = "overseas_private"`) {
		t.Fatalf("rules.toml missing dns_pool:\n%s", data)
	}
}

func TestRuleAddRestoresBackupWhenDoctorFails(t *testing.T) {
	h := testRuleHandler(t, errors.New("doctor failed"))
	before := readText(t, h.rulesPath)
	out := h.handleText("/rule_add example.com direct")
	if !strings.Contains(out.Text, "doctor failed") {
		t.Fatalf("expected doctor failure:\n%s", out.Text)
	}
	after := readText(t, h.rulesPath)
	if after != before {
		t.Fatalf("rules.toml was not restored:\nbefore:\n%s\nafter:\n%s", before, after)
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
				ListenUDP:                "0.0.0.0:1053",
				ListenTCP:                "0.0.0.0:1053",
				ListenDOT:                "0.0.0.0:1853",
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

func testTelegramMessage(chatType, text string) telegramMessage {
	msg := telegramMessage{Text: text}
	msg.Chat.Type = chatType
	return msg
}

func testRuleHandler(t *testing.T, checkErr error) handler {
	t.Helper()
	dir := t.TempDir()
	rulesPath := filepath.Join(dir, "rules.toml")
	if err := os.WriteFile(rulesPath, []byte(`[[rules]]
name = "manual"
exit = "direct"
domain_suffix = ["manual.example"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newHandler(filepath.Join(dir, "config.toml"), rulesPath)
	h.loadConfig = func() (config.Config, error) {
		return config.Config{Exits: []config.ExitConfig{{Name: "direct", Type: "direct"}}}, nil
	}
	h.checkedRunner = func(name string, args ...string) (string, error) {
		if checkErr != nil {
			return "doctor output", checkErr
		}
		return "ok", nil
	}
	return h
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
