package api

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/service"
	"github.com/morain/5gws/internal/store"
)

const applyOperationHeader = "X-5gws-Operation-ID"

var (
	errApplyInProgress       = errors.New("configuration apply already in progress")
	errApplyOperationUnknown = errors.New("configuration apply operation not found")
)

type configApplier interface {
	ApplyBundle(store.Bundle) (service.ApplyResult, error)
}

type ApplyOperation struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Changed    bool            `json:"changed"`
	RevisionID int64           `json:"revision_id"`
	RuleCount  int             `json:"rule_count"`
	Warnings   []rules.Warning `json:"warnings"`
	Error      string          `json:"error,omitempty"`
	QueuedAt   time.Time       `json:"queued_at"`
	StartedAt  *time.Time      `json:"started_at,omitempty"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
}

type applyCoordinator struct {
	mu      sync.RWMutex
	applier configApplier
	latest  *ApplyOperation
}

func newApplyCoordinator(applier configApplier) *applyCoordinator {
	return &applyCoordinator{applier: applier}
}

func (c *applyCoordinator) begin(id string, bundle store.Bundle) (ApplyOperation, bool, error) {
	if _, err := uuid.Parse(id); err != nil {
		return ApplyOperation{}, false, err
	}
	c.mu.Lock()
	if c.latest != nil && c.latest.ID == id {
		operation := *c.latest
		c.mu.Unlock()
		return operation, false, nil
	}
	if c.latest != nil && !terminalApplyStatus(c.latest.Status) {
		c.mu.Unlock()
		return ApplyOperation{}, false, errApplyInProgress
	}
	operation := &ApplyOperation{ID: id, Status: "queued", QueuedAt: time.Now().UTC()}
	c.latest = operation
	snapshot := *operation
	c.mu.Unlock()
	go c.run(id, bundle)
	return snapshot, true, nil
}

func (c *applyCoordinator) status(id string) (ApplyOperation, error) {
	if _, err := uuid.Parse(id); err != nil {
		return ApplyOperation{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.latest == nil || c.latest.ID != id {
		return ApplyOperation{}, errApplyOperationUnknown
	}
	return *c.latest, nil
}

func (c *applyCoordinator) run(id string, bundle store.Bundle) {
	started := time.Now().UTC()
	c.mu.Lock()
	c.latest.Status = "running"
	c.latest.StartedAt = &started
	c.mu.Unlock()

	result, err := c.applier.ApplyBundle(bundle)
	finished := time.Now().UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.latest.Status = "failed"
		c.latest.Error = err.Error()
		log.Printf("configuration apply %s failed: %v", id, err)
	} else {
		c.latest.Status = "succeeded"
		c.latest.Changed = result.Changed
		c.latest.RevisionID = result.RevisionID
		c.latest.RuleCount = result.RuleCount
		c.latest.Warnings = result.Warnings
	}
	c.latest.FinishedAt = &finished
}

func terminalApplyStatus(status string) bool {
	return status == "succeeded" || status == "failed"
}
