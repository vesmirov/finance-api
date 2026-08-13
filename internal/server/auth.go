package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/vesmirov/finance-api/internal/store"
)

const (
	sessionCookie   = "finance_session"
	sessionLifetime = 30 * 24 * time.Hour
	minPasswordLen  = 8
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// currentUser returns the request's user or nil.
func (s *Server) currentUser(r *http.Request) *store.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	u, err := s.st.SessionUser(hashToken(c.Value))
	if err != nil {
		return nil
	}
	return u
}

// requireAuth guards everything under /api except /api/auth/* and /api/health,
// and stores the session user in the request context for handlers.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		open := p == "/" || p == "/api/health" || strings.HasPrefix(p, "/api/auth/")
		u := s.currentUser(r)
		if !open && strings.HasPrefix(p, "/api/") && u == nil {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if u != nil {
			r = withUser(r, u)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) startSession(w http.ResponseWriter, userID int64) error {
	token, err := newToken()
	if err != nil {
		return err
	}
	expires := time.Now().Add(sessionLifetime)
	if err := s.st.CreateSession(hashToken(token), userID, expires); err != nil {
		return err
	}
	s.setSessionCookie(w, token, expires)
	return nil
}

// POST /api/auth/login. Users are created only from the CLI (finance admin
// create) or by an admin in the admin panel; there is no sign-up endpoint.
func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	u, err := s.st.UserByLogin(strings.TrimSpace(b.Login))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusUnauthorized, "invalid login or password")
			return
		}
		s.storeErr(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(b.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid login or password")
		return
	}
	if err := s.startSession(w, u.ID); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /api/auth/logout
func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.st.DeleteSession(hashToken(c.Value))
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/auth/status
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.st.CountUsers()
	if err != nil {
		s.storeErr(w, err)
		return
	}
	u := s.currentUser(r)
	resp := map[string]any{
		"needs_setup":   n == 0,
		"authenticated": u != nil,
	}
	if u != nil {
		resp["login"] = u.Login
		resp["is_admin"] = u.IsAdmin
	}
	writeJSON(w, http.StatusOK, resp)
}
