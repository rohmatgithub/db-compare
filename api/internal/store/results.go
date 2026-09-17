package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dbcompare/internal/engine"
)

// Table data statuses.
const (
	DataPending   = "pending"
	DataIdentical = "identical"
	DataDifferent = "different"
	DataError     = "error"
)

// Row comparison statuses.
const (
	RowDiffNotRun      = "not_run"
	RowDiffNotNeeded   = "not_needed"
	RowDiffNeedsKey    = "needs_key"
	RowDiffRunning     = "running"
	RowDiffDone        = "done"
	RowDiffCancelled   = "cancelled"
	RowDiffInterrupted = "interrupted"
	RowDiffError       = "error"
)

type TableResult struct {
	RunID         int64    `json:"run_id"`
	TableName     string   `json:"table_name"`
	KeyColumns    []string `json:"key_columns"`
	Columns       []string `json:"columns"`
	Notes         []string `json:"notes"`
	SourceRows    *int64   `json:"source_rows"`
	TargetRows    *int64   `json:"target_rows"`
	DataStatus    string   `json:"data_status"`
	RowDiffStatus string   `json:"rowdiff_status"`
	engine.Counts
	Truncated  bool      `json:"truncated"`
	Error      string    `json:"error"`
	DurationMs int64     `json:"duration_ms"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// RowDiffComplete reports whether the row comparison needs no further work.
func (t TableResult) RowDiffComplete() bool {
	switch t.RowDiffStatus {
	case RowDiffDone, RowDiffNotNeeded, RowDiffNeedsKey:
		return true
	}
	return false
}

const tableResultColumns = `run_id, table_name, key_columns, columns, notes, source_rows, target_rows,
	data_status, rowdiff_status, identical_count, different_count, only_source_count, only_target_count,
	truncated, error, duration_ms, updated_at`

func scanTableResult(row interface{ Scan(...any) error }) (TableResult, error) {
	var t TableResult
	var keys, cols, notes []byte
	err := row.Scan(&t.RunID, &t.TableName, &keys, &cols, &notes, &t.SourceRows, &t.TargetRows,
		&t.DataStatus, &t.RowDiffStatus, &t.Identical, &t.Different, &t.OnlySource, &t.OnlyTarget,
		&t.Truncated, &t.Error, &t.DurationMs, &t.UpdatedAt)
	if err != nil {
		return t, err
	}
	for _, f := range []struct {
		data []byte
		into *[]string
	}{{keys, &t.KeyColumns}, {cols, &t.Columns}, {notes, &t.Notes}} {
		if err := fromJSON(f.data, f.into); err != nil {
			return t, err
		}
		if *f.into == nil {
			*f.into = []string{}
		}
	}
	return t, nil
}

func (s *Store) SaveTableResult(ctx context.Context, t TableResult) error {
	keys, err := toJSON(nonNil(t.KeyColumns))
	if err != nil {
		return err
	}
	cols, err := toJSON(nonNil(t.Columns))
	if err != nil {
		return err
	}
	notes, err := toJSON(nonNil(t.Notes))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO table_results (`+tableResultColumns+`)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, now())
		ON CONFLICT (run_id, table_name) DO UPDATE SET
			key_columns = EXCLUDED.key_columns, columns = EXCLUDED.columns, notes = EXCLUDED.notes,
			source_rows = EXCLUDED.source_rows, target_rows = EXCLUDED.target_rows,
			data_status = EXCLUDED.data_status, rowdiff_status = EXCLUDED.rowdiff_status,
			identical_count = EXCLUDED.identical_count, different_count = EXCLUDED.different_count,
			only_source_count = EXCLUDED.only_source_count, only_target_count = EXCLUDED.only_target_count,
			truncated = EXCLUDED.truncated, error = EXCLUDED.error, duration_ms = EXCLUDED.duration_ms,
			updated_at = now()`,
		t.RunID, t.TableName, keys, cols, notes, nullInt64(t.SourceRows), nullInt64(t.TargetRows),
		t.DataStatus, t.RowDiffStatus, t.Identical, t.Different, t.OnlySource, t.OnlyTarget,
		t.Truncated, t.Error, t.DurationMs)
	return err
}

