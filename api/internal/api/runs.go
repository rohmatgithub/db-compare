package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/go-chi/chi/v5"

	"dbcompare/internal/engine"
	"dbcompare/internal/export"
	"dbcompare/internal/runner"
	"dbcompare/internal/store"
)

type runResponse struct {
	store.Run
	Active  bool             `json:"active"`
	Summary store.RunSummary `json:"summary"`
	// Protected reports whether the project uses a protected connection.
	Protected bool `json:"protected"`
}

func (s *Server) loadRun(w http.ResponseWriter, r *http.Request) (store.Run, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return store.Run{}, false
	}
	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return store.Run{}, false
	}
	return run, true
}

// tableParam returns the table name from the URL. chi matches against the
// escaped path when the request has one, so the parameter is unescaped then.
func tableParam(r *http.Request) (string, error) {
	raw := chi.URLParam(r, "table")
	if r.URL.RawPath == "" {
		return raw, nil
	}
	return url.PathUnescape(raw)
}

// loadTable resolves the table of a run. Only tables recorded for the run are
// accepted, so request input never reaches SQL as an identifier.
func (s *Server) loadTable(w http.ResponseWriter, r *http.Request, runID int64) (store.TableResult, bool) {
	name, err := tableParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid table name")
		return store.TableResult{}, false
	}
	t, err := s.store.GetTableResult(r.Context(), runID, name)
	if err != nil {
		writeStoreError(w, err)
		return store.TableResult{}, false
	}
	return t, true
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	summary, err := s.store.RunSummary(r.Context(), run.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	protected, err := s.store.ProjectProtected(r.Context(), run.ProjectID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runResponse{
		Run: run, Active: s.runs.IsActive(run.ID), Summary: summary, Protected: protected,
	})
}

func (s *Server) deleteRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	if s.runs.IsActive(run.ID) {
		writeError(w, http.StatusConflict, "the run is in progress; stop it first")
		return
	}
	if err := s.store.DeleteRun(r.Context(), run.ID); err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "run.delete", fmt.Sprintf("run:%d", run.ID), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	cancelled := s.runs.Cancel(run.ID)
	if cancelled {
		s.audit(r, "run.cancel", fmt.Sprintf("run:%d", run.ID), nil)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"cancelled": cancelled})
}

// executeRun runs or resumes a run and streams its progress. The run stops
// when the client disconnects.
func (s *Server) executeRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	if !s.allowProjectExecution(w, r, run.ProjectID) {
		return
	}
	if err := s.runs.CheckExecutable(run); err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "run.execute", fmt.Sprintf("run:%d", run.ID), map[string]string{"previous_status": run.Status})

	stream := startSSE(w)
	stop := stream.keepAlive(r.Context(), s.cfg.HeartbeatInterval)
	err := s.runs.ExecuteRun(r.Context(), run.ID, stream.emit)
	stop()
	if err != nil && !stream.done() {
		stream.send("error", map[string]string{"message": err.Error()})
	}
}

type rowDiffRequest struct {
	KeyColumns []string `json:"key_columns"`
}

func (s *Server) executeRowDiff(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	t, ok := s.loadTable(w, r, run.ID)
	if !ok {
		return
	}
	var req rowDiffRequest
	if r.ContentLength != 0 && !readJSON(w, r, &req) {
		return
	}
	if !s.allowProjectExecution(w, r, run.ProjectID) {
		return
	}
	if run.Status == store.RunRunning || s.runs.ProjectActive(run.ProjectID) {
		writeError(w, http.StatusConflict, runner.ErrBusy.Error())
		return
	}
	s.audit(r, "run.rowdiff", fmt.Sprintf("run:%d", run.ID), map[string]any{
		"table": t.TableName, "key_columns": req.KeyColumns,
	})

	stream := startSSE(w)
	stop := stream.keepAlive(r.Context(), s.cfg.HeartbeatInterval)
	err := s.runs.ExecuteTableRowDiff(r.Context(), run.ID, t.TableName, req.KeyColumns, stream.emit)
	stop()
	switch {
	case err == nil:
		stream.send("done", map[string]string{"status": "completed"})
	case r.Context().Err() != nil:
	default:
		stream.send("error", map[string]string{"message": err.Error()})
	}
}

func (s *Server) listSchemaItems(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	items, err := s.store.ListSchemaItems(r.Context(), run.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) getSchemaItem(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	itemID, ok := pathID(w, r, "itemID")
	if !ok {
		return
	}
	item, err := s.store.GetSchemaItem(r.Context(), run.ID, itemID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	tables, err := s.store.ListTableResults(r.Context(), run.ID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tables)
}

func (s *Server) getTable(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	t, ok := s.loadTable(w, r, run.ID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type rowsResponse struct {
	Rows []store.RowDiff `json:"rows"`
	// NextAfter is the cursor for the following page, or 0 when there is none.
	NextAfter int64 `json:"next_after"`
}

func (s *Server) listRows(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	t, ok := s.loadTable(w, r, run.ID)
	if !ok {
		return
	}
	q := r.URL.Query()
	category := q.Get("category")
	switch category {
	case "", engine.CategoryDifferent, engine.CategoryOnlySource, engine.CategoryOnlyTarget:
	default:
		writeError(w, http.StatusBadRequest, "invalid category")
		return
	}
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.store.ListRowDiffs(r.Context(), run.ID, t.TableName, category, after, limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	resp := rowsResponse{Rows: rows}
	if len(rows) == limit {
		resp.NextAfter = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) exportRun(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	s.audit(r, "run.export", fmt.Sprintf("run:%d", run.ID), nil)
	s.writeWorkbook(w, r, fmt.Sprintf("db-compare-run-%d.xlsx", run.ID), func(ctx context.Context, out *lazyWriter) error {
		return export.WriteRun(ctx, out, s.store, run.ID)
	})
}

func (s *Server) exportTable(w http.ResponseWriter, r *http.Request) {
	run, ok := s.loadRun(w, r)
	if !ok {
		return
	}
	t, ok := s.loadTable(w, r, run.ID)
	if !ok {
		return
	}
	s.audit(r, "run.export_table", fmt.Sprintf("run:%d", run.ID), map[string]string{"table": t.TableName})
	name := fmt.Sprintf("db-compare-run-%d-%s.xlsx", run.ID, unsafeFileChars.ReplaceAllString(t.TableName, "_"))
	s.writeWorkbook(w, r, name, func(ctx context.Context, out *lazyWriter) error {
		return export.WriteTable(ctx, out, s.store, run.ID, t.TableName)
	})
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (s *Server) writeWorkbook(w http.ResponseWriter, r *http.Request, filename string, write func(context.Context, *lazyWriter) error) {
	out := &lazyWriter{w: w, header: func(h http.Header) {
		h.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		h.Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	}}
	// The workbook is assembled before the first byte is written, so errors
	// raised while reading results can still produce a JSON error response.
	if err := write(r.Context(), out); err != nil {
		if out.started {
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeStoreError(w, err)
	}
}

// lazyWriter sets download headers on the first write.
type lazyWriter struct {
	w       http.ResponseWriter
	header  func(http.Header)
	started bool
}

func (l *lazyWriter) Write(p []byte) (int, error) {
	if !l.started {
		l.started = true
		l.header(l.w.Header())
		l.w.WriteHeader(http.StatusOK)
	}
	return l.w.Write(p)
}
