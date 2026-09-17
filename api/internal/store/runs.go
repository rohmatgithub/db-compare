package store

import (
	"context"
	"database/sql"
	"time"

	"dbcompare/internal/engine"
)

// Run statuses.
const (
	RunPending     = "pending"
	RunRunning     = "running"
	RunCompleted   = "completed"
	RunCancelled   = "cancelled"
	RunInterrupted = "interrupted"
	RunFailed      = "failed"
)

type RunProgress struct {
	Phase        string   `json:"phase"`
	SchemaDone   bool     `json:"schema_done"`
	TablesTotal  int      `json:"tables_total"`
	TablesDone   int      `json:"tables_done"`
	ActiveTables []string `json:"active_tables"`
}

type Run struct {
	ID          int64          `json:"id"`
	ProjectID   int64          `json:"project_id"`
	Status      string         `json:"status"`
	Options     engine.Options `json:"options"`
	Progress    RunProgress    `json:"progress"`
	SourceLabel string         `json:"source_label"`
	TargetLabel string         `json:"target_label"`
	Error       string         `json:"error"`
	CreatedBy   string         `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	StartedAt   *time.Time     `json:"started_at"`
	FinishedAt  *time.Time     `json:"finished_at"`
	HeartbeatAt *time.Time     `json:"heartbeat_at"`
}

// Resumable reports whether the run can be executed (again).
func (r Run) Resumable() bool {
	switch r.Status {
	case RunPending, RunCancelled, RunInterrupted, RunFailed:
		return true
	}
	return false
}

const runColumns = `id, project_id, status, options, progress, source_label, target_label, error,
	created_by, created_at, started_at, finished_at, heartbeat_at`

func scanRun(row interface{ Scan(...any) error }) (Run, error) {
	var r Run
	var opts, progress []byte
	err := row.Scan(&r.ID, &r.ProjectID, &r.Status, &opts, &progress, &r.SourceLabel, &r.TargetLabel, &r.Error,
		&r.CreatedBy, &r.CreatedAt, &r.StartedAt, &r.FinishedAt, &r.HeartbeatAt)
	if err != nil {
		return r, err
	}
	if err := fromJSON(opts, &r.Options); err != nil {
		return r, err
	}
	return r, fromJSON(progress, &r.Progress)
}

func (s *Store) CreateRun(ctx context.Context, projectID int64, opts engine.Options, sourceLabel, targetLabel, createdBy string) (Run, error) {
	payload, err := toJSON(opts)
	if err != nil {
		return Run{}, err
	}
	r, err := scanRun(s.db.QueryRowContext(ctx, `
		INSERT INTO compare_runs (project_id, status, options, source_label, target_label, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+runColumns,
		projectID, RunPending, payload, sourceLabel, targetLabel, createdBy))
	return r, mapError(err)
}

func (s *Store) GetRun(ctx context.Context, id int64) (Run, error) {
	r, err := scanRun(s.db.QueryRowContext(ctx, "SELECT "+runColumns+" FROM compare_runs WHERE id = $1", id))
	return r, mapError(err)
}

func (s *Store) ListRuns(ctx context.Context, projectID int64, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+runColumns+" FROM compare_runs WHERE project_id = $1 ORDER BY id DESC LIMIT $2", projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteRun(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM compare_runs WHERE id = $1", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkRunStarted(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE compare_runs
		SET status = $2, error = '', finished_at = NULL, heartbeat_at = now(),
		    started_at = COALESCE(started_at, now())
		WHERE id = $1`, id, RunRunning)
	return err
}

func (s *Store) Heartbeat(ctx context.Context, id int64, p RunProgress) error {
	payload, err := toJSON(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "UPDATE compare_runs SET progress = $2, heartbeat_at = now() WHERE id = $1", id, payload)
	return err
}

func (s *Store) FinishRun(ctx context.Context, id int64, status, message string, p RunProgress) error {
	payload, err := toJSON(p)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE compare_runs SET status = $2, error = $3, progress = $4, finished_at = now(), heartbeat_at = now()
		WHERE id = $1`, id, status, message, payload)
	return err
}

