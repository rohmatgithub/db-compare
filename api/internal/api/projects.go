package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"dbcompare/internal/store"
)

const maxRowDiffLimit = 1_000_000

func (s *Server) validateProject(ctx context.Context, in *store.ProjectInput) error {
	in.Name = strings.TrimSpace(in.Name)
	var problems []string
	if in.Name == "" {
		problems = append(problems, "name is required")
	}
	if in.SourceConnectionID == in.TargetConnectionID {
		problems = append(problems, "source and target must be different connections")
	}
	if in.Options.RowDiffLimit < 0 || in.Options.RowDiffLimit > maxRowDiffLimit {
		problems = append(problems, fmt.Sprintf("row_diff_limit must be between 0 and %d", maxRowDiffLimit))
	}
	if in.Options.ChunkSize < 0 || in.Options.Parallelism < 0 {
		problems = append(problems, "chunk_size and parallelism must not be negative")
	}
	for table, cols := range in.Options.KeyOverrides {
		if len(cols) == 0 {
			delete(in.Options.KeyOverrides, table)
		}
	}

	var engines []string
	for _, id := range []int64{in.SourceConnectionID, in.TargetConnectionID} {
		c, err := s.store.GetConnection(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			problems = append(problems, fmt.Sprintf("connection %d does not exist", id))
			continue
		}
		if err != nil {
			return err
		}
		engines = append(engines, c.Engine)
	}
	if len(engines) == 2 && engines[0] != engines[1] {
		problems = append(problems, "source and target must use the same engine")
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", errValidation, strings.Join(problems, "; "))
	}
	return nil
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListProjects(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := s.store.GetProject(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var in store.ProjectInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.validateProject(r.Context(), &in); err != nil {
		writeStoreError(w, err)
		return
	}
	p, err := s.store.CreateProject(r.Context(), in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "project.create", fmt.Sprintf("project:%d", p.ID), in)
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var in store.ProjectInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.validateProject(r.Context(), &in); err != nil {
		writeStoreError(w, err)
		return
	}
	p, err := s.store.UpdateProject(r.Context(), id, in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "project.update", fmt.Sprintf("project:%d", id), in)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if s.runs.ProjectActive(id) {
		writeError(w, http.StatusConflict, "a compare is running for this project")
		return
	}
	if err := s.store.DeleteProject(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "project.delete", fmt.Sprintf("project:%d", id), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	runs, err := s.store.ListRuns(r.Context(), id, 100)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// createRun records a pending run with a snapshot of the project options.
// The client then executes it with POST /api/runs/{id}/execute.
func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if !s.allowProjectExecution(w, r, id) {
		return
	}
	ctx := r.Context()
	p, err := s.store.GetProject(ctx, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	src, err := s.store.GetConnection(ctx, p.SourceConnectionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	tgt, err := s.store.GetConnection(ctx, p.TargetConnectionID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	opts := p.Options.WithDefaults(s.cfg.MaxParallelTables)
	if opts.KeyOverrides == nil {
		opts.KeyOverrides = map[string][]string{}
	}
	run, err := s.store.CreateRun(ctx, p.ID, opts, src.Label(), tgt.Label(), actor(ctx))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	s.audit(r, "run.create", fmt.Sprintf("run:%d", run.ID), map[string]any{"project_id": p.ID})
	writeJSON(w, http.StatusCreated, run)
}
