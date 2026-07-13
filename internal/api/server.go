package api

import (
	"io/fs"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/morain/5gws/internal/auth"
	"github.com/morain/5gws/internal/diagnostics"
	"github.com/morain/5gws/internal/engine"
	"github.com/morain/5gws/internal/service"
	"github.com/morain/5gws/internal/updater"
)

const sessionCookie = "5gws_session"

type Server struct {
	Service     *service.Service
	Auth        *auth.Manager
	Supervisor  *engine.Supervisor
	Web         fs.FS
	Version     string
	Updater     *updater.Client
	Diagnostics diagnostics.Runner
	loginMu     sync.Mutex
	logins      map[string]loginAttempt
	applyOnce   sync.Once
	applies     *applyCoordinator
}

func (s *Server) applyCoordinator() *applyCoordinator {
	s.applyOnce.Do(func() { s.applies = newApplyCoordinator(s.Service) })
	return s.applies
}

func (s *Server) Router(local bool) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.RequestID, middleware.Recoverer)
	if !local {
		router.Use(s.allowCIDR)
	}
	router.Get("/api/v1/health", s.health)
	router.Get("/api/v1/bootstrap", s.bootstrapStatus)
	router.Post("/api/v1/session", s.login)
	router.Get("/ios/5gws-dot.mobileconfig", s.iosProfileFile)
	router.Get("/ios/5gws-dot.png", s.iosProfileQR)
	router.Group(func(router chi.Router) {
		if local {
			router.Use(localUser)
		} else {
			router.Use(s.requireSession)
			router.Use(requireSameOrigin)
		}
		router.Delete("/api/v1/session", s.logout)
		router.Get("/api/v1/me", s.me)
		router.Post("/api/v1/password", s.changePassword)
		router.Get("/api/v1/dashboard", s.dashboard)
		router.Get("/api/v1/metrics", s.metrics)
		router.Get("/api/v1/config", s.currentConfig)
		router.Get("/api/v1/rules/defaults", s.defaultRules)
		router.Post("/api/v1/config/validate", s.validateConfig)
		router.Post("/api/v1/config/apply", s.applyConfig)
		router.Get("/api/v1/config/apply/{id}", s.applyConfigStatus)
		router.Post("/api/v1/config/import", s.importConfig)
		router.Get("/api/v1/active/rules", s.activeRules)
		if local {
			router.Get("/api/v1/active", s.active)
			router.Get("/api/v1/draft", s.draft)
			router.Put("/api/v1/draft", s.saveDraft)
			router.Post("/api/v1/draft/validate", s.validateDraft)
			router.Post("/api/v1/apply", s.apply)
		}
		router.Get("/api/v1/logs", s.logs)
		router.Get("/api/v1/logs/stream", s.logsStream)
		router.Get("/api/v1/diagnostics", s.diagnostics)
		router.Post("/api/v1/diagnostics/run", s.runDiagnostics)
		router.Get("/api/v1/backup", s.exportBackup)
		if local {
			router.Post("/api/v1/backup", s.importBackup)
		}
		router.Get("/api/v1/ios/profile", s.iosProfile)
		router.Get("/api/v1/update", s.updateCheck)
		router.Post("/api/v1/update", s.updateApply)
	})
	if s.Web != nil && !local {
		router.Handle("/*", s.spaHandler())
	}
	return router
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	active, err := s.Service.Active(r.Context())
	if err != nil {
		writeStatusError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.Version, "active_revision": active.ID})
}