// MarkStaleRuns flags runs whose heartbeat is older than staleAfter as
// interrupted. Active runs refresh their heartbeat, so this is safe to call
// while the server is running.
func (s *Store) MarkStaleRuns(ctx context.Context, staleAfter time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE compare_runs SET status = $1, finished_at = now()
		WHERE status = $2 AND (heartbeat_at IS NULL OR heartbeat_at < now() - make_interval(secs => $3))`,
		RunInterrupted, RunRunning, staleAfter.Seconds())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkRowDiffsInterrupted flags every in-progress table comparison as
// interrupted. Table comparisons have no heartbeat, so this must only run
// before the server accepts requests.
func (s *Store) MarkRowDiffsInterrupted(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE table_results SET rowdiff_status = $1 WHERE rowdiff_status = $2",
		RowDiffInterrupted, RowDiffRunning)
	return err
}

// DeleteRunsBefore removes finished runs created before the cutoff.
func (s *Store) DeleteRunsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM compare_runs WHERE created_at < $1 AND status <> $2", cutoff, RunRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type RunSummary struct {
	Schema  map[string]int `json:"schema"`
	Data    map[string]int `json:"data"`
	RowDiff map[string]int `json:"rowdiff"`
	Rows    engine.Counts  `json:"rows"`
}

func (s *Store) RunSummary(ctx context.Context, id int64) (RunSummary, error) {
	sum := RunSummary{Schema: map[string]int{}, Data: map[string]int{}, RowDiff: map[string]int{}}
	if err := s.countBy(ctx, "SELECT status, count(*) FROM schema_diff_items WHERE run_id = $1 GROUP BY status", id, sum.Schema); err != nil {
		return sum, err
	}
	if err := s.countBy(ctx, "SELECT data_status, count(*) FROM table_results WHERE run_id = $1 GROUP BY data_status", id, sum.Data); err != nil {
		return sum, err
	}
	if err := s.countBy(ctx, "SELECT rowdiff_status, count(*) FROM table_results WHERE run_id = $1 GROUP BY rowdiff_status", id, sum.RowDiff); err != nil {
		return sum, err
	}
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(sum(different_count), 0), COALESCE(sum(only_source_count), 0), COALESCE(sum(only_target_count), 0)
		FROM table_results WHERE run_id = $1`, id).Scan(&sum.Rows.Different, &sum.Rows.OnlySource, &sum.Rows.OnlyTarget)
	return sum, err
}

func (s *Store) countBy(ctx context.Context, query string, id int64, into map[string]int) error {
	rows, err := s.db.QueryContext(ctx, query, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return err
		}
		into[key] = n
	}
	return rows.Err()
}

type SchemaItem struct {
	ID         int64                 `json:"id"`
	ObjectType string                `json:"object_type"`
	Name       string                `json:"name"`
	Status     string                `json:"status"`
	Changes    []engine.SchemaChange `json:"changes"`
	SourceDDL  string                `json:"source_ddl,omitempty"`
	TargetDDL  string                `json:"target_ddl,omitempty"`
}

func (s *Store) ReplaceSchemaItems(ctx context.Context, runID int64, items []engine.SchemaDiffItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM schema_diff_items WHERE run_id = $1", runID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO schema_diff_items (run_id, object_type, object_name, status, changes, source_ddl, target_ddl)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, it := range items {
		changes := it.Changes
		if changes == nil {
			changes = []engine.SchemaChange{}
		}
		payload, err := toJSON(changes)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, runID, it.ObjectType, it.Name, it.Status, payload, it.SourceDDL, it.TargetDDL); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListSchemaItems returns items without DDL, which GetSchemaItem provides.
func (s *Store) ListSchemaItems(ctx context.Context, runID int64) ([]SchemaItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, object_type, object_name, status, changes
		FROM schema_diff_items WHERE run_id = $1 ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SchemaItem{}
	for rows.Next() {
		var it SchemaItem
		var changes []byte
		if err := rows.Scan(&it.ID, &it.ObjectType, &it.Name, &it.Status, &changes); err != nil {
			return nil, err
		}
		if err := fromJSON(changes, &it.Changes); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) GetSchemaItem(ctx context.Context, runID, id int64) (SchemaItem, error) {
	var it SchemaItem
	var changes []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, object_type, object_name, status, changes, source_ddl, target_ddl
		FROM schema_diff_items WHERE run_id = $1 AND id = $2`, runID, id).
		Scan(&it.ID, &it.ObjectType, &it.Name, &it.Status, &changes, &it.SourceDDL, &it.TargetDDL)
	if err != nil {
		return it, mapError(err)
	}
	return it, fromJSON(changes, &it.Changes)
}

func nullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}
