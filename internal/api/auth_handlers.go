package api

import (
	"net"
	"net/http"
	"time"
)

type loginAttempt struct {
	Failures int
	Window   time.Time
	Locked   time.Time
}

func (s *Server) bootstrapStatus(w http.ResponseWriter, r *http.Request) {
	needed, err := s.Auth.NeedsBootstrap(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": needed})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if !decodeJSON(w, r, &body) {
		return
	}
	ip := remoteIP(r)
	if !s.allowLogin(ip) {
		writeStatusError(w, http.StatusTooManyRequests, "login temporarily locked")
		return
	}
	user, token, expires, err := s.Auth.Login(r.Context(), body.Username, body.Password)
	if err != nil {
		s.recordLoginFailure(ip)
		writeStatusError(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.resetLogin(ip)
	http.SetCookie(w, session(token, expires))
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Auth.Logout(r.Context(), cookie.Value); err != nil {
			writeError(w, err)
			return
		}
	}
	http.SetCookie(w, expiredSession())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct{ Current, Next string }
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.Auth.ChangePassword(r.Context(), currentUser(r).ID, body.Current, body.Next); err != nil {
		writeError(w, err)
		return
	}
	http.SetCookie(w, expiredSession())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) allowLogin(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.logins == nil {
		s.logins = make(map[string]loginAttempt)
	}
	return !s.logins[ip].Locked.After(time.Now())
}

func (s *Server) recordLoginFailure(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.logins == nil {
		s.logins = make(map[string]loginAttempt)
	}
	now := time.Now()
	item := s.logins[ip]
	if item.Window.IsZero() || now.Sub(item.Window) > time.Minute {
		item = loginAttempt{Window: now}
	}
	item.Failures++
	if item.Failures >= 5 {
		item.Locked = now.Add(15 * time.Minute)
	}
	s.logins[ip] = item
}

func (s *Server) resetLogin(ip string) {
	s.loginMu.Lock()
	delete(s.logins, ip)
	s.loginMu.Unlock()
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func session(token string, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expires, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}

func expiredSession() *http.Cookie {
	return &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode}
}
