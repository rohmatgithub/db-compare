package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"dbcompare/internal/engine"
	"dbcompare/internal/store"
)

func validateConnection(in *store.ConnectionInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.Database = strings.TrimSpace(in.Database)
	in.Schema = strings.TrimSpace(in.Schema)
	in.Username = strings.TrimSpace(in.Username)
	if in.SSLMode == "" {
		in.SSLMode = engine.SSLDisable
	}
	var problems []string
	if in.Name == "" {
		problems = append(problems, "name is required")
	}
	if in.Engine != engine.EngineMySQL && in.Engine != engine.EnginePostgres {
		problems = append(problems, "engine must be mysql or postgres")
	}
	if in.Host == "" {
		problems = append(problems, "host is required")
	}
	if in.Port < 1 || in.Port > 65535 {
		problems = append(problems, "port must be between 1 and 65535")
	}
	if in.Database == "" {
		problems = append(problems, "database is required")
	}
	if in.Username == "" {
		problems = append(problems, "username is required")
	}
	switch in.SSLMode {
	case engine.SSLDisable, engine.SSLPrefer, engine.SSLRequire, engine.SSLVerifyFull:
	default:
		problems = append(problems, "ssl_mode must be disable, prefer, require, or verify-full")
	}
	if in.Engine == engine.EngineMySQL {
		in.Schema = ""
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", errValidation, strings.Join(problems, "; "))
	}
	return nil
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListConnections(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	c, err := s.store.GetConnection(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) createConnection(w http.ResponseWriter, r *http.Request) {
	var in store.ConnectionInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := validateConnection(&in); err != nil {
		writeStoreError(w, err)
		return
	}
	c, err := s.store.CreateConnection(r.Context(), in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "connection.create", fmt.Sprintf("connection:%d", c.ID), map[string]any{
		"name": c.Name, "protected": c.Protected,
	})
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) updateConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in store.ConnectionInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := validateConnection(&in); err != nil {
		writeStoreError(w, err)
		return
	}
	c, err := s.store.UpdateConnection(r.Context(), id, in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.pools.Invalidate(id)
	s.audit(r, "connection.update", fmt.Sprintf("connection:%d", id), map[string]any{
		"name": c.Name, "protected": c.Protected, "password_changed": in.Password != nil,
	})
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) deleteConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.store.DeleteConnection(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.pools.Invalidate(id)
	s.audit(r, "connection.delete", fmt.Sprintf("connection:%d", id), nil)
	w.WriteHeader(http.StatusNoContent)
}

type testConnectionRequest struct {
	store.ConnectionInput
	// ID lets the form test an existing connection without re-entering its
	// password.
	ID int64 `json:"id"`
}

type testConnectionResponse struct {
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Tables  int    `json:"tables"`
	Warning string `json:"warning,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	var in testConnectionRequest
	if !readJSON(w, r, &in) {
		return
	}
	if err := validateConnection(&in.ConnectionInput); err != nil {
		writeStoreError(w, err)
		return
	}
	password := ""
	switch {
	case in.Password != nil:
		password = *in.Password
	case in.ID > 0:
		p, err := s.store.ConnectionPassword(r.Context(), in.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		password = p
	}

	info := engine.ConnInfo{
		Engine: in.Engine, Host: in.Host, Port: in.Port, Database: in.Database,
		Schema: in.Schema, Username: in.Username, Password: password, SSLMode: in.SSLMode,
	}
	writeJSON(w, http.StatusOK, probeConnection(r.Context(), info))
}

func probeConnection(ctx context.Context, info engine.ConnInfo) testConnectionResponse {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	db, dialect, err := engine.Open(info)
	if err != nil {
		return testConnectionResponse{Error: err.Error()}
	}
	defer db.Close()
	version, err := dialect.ServerVersion(ctx, db)
	if err != nil {
		return testConnectionResponse{Error: err.Error()}
	}
	res := testConnectionResponse{OK: true, Version: version}
	if warning, err := dialect.WritePrivilegeWarning(ctx, db); err != nil {
		res.Warning = "could not check privileges: " + err.Error()
	} else {
		res.Warning = warning
	}
	// An empty table filter runs every catalog query without reading any
	// table, which verifies catalog access cheaply.
	if _, err := dialect.Inspect(ctx, db, info.SchemaName(), []string{}); err != nil {
		res.OK, res.Error = false, "connected, but cannot read the schema: "+err.Error()
		return res
	}
	var count int
	countQuery := "SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_type = 'BASE TABLE'"
	if info.Engine == engine.EnginePostgres {
		countQuery = "SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_type = 'BASE TABLE'"
	}
	if err := db.QueryRowContext(ctx, countQuery, info.SchemaName()).Scan(&count); err == nil {
		res.Tables = count
	}
	if res.Tables == 0 {
		res.Warning = strings.TrimPrefix(res.Warning+"; schema "+info.SchemaName()+" has no tables", "; ")
	}
	return res
}
