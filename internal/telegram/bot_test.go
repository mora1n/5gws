package telegram

import (
	"strings"
	"testing"
)

func TestHandleIOSUsesNoQR(t *testing.T) {
	var got []string
	out := handleWithRunner("/ios", func(args ...string) string {
		got = append([]string{}, args...)
		return strings.Join(args, " ")
	})
	if out != "ios-link --no-qr" {
		t.Fatalf("unexpected output: %q", out)
	}
	if strings.Join(got, " ") != "ios-link --no-qr" {
		t.Fatalf("unexpected command: %v", got)
	}
}
