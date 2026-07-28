package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/morain/5gws/internal/config"
	"github.com/morain/5gws/internal/store"
)

func TestWatchReportsUnexpectedExit(t *testing.T) {
	group := testProcessGroup()
	supervisor := NewSupervisor(context.Background(), t.TempDir(), NewLogBuffer(1024))
	supervisor.current = group
	go supervisor.watch(group)

	group.done <- errors.New("child exited")
	select {
	case err := <-supervisor.Fatal():
		if err == nil || err.Error() != "child exited" {
			t.Fatalf("unexpected fatal error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unexpected child exit was not reported")
	}
}

func TestManagedCommandsUseSingleHAProxyProcess(t *testing.T) {
	bundle := store.Bundle{Config: config.Config{DNS: config.DNSConfig{Binary: "smartdns"}}}
	commands := managedCommands("/state/revision", bundle)
	want := []string{"haproxy", "-db", "-f", "/state/revision/haproxy/haproxy.cfg"}
	if len(commands) != 2 || !reflect.DeepEqual(commands[1], want) {
		t.Fatalf("managed commands = %#v, want HAProxy command %#v", commands, want)
	}
}

func TestReadinessTimeoutCoversSlowSmartDNSStartup(t *testing.T) {
	if readinessTimeout < 15*time.Second {
		t.Fatalf("readiness timeout = %s, want at least 15s", readinessTimeout)
	}
}

func TestReadinessExcludesTCPGateway(t *testing.T) {
	bundle := store.Bundle{Config: config.Config{Network: config.NetworkConfig{HTTPRedirectPort: 18080, HTTPSRedirectPort: 18443, TCPRedirectPort: 18082}, DNS: config.DNSConfig{ListenTCP: "0.0.0.0:1053"}}}
	addresses := readinessAddresses(bundle)
	if unwanted := "127.0.0.1:18082"; slices.Contains(addresses, unwanted) {
		t.Fatalf("readiness addresses=%v, unexpectedly contains %s", addresses, unwanted)
	}
	for _, want := range []string{"127.0.0.1:1053", "127.0.0.1:18080", "127.0.0.1:18443"} {
		if !slices.Contains(addresses, want) {
			t.Fatalf("readiness addresses=%v, missing %s", addresses, want)
		}
	}
}

func TestWaitReadiness(t *testing.T) {
	t.Run("gateway ready", func(t *testing.T) {
		ready := make(chan struct{}, 1)
		ready <- struct{}{}
		err := waitReadiness(context.Background(), make(chan error), func(ctx context.Context) error {
			select {
			case <-ready:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil {
			t.Fatalf("waitReadiness() error = %v", err)
		}
	})

	t.Run("startup error", func(t *testing.T) {
		want := errors.New("gateway bind failed")
		done := make(chan error, 1)
		done <- want
		err := waitReadiness(context.Background(), done, func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, want) {
			t.Fatalf("waitReadiness() error = %v, want %v", err, want)
		}
	})

	t.Run("context canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := waitReadiness(ctx, make(chan error), func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waitReadiness() error = %v, want context canceled", err)
		}
	})

	t.Run("context deadline exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		err := waitReadiness(ctx, make(chan error), func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waitReadiness() error = %v, want context deadline exceeded", err)
		}
	})
}

func TestWatchIgnoresPlannedStop(t *testing.T) {
	group := testProcessGroup()
	supervisor := NewSupervisor(context.Background(), t.TempDir(), NewLogBuffer(1024))
	supervisor.current = group
	go supervisor.watch(group)
	close(group.stopped)

	select {
	case err := <-supervisor.Fatal():
		t.Fatalf("planned stop reported as fatal: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}

func testProcessGroup() *processGroup {
	_, cancel := context.WithCancel(context.Background())
	return &processGroup{
		cancel:  cancel,
		done:    make(chan error, 1),
		stopped: make(chan struct{}),
	}
}
