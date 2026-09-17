package api

import (
	"errors"
	"fmt"
	"net/http"

	"dbcompare/internal/auth"
	"dbcompare/internal/store"
)

type createUserRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type updateUserRequest struct {
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

// hashPassword writes a 400 response for passwords of invalid length.
func hashPassword(w http.ResponseWriter, password string) (string, bool) {
	hash, err := auth.HashPassword(password)
	if errors.Is(err, auth.ErrPasswordLength) {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", false
	}
	if err != nil {
		writeStoreError(w, err)
		return "", false
	}
	return hash, true
}

// userTarget resolves the user in the path. Admins change their own account
// through /api/me so that they cannot lock themselves out by accident.
func userTarget(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return 0, false
	}
	if id == currentUser(r.Context()).ID {
		writeError(w, http.StatusConflict, "you cannot change your own account here; ask another admin")
		return 0, false
	}
	return id, true
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var in createUserRequest
	if !readJSON(w, r, &in) {
		return
	}
	in.Username = normalizeUsername(in.Username)
	if !auth.ValidUsername(in.Username) {
		writeError(w, http.StatusBadRequest,
			"username must be 3-64 characters of lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
		return
	}
	if !auth.ValidRole(in.Role) {
		writeError(w, http.StatusBadRequest, "role must be admin, operator, or viewer")
		return
	}
	hash, ok := hashPassword(w, in.Password)
	if !ok {
		return
	}
	u, err := s.store.CreateUser(r.Context(), in.Username, hash, in.Role)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "a user with this username already exists")
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "user.create", fmt.Sprintf("user:%d", u.ID), map[string]string{"username": u.Username, "role": u.Role})
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userTarget(w, r)
	if !ok {
		return
	}
	var in updateUserRequest
	if !readJSON(w, r, &in) {
		return
	}
	if !auth.ValidRole(in.Role) {
		writeError(w, http.StatusBadRequest, "role must be admin, operator, or viewer")
		return
	}
	before, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	u, err := s.store.UpdateUser(r.Context(), id, in.Role, in.Disabled)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "user.update", fmt.Sprintf("user:%d", id), map[string]any{
		"username": u.Username,
		"role":     map[string]string{"from": before.Role, "to": u.Role},
		"disabled": map[string]bool{"from": before.Disabled, "to": u.Disabled},
	})
	writeJSON(w, http.StatusOK, u)
}

// resetUserPassword sets a new password for another user and ends that
// user's sessions.
func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := userTarget(w, r)
	if !ok {
		return
	}
	var in resetPasswordRequest
	if !readJSON(w, r, &in) {
		return
	}
	hash, ok := hashPassword(w, in.Password)
	if !ok {
		return
	}
	if err := s.store.SetUserPassword(r.Context(), id, hash, nil); err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "user.password_reset", fmt.Sprintf("user:%d", id), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userTarget(w, r)
	if !ok {
		return
	}
	u, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "user.delete", fmt.Sprintf("user:%d", id), map[string]string{"username": u.Username})
	w.WriteHeader(http.StatusNoContent)
}
