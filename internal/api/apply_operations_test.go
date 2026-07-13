package api

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/morain/5gws/internal/service"
	"github.com/morain/5gws/internal/store"
)

type blockingApplier struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	result  service.ApplyResult
	err     error
}

func (a *blockingApplier) ApplyBundle(_ store.Bundle) (service.ApplyResult, error) {
	a.mu.Lock()
	a.calls++
	if a.calls == 1 {
		close(a.started)
	}
	a.mu.Unlock()
	<-a.release
	return a.result, a.err
}

func (a *blockingApplier) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func TestApplyCoordinatorIsIdempotentAndRejectsConcurrentOperation(t *testing.T) {
	applier := &blockingApplier{
		started: make(chan struct{}), release: make(chan struct{}),
		result: service.ApplyResult{Changed: true, RevisionID: 2, RuleCount: 3},
	}
	coordinator := newApplyCoordinator(applier)
	id := uuid.NewString()
	if operation, created, err := coordinator.begin(id, store.Bundle{}); err != nil || !created || operation.Status != "queued" {
		t.Fatalf("begin=%+v created=%v err=%v", operation, created, err)
	}
	select {
	case <-applier.started:
	case <-time.After(time.Second):
		t.Fatal("apply did not start")
	}
	if _, created, err := coordinator.begin(id, store.Bundle{}); err != nil || created {
		t.Fatalf("idempotent begin created=%v err=%v", created, err)
	}
	if _, _, err := coordinator.begin(uuid.NewString(), store.Bundle{}); !errors.Is(err, errApplyInProgress) {
		t.Fatalf("concurrent begin error=%v", err)
	}
	if applier.callCount() != 1 {
		t.Fatalf("apply calls=%d", applier.callCount())
	}
	close(applier.release)
	operation := waitCoordinator(t, coordinator, id)
	if operation.Status != "succeeded" || !operation.Changed || operation.RevisionID != 2 {
		t.Fatalf("operation=%+v", operation)
	}
}

func TestApplyCoordinatorReportsFailureAndValidatesIDs(t *testing.T) {
	applier := &blockingApplier{
		started: make(chan struct{}), release: make(chan struct{}), err: errors.New("runtime failed"),
	}
	coordinator := newApplyCoordinator(applier)
	if _, _, err := coordinator.begin("bad-id", store.Bundle{}); err == nil {
		t.Fatal("invalid operation ID accepted")
	}
	id := uuid.NewString()
	if _, _, err := coordinator.begin(id, store.Bundle{}); err != nil {
		t.Fatal(err)
	}
	<-applier.started
	close(applier.release)
	operation := waitCoordinator(t, coordinator, id)
	if operation.Status != "failed" || operation.Error != "runtime failed" {
		t.Fatalf("operation=%+v", operation)
	}
	if _, err := coordinator.status(uuid.NewString()); !errors.Is(err, errApplyOperationUnknown) {
		t.Fatalf("unknown status error=%v", err)
	}
}

func waitCoordinator(t *testing.T, coordinator *applyCoordinator, id string) ApplyOperation {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		operation, err := coordinator.status(id)
		if err != nil {
			t.Fatal(err)
		}
		if terminalApplyStatus(operation.Status) {
			return operation
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("operation did not finish")
	return ApplyOperation{}
}
