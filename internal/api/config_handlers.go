package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/pelletier/go-toml/v2"

	"github.com/morain/5gws/internal/engine"
	"github.com/morain/5gws/internal/ios"
	"github.com/morain/5gws/internal/rules"
	"github.com/morain/5gws/internal/store"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	active, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.Version, "active_revision": active.ID,
		"rules": len(active.Bundle.ResolvedRules), "processes": s.Supervisor.Status(),
	})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	items, err := s.Service.Store().Metrics(r.Context(), 360)
	if err != nil {
		writeError(w, err)
		return
	}
	metrics := make([]engine.Metrics, 0, len(items))
	for index := len(items) - 1; index >= 0; index-- {
		var metric engine.Metrics
		if err := json.Unmarshal(items[index], &metric); err != nil {
			writeError(w, err)
			return
		}
		metrics = append(metrics, metric)
	}
	writeJSON(w, http.StatusOK, map[string]any{"metrics": metrics})
}

func (s *Server) currentConfig(w http.ResponseWriter, r *http.Request) {
	active, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	active.Bundle.ResolvedRules = nil
	active.Bundle.Rules = rules.EnsureManaged(active.Bundle.Rules)
	writeJSON(w, http.StatusOK, active.Bundle)
}

func (s *Server) defaultRules(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, rules.ManagedFile())
}

func (s *Server) validateConfig(w http.ResponseWriter, r *http.Request) {
	var bundle store.Bundle
	if !decodeJSON(w, r, &bundle) {
		return
	}
	result, err := s.Service.ValidateBundle(r.Context(), bundle)
	respond(w, map[string]any{"rule_count": result.RuleCount, "warnings": result.Warnings}, err)
}

func (s *Server) applyConfig(w http.ResponseWriter, r *http.Request) {
	var bundle store.Bundle
	if !decodeJSON(w, r, &bundle) {
		return
	}
	operation, created, err := s.applyCoordinator().begin(r.Header.Get(applyOperationHeader), bundle)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errApplyInProgress) {
			status = http.StatusConflict
		}
		writeStatusError(w, status, err.Error())
		return
	}
	status := http.StatusOK
	if created || !terminalApplyStatus(operation.Status) {
		status = http.StatusAccepted
	}
	writeJSON(w, status, operation)
}

func (s *Server) applyConfigStatus(w http.ResponseWriter, r *http.Request) {
	operation, err := s.applyCoordinator().status(chi.URLParam(r, "id"))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errApplyOperationUnknown) {
			status = http.StatusNotFound
		}
		writeStatusError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
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
	bundle.ApplyDefaults()
	if err := bundle.Config.Validate(); err != nil {
		writeStatusError(w, http.StatusBadRequest, err.Error())
		return
	}
	bundle.ResolvedRules = nil
	bundle.Rules = rules.EnsureManaged(bundle.Rules)
	writeJSON(w, http.StatusOK, bundle)
}

func (s *Server) active(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Active(r.Context())
	respond(w, revision, err)
}

func (s *Server) draft(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Draft(r.Context())
	revision.Bundle.ResolvedRules = nil
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

func (s *Server) exportBackup(w http.ResponseWriter, r *http.Request) {
	revision, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	revision.Bundle.ResolvedRules = nil
	revision.Bundle.Rules = rules.EnsureManaged(revision.Bundle.Rules)
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
	writeJSON(w, http.StatusOK, ios.BuildLinks(revision.Bundle.Config))
}

func (s *Server) iosProfileFile(w http.ResponseWriter, r *http.Request) {
	revision, ok := s.activeIOS(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="5gws-dot.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(ios.Profile(revision.Bundle.Config))
}

func (s *Server) iosProfileQR(w http.ResponseWriter, r *http.Request) {
	revision, ok := s.activeIOS(w, r)
	if !ok {
		return
	}
	data, err := ios.QRCode(revision.Bundle.Config)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) activeIOS(w http.ResponseWriter, r *http.Request) (store.Revision, bool) {
	revision, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return store.Revision{}, false
	}
	if !revision.Bundle.Config.IOS.Enabled {
		http.NotFound(w, r)
		return store.Revision{}, false
	}
	return revision, true
}
