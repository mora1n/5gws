package engine

import (
	"strings"
	"testing"
	"time"
)

func TestLogBufferWrapsAndTailsLines(t *testing.T) {
	buffer := NewLogBuffer(15)
	_, _ = buffer.Write([]byte("one\ntwo\n"))
	_, _ = buffer.Write([]byte("three\nfour\n"))

	if got, want := buffer.Tail(2), "three\nfour\n"; got != want {
		t.Fatalf("Tail(2) = %q, want %q", got, want)
	}
	if got, want := buffer.Tail(0), "two\nthree\nfour\n"; got != want {
		t.Fatalf("Tail(0) = %q, want %q", got, want)
	}
}

func TestLogBufferKeepsNewestOversizedWrite(t *testing.T) {
	buffer := NewLogBuffer(8)
	payload := []byte("0123456789")
	n, err := buffer.Write(payload)
	if err != nil || n != len(payload) {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if got, want := buffer.Tail(0), "23456789"; got != want {
		t.Fatalf("Tail(0) = %q, want %q", got, want)
	}
}

func TestLogBufferBroadcastsToEverySubscriber(t *testing.T) {
	buffer := NewLogBuffer(1024)
	first, cancelFirst := buffer.Subscribe()
	second, cancelSecond := buffer.Subscribe()
	defer cancelSecond()
	_, _ = buffer.Write([]byte("ready\n"))
	for index, subscriber := range []<-chan uint64{first, second} {
		select {
		case sequence := <-subscriber:
			if sequence != 1 {
				t.Fatalf("subscriber %d sequence=%d", index, sequence)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive update", index)
		}
	}
	cancelFirst()
	_, _ = buffer.Write([]byte("running\n"))
	select {
	case sequence := <-second:
		if sequence != 2 {
			t.Fatalf("second sequence=%d", sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("remaining subscriber did not receive update")
	}
}

func BenchmarkLogBufferWriteFull(b *testing.B) {
	buffer := NewLogBuffer(2 << 20)
	line := []byte(strings.Repeat("x", 160) + "\n")
	_, _ = buffer.Write(make([]byte, 2<<20))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = buffer.Write(line)
	}
}
