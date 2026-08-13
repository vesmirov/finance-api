package server

import (
	"errors"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/vesmirov/finance-api/internal/store"
)

// adminOnly hides the admin API from everyone else: non-admins get a plain
// 404 (not 403) so the endpoints' existence is not revealed.
func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.user(r)
		if u == nil || !u.IsAdmin {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		next(w, r)
	}
}

func userDTO(u store.User) map[string]any {
	return map[string]any{"id": u.ID, "login": u.Login, "is_admin": u.IsAdmin}
}

// GET /api/admin/users
func (s *Server) adminListUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.st.Users()
	if err != nil {
		s.storeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, userDTO(u))
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/admin/users {login, password, is_admin}
func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Login    string `json:"login"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	login := strings.TrimSpace(b.Login)
	if login == "" {
		writeErr(w, http.StatusUnprocessableEntity, "login is required")
		return
	}
	if len(b.Password) < minPasswordLen {
		writeErr(w, http.StatusUnprocessableEntity, "password must be at least 8 characters")
		return
	}
	if _, err := s.st.UserByLogin(login); err == nil {
		writeErr(w, http.StatusConflict, "login is already taken")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.storeErr(w, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	id, err := s.st.CreateUser(login, string(hash), b.IsAdmin)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "login": login, "is_admin": b.IsAdmin})
}

// DELETE /api/admin/users/{id} — you cannot delete yourself or the last admin.
func (s *Server) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	self := s.user(r)
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if id == self.ID {
		writeErr(w, http.StatusConflict, "you cannot delete your own account")
		return
	}
	target, err := s.st.UserByID(id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if target.IsAdmin {
		admins, err := s.st.CountAdmins()
		if err != nil {
			s.storeErr(w, err)
			return
		}
		if admins <= 1 {
			writeErr(w, http.StatusConflict, "cannot delete the last admin")
			return
		}
	}
	if err := s.st.DeleteUser(id); err != nil {
		s.storeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/admin/users/{id}/password {password} — resets the password and
// revokes the user's sessions.
func (s *Server) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if len(b.Password) < minPasswordLen {
		writeErr(w, http.StatusUnprocessableEntity, "password must be at least 8 characters")
		return
	}
	if _, err := s.st.UserByID(id); err != nil {
		s.storeErr(w, err)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(b.Password), bcrypt.DefaultCost)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	if err := s.st.UpdateUser(id, map[string]any{"password_hash": string(hash)}); err != nil {
		s.storeErr(w, err)
		return
	}
	if err := s.st.DeleteUserSessions(id); err != nil {
		s.storeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
