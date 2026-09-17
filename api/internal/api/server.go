// Package api exposes the HTTP interface used by the web frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"dbcompare/internal/auth"
	"dbcompare/internal/config"
	"dbcompare/internal/runner"
	"dbcompare/internal/store"
)

type Server struct {
	cfg    *config.Config
	store  *store.Store
	runs   *runner.Manager
	pools  *runner.Pools
	logins *loginLimiter
}

func New(cfg *config.Config, st *store.Store, runs *runner.Manager, pools *runner.Pools) *Server {
	return &Server{cfg: cfg, store: st, runs: runs, pools: pools, logins: newLoginLimiter()}
}

// Routes serves the API. Every signed-in user may read projects,
// connections, and results; other actions need the permission named next to
// the route.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger)
	r.Use(middleware.Recoverer)
	// The session cookie is sent with any request to this origin, so
	// state-changing requests from other origins are rejected.
	r.Use(http.NewCrossOriginProtection().Handler)

	r.Get("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Post("/api/auth/login", s.login)

	r.Group(func(r chi.Router) {
		r.Use(s.authenticate)

		r.Post("/api/auth/logout", s.logout)
		r.Get("/api/me", s.me)
		r.Put("/api/me/password", s.changePassword)

		r.Route("/api/users", func(r chi.Router) {
			r.Use(require(auth.PermUserManage))
			r.Get("/", s.listUsers)
			r.Post("/", s.createUser)
			r.Put("/{id}", s.updateUser)
			r.Put("/{id}/password", s.resetUserPassword)
			r.Delete("/{id}", s.deleteUser)
		})

		r.Route("/api/connections", func(r chi.Router) {
			r.Get("/", s.listConnections)
			r.Get("/{id}", s.getConnection)
			r.Group(func(r chi.Router) {
				r.Use(require(auth.PermConnectionManage))
				r.Post("/", s.createConnection)
				r.Post("/test", s.testConnection)
				r.Put("/{id}", s.updateConnection)
				r.Delete("/{id}", s.deleteConnection)
			})
		})

		r.Route("/api/projects", func(r chi.Router) {
			r.Get("/", s.listProjects)
			r.Get("/{id}", s.getProject)
			r.Get("/{id}/runs", s.listRuns)
			r.With(require(auth.PermProjectWrite)).Post("/", s.createProject)
			r.With(require(auth.PermProjectWrite)).Put("/{id}", s.updateProject)
			r.With(require(auth.PermProjectDelete)).Delete("/{id}", s.deleteProject)
			r.With(require(auth.PermRunExecute)).Post("/{id}/runs", s.createRun)
		})

		r.Route("/api/runs/{id}", func(r chi.Router) {
			r.Get("/", s.getRun)
			r.Get("/schema", s.listSchemaItems)
			r.Get("/schema/{itemID}", s.getSchemaItem)
			r.Get("/tables", s.listTables)
			r.Get("/tables/{table}", s.getTable)
			r.Get("/tables/{table}/rows", s.listRows)
			r.With(require(auth.PermRunDelete)).Delete("/", s.deleteRun)
			r.Group(func(r chi.Router) {
				r.Use(require(auth.PermRunExecute))
				r.Post("/execute", s.executeRun)
				r.Post("/cancel", s.cancelRun)
				r.Post("/tables/{table}/rowdiff", s.executeRowDiff)
			})
			r.Group(func(r chi.Router) {
				r.Use(require(auth.PermRunExport))
				r.Get("/export.xlsx", s.exportRun)
				r.Get("/tables/{table}/export.xlsx", s.exportTable)
			})
		})
	})
	return r
}

func (s *Server) audit(r *http.Request, action, target string, details any) {
	s.auditAs(r, actor(r.Context()), action, target, details)
}

// auditAs records an action for a named actor, for requests made before
// the user is signed in.
func (s *Server) auditAs(r *http.Request, actor, action, target string, details any) {
	if err := s.store.Audit(context.WithoutCancel(r.Context()), actor, action, target, details); err != nil {
		slog.Error("write audit log", "action", action, "err", err)
	}
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"duration", time.Since(start).Round(time.Millisecond), "request_id", middleware.GetReqID(r.Context()))
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeStoreError maps package errors to HTTP statuses.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrLastAdmin),
		errors.Is(err, runner.ErrBusy), errors.Is(err, runner.ErrNotResumable):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("request failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

var errValidation = errors.New("invalid input")

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return id, true
}
