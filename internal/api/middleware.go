package api

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/morain/5gws/internal/auth"
)

type contextKey string

const userKey contextKey = "user"

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			writeStatusError(w, http.StatusUnauthorized, "missing session")
			return
		}
		user, err := s.Auth.Verify(r.Context(), cookie.Value)
		if err != nil {
			writeStatusError(w, http.StatusUnauthorized, err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

func localUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := auth.User{ID: 0, Username: "root"}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

func (s *Server) allowCIDR(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		revision, err := s.Service.Active(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		for _, value := range revision.Bundle.Config.Panel.AllowedCIDRs {
			_, network, _ := net.ParseCIDR(value)
			if network != nil && network.Contains(ip) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeStatusError(w, http.StatusForbidden, "source IP is not allowed")
	})
}

func requireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			origin := r.Header.Get("Origin")
			if origin != "" && !strings.EqualFold(strings.TrimPrefix(origin, "https://"), r.Host) {
				writeStatusError(w, http.StatusForbidden, "origin mismatch")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func currentUser(r *http.Request) auth.User {
	user, _ := r.Context().Value(userKey).(auth.User)
	return user
}
