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

func New(state *store.Store, supervisor Runtime) *Service {
	return &Service{store: state, supervisor: supervisor}
}

func (s *Service) Store() *store.Store { return s.store }

func (s *Service) Draft(ctx context.Context) (store.Revision, error) {
	return s.store.Draft(ctx)
}

func (s *Service) Active(ctx context.Context) (store.Revision, error) {
	return s.store.Active(ctx)
}

func (s *Service) SaveDraft(ctx context.Context, bundle store.Bundle) (store.Revision, error) {
	bundle.ResolvedRules = nil
	return s.store.SaveDraft(ctx, bundle)
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
	root, err := s.supervisor.Prepare(ctx, prepared.ID, prepared.Bundle)
	if err != nil {
		_ = s.store.Fail(ctx, prepared.ID, err)
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
	if err := s.store.Activate(ctx, validated.RevisionID); err != nil {
		oldRoot := filepath.Join(active.Bundle.Config.System.StateDir, "revisions", fmt.Sprint(active.ID))
		if rollbackErr := s.supervisor.Apply(ctx, oldRoot, active.Bundle); rollbackErr != nil {
			return store.Revision{}, errors.Join(err, fmt.Errorf("runtime rollback: %w", rollbackErr))
		}
		return store.Revision{}, err
	}
	return s.store.Active(ctx)
}

func (s *Service) Rollback(ctx context.Context, revisionID int64) (store.Revision, error) {
	if _, err := s.store.DraftFromRevision(ctx, revisionID); err != nil {
		return store.Revision{}, err
	}
	return s.Apply(ctx)
}
