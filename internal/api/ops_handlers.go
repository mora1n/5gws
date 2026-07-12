package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"

	"github.com/morain/5gws/internal/diagnostics"
)

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	lines, _ := strconv.Atoi(r.URL.Query().Get("lines"))
	if lines == 0 {
		lines = 300
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": s.Supervisor.Logs().Tail(lines)})
}

func (s *Server) logsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeStatusError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	updates, unsubscribe := s.Supervisor.Logs().Subscribe()
	defer unsubscribe()
	writeLogs := func() error {
		logs, sequence := s.Supervisor.Logs().Snapshot(500)
		body, _ := json.Marshal(map[string]string{"logs": logs})
		if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", sequence, body); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := writeLogs(); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	var batch *time.Timer
	var batchC <-chan time.Time
	defer func() {
		if batch != nil {
			batch.Stop()
		}
	}()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-updates:
			if !ok {
				return
			}
			if batch == nil {
				batch = time.NewTimer(250 * time.Millisecond)
				batchC = batch.C
			}
		case <-batchC:
			if err := writeLogs(); err != nil {
				return
			}
			batch = nil
			batchC = nil
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"processes": s.Supervisor.Status(), "logs": s.Supervisor.Logs().Tail(40)})
}

func (s *Server) runDiagnostics(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = diagnostics.ScopeAll
	}
	if !diagnostics.ValidScope(scope) {
		writeStatusError(w, http.StatusBadRequest, "invalid diagnostics scope")
		return
	}
	active, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.Diagnostics.Run(r.Context(), active.Bundle.Config, scope))
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	info, err := s.Updater.Check(r.Context(), s.Version)
	respond(w, info, err)
}

func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	active, err := s.Service.Active(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	info, err := s.Updater.Apply(r.Context(), s.Version, "/usr/local/bin/5gws", active.Bundle.Config.System.StateDir)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, info)
	go func() {
		time.Sleep(time.Second)
		if err := exec.Command("systemctl", "restart", "5gws.service").Run(); err != nil {
			log.Printf("restart after update: %v", err)
		}
	}()
}
