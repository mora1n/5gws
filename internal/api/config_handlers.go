package api

import (
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/pelletier/go-toml/v2"

	"github.com/morain/5gws/internal/ios"
	"github.com/morain/5gws/internal/store"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	active, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	draft, err := s.Service.Draft(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.Version, "active_revision": active.ID, "draft_revision": draft.ID,
		"dirty": active.ID != draft.ID, "rules": len(active.Bundle.ResolvedRules), "processes": s.Supervisor.Status(),
	})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	items, err := s.Service.Store().Metrics(r.Context(), 360)
	respond(w, map[string]any{"metrics": items}, err)
}

func (s *Server) active(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Active(r.Context())
	respond(w, revision, err)
}

func (s *Server) draft(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Draft(r.Context())
	respond(w, revision, err)
}

func (s *Server) saveDraft(w http.ResponseWriter, r *http.Request) {
	var bundle store.Bundle
	if !decodeJSON(w, r, &bundle) {
		return
	}
	revision, err := s.Service.SaveDraft(r.Context(), bundle)
	respond(w, revision, err)
}

func (s *Server) validateDraft(w http.ResponseWriter, r *http.Request) {
	result, err := s.Service.ValidateDraft(r.Context())
	respond(w, result, err)
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Apply(r.Context())
	respond(w, revision, err)
}

func (s *Server) revisions(w http.ResponseWriter, r *http.Request) {
	items, err := s.Service.Store().Revisions(r.Context(), 100)
	respond(w, map[string]any{"revisions": items}, err)
}

func (s *Server) rollback(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeStatusError(w, http.StatusBadRequest, "invalid revision id")
		return
	}
	revision, err := s.Service.Rollback(r.Context(), id)
	respond(w, revision, err)
}

func (s *Server) exportBackup(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	revision.Bundle.ResolvedRules = nil
	data, err := toml.Marshal(revision.Bundle)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/toml")
	w.Header().Set("Content-Disposition", `attachment; filename="5gws-backup.toml"`)
	_, _ = w.Write(data)
}

func (s *Server) importBackup(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeError(w, err)
		return
	}
	var bundle store.Bundle
	if err := toml.Unmarshal(data, &bundle); err != nil {
		writeStatusError(w, http.StatusBadRequest, err.Error())
		return
	}
	revision, err := s.Service.SaveDraft(r.Context(), bundle)
	respond(w, revision, err)
}

func (s *Server) iosProfile(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	links, err := ios.Generate(revision.Bundle.Config.DNS.CertDir, revision.Bundle.Config)
	respond(w, links, err)
}
