package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"dbcompare/internal/auth"
	"dbcompare/internal/store"
)

const sessionCookieName = "dbcompare_session"

type sessionKey struct{}

type session struct {
	user      store.User
	tokenHash []byte
}

func currentSession(ctx context.Context) session {
	s, _ := ctx.Value(sessionKey{}).(session)
	return s
}

func currentUser(ctx context.Context) store.User {
	return currentSession(ctx).user
}

// actor names the signed-in user in audit logs and run records.
func actor(ctx context.Context) string {
	return currentUser(ctx).Username
}

func can(ctx context.Context, p auth.Permission) bool {
	return auth.Can(currentUser(ctx).Role, p)
}

// authenticate resolves the session cookie to an enabled user.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		hash := auth.HashToken(c.Value)
		u, err := s.store.SessionUser(r.Context(), hash)
		if errors.Is(err, store.ErrNotFound) {
			clearSessionCookie(w, r)
			writeError(w, http.StatusUnauthorized, "your session has expired; sign in again")
			return
		}
		if err != nil {
			writeStoreError(w, err)
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, session{user: u, tokenHash: hash})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// require rejects requests from users whose role lacks p.
func require(p auth.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !can(r.Context(), p) {
				writeError(w, http.StatusForbidden, "your role does not allow this action")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// allowProjectExecution reports whether the user may run compares for the
// project, writing a 403 response when not. Projects that use a protected
// connection need PermConnectionProtected on top of PermRunExecute.
func (s *Server) allowProjectExecution(w http.ResponseWriter, r *http.Request, projectID int64) bool {
	if can(r.Context(), auth.PermConnectionProtected) {
		return true
	}
	protected, err := s.store.ProjectProtected(r.Context(), projectID)
	if err != nil {
		writeStoreError(w, err)
		return false
	}
	if protected {
		writeError(w, http.StatusForbidden, "this project uses a protected connection; only admins can run it")
		return false
	}
	return true
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
}

type meResponse struct {
	ID          int64             `json:"id"`
	User        string            `json:"user"`
	Role        string            `json:"role"`
	Permissions []auth.Permission `json:"permissions"`
}

func newMeResponse(u store.User) meResponse {
	return meResponse{ID: u.ID, User: u.Username, Role: u.Role, Permissions: auth.Permissions(u.Role)}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, newMeResponse(currentUser(r.Context())))
}

func normalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// auditName bounds untrusted user names before they are written to the
// audit log or used as a rate-limit key.
func auditName(name string) string {
	if len(name) > 64 {
		return name[:64]
	}
	return name
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if !readJSON(w, r, &in) {
		return
	}
	username := normalizeUsername(in.Username)
	key := auditName(username) + "|" + clientIP(r)
	if wait := s.logins.blocked(key); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many failed sign-in attempts; try again shortly")
		return
	}

	ctx := r.Context()
	u, hash, err := s.store.UserCredentials(ctx, username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeStoreError(w, err)
		return
	}
	// An unknown user leaves hash empty, which still costs a full bcrypt
	// comparison.
	if !auth.CheckPassword(hash, in.Password) {
		s.logins.fail(key)
		s.auditAs(r, auditName(username), "auth.login_failed", "user:"+auditName(username), nil)
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if u.Disabled {
		s.auditAs(r, u.Username, "auth.login_failed", fmt.Sprintf("user:%d", u.ID), map[string]string{"reason": "disabled"})
		writeError(w, http.StatusForbidden, "this account is disabled")
		return
	}
	s.logins.reset(key)

	token, tokenHash, err := auth.NewToken()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.store.CreateSession(ctx, tokenHash, u.ID, expires); err != nil {
		writeStoreError(w, err)
		return
	}
	setSessionCookie(w, r, token, expires)
	s.auditAs(r, u.Username, "auth.login", fmt.Sprintf("user:%d", u.ID), nil)
	writeJSON(w, http.StatusOK, newMeResponse(u))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sess := currentSession(r.Context())
	if err := s.store.DeleteSession(r.Context(), sess.tokenHash); err != nil {
		writeStoreError(w, err)
		return
	}
	clearSessionCookie(w, r)
	s.audit(r, "auth.logout", fmt.Sprintf("user:%d", sess.user.ID), nil)
	w.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// changePassword lets users replace their own password. Other sessions of
// the user are ended; the current one stays signed in.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var in changePasswordRequest
	if !readJSON(w, r, &in) {
		return
	}
	ctx := r.Context()
	sess := currentSession(ctx)
	key := sess.user.Username + "|" + clientIP(r)
	if wait := s.logins.blocked(key); wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many failed attempts; try again shortly")
		return
	}
	_, hash, err := s.store.UserCredentials(ctx, sess.user.Username)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if !auth.CheckPassword(hash, in.CurrentPassword) {
		s.logins.fail(key)
		writeError(w, http.StatusBadRequest, "the current password is incorrect")
		return
	}
	s.logins.reset(key)
	newHash, err := auth.HashPassword(in.NewPassword)
	if errors.Is(err, auth.ErrPasswordLength) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.store.SetUserPassword(ctx, sess.user.ID, newHash, sess.tokenHash); err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "user.password_change", fmt.Sprintf("user:%d", sess.user.ID), nil)
	w.WriteHeader(http.StatusNoContent)
}

// clientIP returns the request's remote host. middleware.RealIP has already
// replaced RemoteAddr with the forwarded address when one is present.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

const (
	maxLoginFailures   = 5
	loginFailureWindow = 15 * time.Minute
	loginLockout       = time.Minute
)

// loginLimiter locks out a user name and client address pair for a while
// after repeated failed password checks.
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginFailures
}

type loginFailures struct {
	count  int
	last   time.Time
	locked time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: map[string]*loginFailures{}}
}

// blocked returns how long key stays locked out, or 0.
func (l *loginLimiter) blocked(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return 0
	}
	return max(time.Until(e.locked), 0)
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.entries) > 10_000 {
		for k, e := range l.entries {
			if now.Sub(e.last) > loginFailureWindow && now.After(e.locked) {
				delete(l.entries, k)
			}
		}
	}
	e := l.entries[key]
	if e == nil || now.Sub(e.last) > loginFailureWindow {
		e = &loginFailures{}
		l.entries[key] = e
	}
	e.count++
	e.last = now
	if e.count >= maxLoginFailures {
		e.count = 0
		e.locked = now.Add(loginLockout)
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
