package ssrust

import (
	"encoding/json"
	"testing"

	"github.com/morain/5gws/internal/config"
)

func TestConfigRendersSupportedFieldsOnly(t *testing.T) {
	password := "MTIzNDU2Nzg5MGFiY2RlZg==:ZmVkY2JhMDk4NzY1NDMyMQ=="
	out, err := Config(config.ExitConfig{
		Name:           "ss1",
		Type:           "shadowsocks-rust",
		Server:         "198.51.100.10",
		ServerPort:     8388,
		Method:         "2022-blake3-aes-128-gcm",
		Password:       password,
		Username:       "default",
		ListenAddress:  "127.0.0.1",
		ListenPort:     1080,
		TimeoutSeconds: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["username"] != nil {
		t.Fatalf("username must not be written to shadowsocks-rust JSON: %s", out)
	}
	if parsed["method"] != "2022-blake3-aes-128-gcm" {
		t.Fatalf("method not rendered: %#v", parsed)
	}
	if parsed["password"] != password {
		t.Fatalf("password chain not preserved: %#v", parsed)
	}
	if parsed["mode"] != "tcp_and_udp" {
		t.Fatalf("mode not rendered: %#v", parsed)
	}
	if parsed["local_address"] != "127.0.0.1" || parsed["local_port"] != float64(1080) {
		t.Fatalf("local listen fields not rendered: %#v", parsed)
	}
	if parsed["timeout"] != float64(300) {
		t.Fatalf("timeout not rendered in seconds: %#v", parsed)
	}
}

func TestRuntimeNamesUseSafeIDsForComplexExitNames(t *testing.T) {
	exit := config.ExitConfig{Name: "🇯🇵 Tokyo SS"}
	want := config.ExitID(exit.Name)
	if got := ConfigFileName(exit); got != want+".json" {
		t.Fatalf("config file name = %q, want %q", got, want+".json")
	}
	if got := ServiceName(exit); got != "5gws-ssrust-"+want+".service" {
		t.Fatalf("service name = %q, want safe ID %q", got, want)
	}
}
