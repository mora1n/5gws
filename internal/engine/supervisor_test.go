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

func TestReadinessIncludesTCPGateway(t *testing.T) {
	bundle := store.Bundle{Config: config.Config{Network: config.NetworkConfig{HTTPRedirectPort: 18080, HTTPSRedirectPort: 18443, TCPRedirectPort: 18082}, DNS: config.DNSConfig{ListenTCP: "0.0.0.0:1053"}}}
	addresses := readinessAddresses(bundle)
	want := "127.0.0.1:18082"
	if !slices.Contains(addresses, want) {
		t.Fatalf("readiness addresses=%v, missing %s", addresses, want)
	}
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
