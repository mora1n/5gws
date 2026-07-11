package quic

import (
	"io"
	"net"
	"testing"
)

func TestWriteInitialForwardsAllBytes(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	payload := []byte("client hello")
	done := make(chan error, 1)
	go func() {
		defer client.Close()
		n, err := writeInitial(client, payload)
		if err == nil && n != int64(len(payload)) {
			t.Errorf("writeInitial bytes = %d, want %d", n, len(payload))
		}
		done <- err
	}()

	got := make([]byte, len(payload))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