func (s *Store) ListTableResults(ctx context.Context, runID int64) ([]TableResult, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+tableResultColumns+" FROM table_results WHERE run_id = $1 ORDER BY table_name", runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TableResult{}
	for rows.Next() {
		t, err := scanTableResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTableResult(ctx context.Context, runID int64, table string) (TableResult, error) {
	t, err := scanTableResult(s.db.QueryRowContext(ctx,
		"SELECT "+tableResultColumns+" FROM table_results WHERE run_id = $1 AND table_name = $2", runID, table))
	return t, mapError(err)
}

type RowDiff struct {
	ID          int64     `json:"id"`
	Category    string    `json:"category"`
	Key         []string  `json:"key"`
	Source      []*string `json:"source"`
	Target      []*string `json:"target"`
	DiffColumns []string  `json:"diff_columns"`
}

func (s *Store) DeleteRowDiffs(ctx context.Context, runID int64, table string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM row_diffs WHERE run_id = $1 AND table_name = $2", runID, table)
	return err
}

// InsertRowDiffs writes one batch with a single multi-row INSERT.
func (s *Store) InsertRowDiffs(ctx context.Context, runID int64, table string, diffs []engine.RowDiff) error {
	if len(diffs) == 0 {
		return nil
	}
	const perRow = 7
	var b strings.Builder
	b.WriteString("INSERT INTO row_diffs (run_id, table_name, category, key_values, source_values, target_values, diff_columns) VALUES ")
	args := make([]any, 0, len(diffs)*perRow)
	for i, d := range diffs {
		if i > 0 {
			b.WriteString(", ")
		}
		n := i * perRow
		fmt.Fprintf(&b, "($%d, $%d, $%d, $%d, $%d, $%d, $%d)", n+1, n+2, n+3, n+4, n+5, n+6, n+7)
		key, err := toJSON(d.Key)
		if err != nil {
			return err
		}
		src, err := nullableJSON(d.Source)
		if err != nil {
			return err
		}
		tgt, err := nullableJSON(d.Target)
		if err != nil {
			return err
		}
		changed, err := toJSON(nonNil(d.DiffColumns))
		if err != nil {
			return err
		}
		args = append(args, runID, table, d.Category, key, src, tgt, changed)
	}
	_, err := s.db.ExecContext(ctx, b.String(), args...)
	return err
}

// ListRowDiffs returns up to limit rows after the given id, optionally
// restricted to one category.
func (s *Store) ListRowDiffs(ctx context.Context, runID int64, table, category string, afterID int64, limit int) ([]RowDiff, error) {
	query := `SELECT id, category, key_values, source_values, target_values, diff_columns
		FROM row_diffs WHERE run_id = $1 AND table_name = $2 AND id > $3`
	args := []any{runID, table, afterID}
	if category != "" {
		query += " AND category = $4"
		args = append(args, category)
	}
	query += fmt.Sprintf(" ORDER BY id LIMIT %d", limit)

	out := []RowDiff{}
	err := s.eachRowDiff(ctx, query, args, func(d RowDiff) error {
		out = append(out, d)
		return nil
	})
	return out, err
}

// ForEachRowDiff streams all stored rows of a table in insertion order.
func (s *Store) ForEachRowDiff(ctx context.Context, runID int64, table string, fn func(RowDiff) error) error {
	return s.eachRowDiff(ctx, `SELECT id, category, key_values, source_values, target_values, diff_columns
		FROM row_diffs WHERE run_id = $1 AND table_name = $2 ORDER BY id`, []any{runID, table}, fn)
}

func (s *Store) eachRowDiff(ctx context.Context, query string, args []any, fn func(RowDiff) error) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d RowDiff
		var key, src, tgt, changed []byte
		if err := rows.Scan(&d.ID, &d.Category, &key, &src, &tgt, &changed); err != nil {
			return err
		}
		for _, f := range []struct {
			data []byte
			into any
		}{{key, &d.Key}, {src, &d.Source}, {tgt, &d.Target}, {changed, &d.DiffColumns}} {
			if err := fromJSON(f.data, f.into); err != nil {
				return err
			}
		}
		if d.DiffColumns == nil {
			d.DiffColumns = []string{}
		}
		if err := fn(d); err != nil {
			return err
		}
	}
	return rows.Err()
}

func nullableJSON(values []*string) (any, error) {
	if values == nil {
		return nil, nil
	}
	return toJSON(values)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
