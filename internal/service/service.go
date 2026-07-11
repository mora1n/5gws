package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/store"
)

type Service struct {
	store      *store.Store
	supervisor Runtime
	applyMu    sync.Mutex
	stateMu    sync.RWMutex
	active     store.Revision
	draft      store.Revision
}

type Runtime interface {
	Prepare(context.Context, int64, store.Bundle) (string, error)
	Apply(context.Context, string, store.Bundle) error
}

type Validation struct {
	RevisionID int64           `json:"revision_id"`
	RuleCount  int             `json:"rule_count"`
	Warnings   []rules.Warning `json:"warnings"`
	Root       string          `json:"-"`
	Bundle     store.Bundle    `json:"-"`
}

func New(state *store.Store, supervisor Runtime, active, draft store.Revision) *Service {
	return &Service{store: state, supervisor: supervisor, active: active, draft: draft}
}

func (s *Service) Store() *store.Store { return s.store }

func (s *Service) Draft(ctx context.Context) (store.Revision, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.draft, nil
}

func (s *Service) Active(ctx context.Context) (store.Revision, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.active, nil
}

func (s *Service) SaveDraft(ctx context.Context, bundle store.Bundle) (store.Revision, error) {
	bundle.ResolvedRules = nil
	revision, err := s.store.SaveDraft(ctx, bundle)
	if err == nil {
		s.setDraft(revision)
	}
	return revision, err
}

func (s *Service) ValidateDraft(ctx context.Context) (Validation, error) {
	draft, err := s.store.Draft(ctx)
	if err != nil {
		return Validation{}, err
	}
	norm, err := (rules.Resolver{Cache: s.store}).Normalize(ctx, draft.Bundle.Rules)
	if err != nil {
		return Validation{}, err
	}
	if _, err := rules.Compile(norm); err != nil {
		return Validation{}, err
	}
	draft.Bundle.ResolvedRules = norm.Rules
	prepared, err := s.store.SaveDraft(ctx, draft.Bundle)
	if err != nil {
		return Validation{}, err
	}
	s.setDraft(prepared)
	root, err := s.supervisor.Prepare(ctx, prepared.ID, prepared.Bundle)
	if err != nil {
		_ = s.store.Fail(ctx, prepared.ID, err)
		prepared.Status = "failed"
		prepared.Error = err.Error()
		s.setDraft(prepared)
		return Validation{}, err
	}
	return Validation{RevisionID: prepared.ID, RuleCount: len(norm.Rules), Warnings: norm.Warnings, Root: root, Bundle: prepared.Bundle}, nil
}

func (s *Service) Apply(ctx context.Context) (store.Revision, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	active, err := s.store.Active(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	validated, err := s.ValidateDraft(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	if err := s.supervisor.Apply(ctx, validated.Root, validated.Bundle); err != nil {
		_ = s.store.Fail(ctx, validated.RevisionID, err)
		return store.Revision{}, err
	}
	activated, err := s.store.Activate(ctx, validated.RevisionID)
	if err != nil {
		oldRoot := filepath.Join(active.Bundle.Config.System.StateDir, "revisions", fmt.Sprint(active.ID))
		if rollbackErr := s.supervisor.Apply(ctx, oldRoot, active.Bundle); rollbackErr != nil {
			return store.Revision{}, errors.Join(err, fmt.Errorf("runtime rollback: %w", rollbackErr))
		}
		return store.Revision{}, err
	}
	s.setActiveAndDraft(activated)
	return activated, nil
}

func (s *Service) Rollback(ctx context.Context, revisionID int64) (store.Revision, error) {
	draft, err := s.store.DraftFromRevision(ctx, revisionID)
	if err != nil {
		return store.Revision{}, err
	}
	s.setDraft(draft)
	return s.Apply(ctx)
}

func (s *Service) setDraft(revision store.Revision) {
	s.stateMu.Lock()
	s.draft = revision
	s.stateMu.Unlock()
}

func (s *Service) setActiveAndDraft(revision store.Revision) {
	s.stateMu.Lock()
	s.active = revision
	s.draft = revision
	s.stateMu.Unlock()
}
