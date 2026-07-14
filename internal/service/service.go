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
	ctx        context.Context
	store      *store.Store
	supervisor Runtime
	resolver   RuleResolver
	applyMu    sync.Mutex
	stateMu    sync.RWMutex
	active     store.Revision
	draft      store.Revision
}

type Options struct {
	Context  context.Context
	Store    *store.Store
	Runtime  Runtime
	Resolver RuleResolver
	Active   store.Revision
	Draft    store.Revision
}

type RuleResolver interface {
	Normalize(context.Context, rules.File) (rules.Normalized, error)
}

type Runtime interface {
	Preflight(context.Context, store.Bundle) error
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

type ApplyResult struct {
	Changed    bool            `json:"changed"`
	RevisionID int64           `json:"revision_id"`
	RuleCount  int             `json:"rule_count"`
	Warnings   []rules.Warning `json:"warnings"`
}

func New(opts Options) *Service {
	resolver := opts.Resolver
	if resolver == nil {
		resolver = rules.Resolver{Cache: opts.Store}
	}
	return &Service{
		ctx: opts.Context, store: opts.Store, supervisor: opts.Runtime,
		resolver: resolver, active: opts.Active, draft: opts.Draft,
	}
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
	validation, err := s.ValidateBundle(ctx, draft.Bundle)
	validation.RevisionID = draft.ID
	return validation, err
}

func (s *Service) ValidateBundle(ctx context.Context, bundle store.Bundle) (Validation, error) {
	resolved, warnings, err := s.resolveBundle(ctx, bundle)
	if err != nil {
		return Validation{}, err
	}
	if err := s.supervisor.Preflight(ctx, resolved); err != nil {
		return Validation{}, err
	}
	return Validation{RuleCount: len(resolved.ResolvedRules), Warnings: warnings, Bundle: resolved}, nil
}

func (s *Service) resolveBundle(ctx context.Context, bundle store.Bundle) (store.Bundle, []rules.Warning, error) {
	bundle.ApplyDefaults()
	if err := bundle.Config.Validate(); err != nil {
		return store.Bundle{}, nil, err
	}
	if err := rules.ValidateManaged(bundle.Rules); err != nil {
		return store.Bundle{}, nil, err
	}
	if err := rules.ValidateDNSPoolReferences(bundle.Rules, bundle.Config.DNS.PoolNames()); err != nil {
		return store.Bundle{}, nil, err
	}
	norm, err := s.resolver.Normalize(ctx, bundle.Rules)
	if err != nil {
		return store.Bundle{}, nil, err
	}
	if _, err := rules.Compile(norm); err != nil {
		return store.Bundle{}, nil, err
	}
	bundle.ResolvedRules = norm.Rules
	return bundle, norm.Warnings, nil
}

func (s *Service) ApplyBundle(bundle store.Bundle) (ApplyResult, error) {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	ctx := s.ctx
	active, err := s.store.Active(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := rules.ValidateManaged(bundle.Rules); err != nil {
		return ApplyResult{}, err
	}
	if active.Bundle.SameInput(bundle) && active.Bundle.ResolvedLocalRulesCurrent() {
		reset, err := s.store.ResetDraftToActive(ctx)
		if err != nil {
			return ApplyResult{}, err
		}
		if err := s.store.PruneRevisions(ctx); err != nil {
			return ApplyResult{}, err
		}
		if err := PruneRevisionDirs(active.Bundle.Config.System.StateDir, active.ID); err != nil {
			return ApplyResult{}, err
		}
		s.setActiveAndDraft(reset)
		return ApplyResult{Changed: false, RevisionID: active.ID, RuleCount: len(active.Bundle.ResolvedRules)}, nil
	}
	resolved, warnings, err := s.resolveBundle(ctx, bundle)
	if err != nil {
		return ApplyResult{}, err
	}
	candidate, err := s.store.SaveDraft(ctx, resolved)
	if err != nil {
		return ApplyResult{}, err
	}
	s.setDraft(candidate)
	root, err := s.supervisor.Prepare(ctx, candidate.ID, candidate.Bundle)
	if err == nil {
		err = s.supervisor.Apply(ctx, root, candidate.Bundle)
	}
	if err != nil {
		failErr := s.store.Fail(ctx, candidate.ID, err)
		restoreErr := s.restoreActiveDraft(ctx)
		return ApplyResult{}, errors.Join(
			err,
			wrapError("mark candidate failed", failErr),
			wrapError("restore active revision", restoreErr),
		)
	}
	activated, err := s.store.Activate(ctx, candidate.ID)
	if err != nil {
		oldRoot := filepath.Join(active.Bundle.Config.System.StateDir, "revisions", fmt.Sprint(active.ID))
		rollbackErr := s.supervisor.Apply(ctx, oldRoot, active.Bundle)
		restoreErr := s.restoreActiveDraft(ctx)
		return ApplyResult{}, errors.Join(
			err,
			wrapError("runtime rollback", rollbackErr),
			wrapError("restore active revision", restoreErr),
		)
	}
	s.setActiveAndDraft(activated)
	if err := s.store.PruneRevisions(ctx); err != nil {
		return ApplyResult{}, err
	}
	if err := PruneRevisionDirs(activated.Bundle.Config.System.StateDir, activated.ID); err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Changed: true, RevisionID: activated.ID, RuleCount: len(activated.Bundle.ResolvedRules), Warnings: warnings}, nil
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (s *Service) restoreActiveDraft(ctx context.Context) error {
	active, err := s.store.ResetDraftToActive(ctx)
	if err != nil {
		return err
	}
	s.setActiveAndDraft(active)
	if err := s.store.PruneRevisions(ctx); err != nil {
		return err
	}
	return PruneRevisionDirs(active.Bundle.Config.System.StateDir, active.ID)
}

func (s *Service) Apply(ctx context.Context) (store.Revision, error) {
	draft, err := s.store.Draft(ctx)
	if err != nil {
		return store.Revision{}, err
	}
	if _, err := s.ApplyBundle(draft.Bundle); err != nil {
		return store.Revision{}, err
	}
	return s.Active(ctx)
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
