package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"time"
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
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.Supervisor.Logs().Notify():
			body, _ := json.Marshal(map[string]string{"logs": s.Supervisor.Logs().Tail(100)})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"processes": s.Supervisor.Status(), "logs": s.Supervisor.Logs().Tail(40)})
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
