package engine

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Row diff categories.
const (
	CategoryDifferent  = "different"
	CategoryOnlySource = "only_source"
	CategoryOnlyTarget = "only_target"
)

// MaskedValue replaces the content of masked columns in reported rows.
const MaskedValue = "***"

var ErrNoKey = errors.New("table has no usable key for row comparison")

// Side is one of the two tables being compared.
type Side struct {
	DB      *sql.DB
	Dialect Dialect
	Schema  string
	Table   *Table
}

func (s Side) ref() string {
	return s.Dialect.TableRef(s.Schema, s.Table.Name)
}

func (s Side) columns(names []string) []*Column {
	cols := make([]*Column, len(names))
	for i, n := range names {
		cols[i] = s.Table.Column(n)
	}
	return cols
}

// TablePlan fixes which columns are compared and which identify a row.
// Columns always starts with KeyColumns.
type TablePlan struct {
	Table      string
	KeyColumns []string
	Columns    []string
	Masked     []bool
	Notes      []string
}

func (p TablePlan) HasKey() bool { return len(p.KeyColumns) > 0 }

// PlanTable chooses the key and the compared columns for a table that exists
// on both sides. Only columns present on both sides are compared.
func PlanTable(src, tgt *Table, opts Options) TablePlan {
	plan := TablePlan{Table: src.Name}
	key, note := chooseKey(src, tgt, opts)
	plan.KeyColumns = key
	if note != "" {
		plan.Notes = append(plan.Notes, note)
	}

	isKey := map[string]bool{}
	for _, k := range key {
		isKey[k] = true
	}
	plan.Columns = append(plan.Columns, key...)
	for _, c := range src.Columns {
		switch {
		case isKey[c.Name]:
		case tgt.Column(c.Name) == nil:
			plan.Notes = append(plan.Notes, fmt.Sprintf("column %s exists only in source and is not compared", c.Name))
		case opts.columnIgnored(src.Name, c.Name):
			plan.Notes = append(plan.Notes, fmt.Sprintf("column %s is ignored", c.Name))
		default:
			plan.Columns = append(plan.Columns, c.Name)
		}
	}
	for _, c := range tgt.Columns {
		if src.Column(c.Name) == nil {
			plan.Notes = append(plan.Notes, fmt.Sprintf("column %s exists only in target and is not compared", c.Name))
		}
	}

	plan.Masked = make([]bool, len(plan.Columns))
	for i, c := range plan.Columns {
		plan.Masked[i] = opts.columnMasked(src.Name, c)
	}
	return plan
}

func chooseKey(src, tgt *Table, opts Options) ([]string, string) {
	if cols, ok := opts.keyOverride(src.Name); ok {
		for _, c := range cols {
			if src.Column(c) == nil || tgt.Column(c) == nil {
				return nil, fmt.Sprintf("key column %s does not exist on both sides", c)
			}
		}
		if note := keyTypeMismatch(src, tgt, cols); note != "" {
			return nil, note
		}
		return cols, "using key columns chosen by the user"
	}

	if len(src.PrimaryKey) > 0 && equalStrings(src.PrimaryKey, tgt.PrimaryKey) {
		if note := keyTypeMismatch(src, tgt, src.PrimaryKey); note != "" {
			return nil, note
		}
		return src.PrimaryKey, ""
	}

	for _, idx := range src.Indexes {
		if !idx.Unique || !idx.Simple || !notNullOnBothSides(src, tgt, idx.Columns) {
			continue
		}
		if !hasSimpleUniqueIndex(tgt, idx.Columns) || keyTypeMismatch(src, tgt, idx.Columns) != "" {
			continue
		}
		return idx.Columns, fmt.Sprintf("no matching primary key; using unique index %s", idx.Name)
	}

	return nil, "no matching primary key or usable unique index; choose key columns to compare rows"
}

func keyTypeMismatch(src, tgt *Table, cols []string) string {
	for _, c := range cols {
		if src.Column(c).DataType != tgt.Column(c).DataType {
			return fmt.Sprintf("key column %s has different types (%s vs %s)", c, src.Column(c).DataType, tgt.Column(c).DataType)
		}
	}
	return ""
}

func notNullOnBothSides(src, tgt *Table, cols []string) bool {
	for _, c := range cols {
		s, t := src.Column(c), tgt.Column(c)
		if s == nil || t == nil || s.Nullable || t.Nullable {
			return false
		}
	}
	return true
}

func hasSimpleUniqueIndex(t *Table, cols []string) bool {
	for _, idx := range t.Indexes {
		if idx.Unique && idx.Simple && equalStrings(idx.Columns, cols) {
			return true
		}
	}
	return false
}

type Summary struct {
	SourceRows int64
	TargetRows int64
	Identical  bool
}

// Summarize counts and checksums the whole table on both sides in parallel.
// The work happens inside the databases; no rows are transferred.
func Summarize(ctx context.Context, plan TablePlan, src, tgt Side) (Summary, error) {
	var s Summary
	var srcSum, tgtSum string
	err := parallel(
		func() (err error) {
			s.SourceRows, srcSum, err = checksum(ctx, src, plan, keyRange{})
			return err
		},
		func() (err error) {
			s.TargetRows, tgtSum, err = checksum(ctx, tgt, plan, keyRange{})
			return err
		},
	)
	if err != nil {
		return s, err
	}
	s.Identical = s.SourceRows == s.TargetRows && srcSum == tgtSum
	return s, nil
}

func checksum(ctx context.Context, s Side, plan TablePlan, r keyRange) (int64, string, error) {
	where, args, err := r.where(s, plan)
	if err != nil {
		return 0, "", err
	}
	// With no common columns only the row counts can be compared.
	sumExpr := "'-'"
	if len(plan.Columns) > 0 {
		sumExpr = s.Dialect.ChecksumExpr(s.columns(plan.Columns))
	}
	query := "SELECT COUNT(*), " + sumExpr + " FROM " + s.ref() + where
	var count int64
	var sum sql.NullString
	if err := s.DB.QueryRowContext(ctx, query, args...).Scan(&count, &sum); err != nil {
		return 0, "", err
	}
	return count, sum.String, nil
}

// keyRange selects rows with lower < key <= upper. A nil bound is open.
type keyRange struct {
	lower, upper []string
}

func (r keyRange) where(s Side, plan TablePlan) (string, []any, error) {
	keys := s.columns(plan.KeyColumns)
	var conds []string
	var args []any
	add := func(op string, values []string) error {
		sorts := make([]string, len(keys))
		params := make([]string, len(keys))
		for i, c := range keys {
			arg, err := s.Dialect.KeyArg(c, values[i])
			if err != nil {
				return err
			}
			args = append(args, arg)
			sorts[i] = s.Dialect.SortExpr(c)
			params[i] = s.Dialect.ParamExpr(c, len(args))
		}
		conds = append(conds, "("+strings.Join(sorts, ", ")+") "+op+" ("+strings.Join(params, ", ")+")")
		return nil
	}
	if r.lower != nil {
		if err := add(">", r.lower); err != nil {
			return "", nil, err
		}
	}
	if r.upper != nil {
		if err := add("<=", r.upper); err != nil {
			return "", nil, err
		}
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

type Counts struct {
	Identical  int64 `json:"identical"`
	Different  int64 `json:"different"`
	OnlySource int64 `json:"only_source"`
	OnlyTarget int64 `json:"only_target"`
}

// RowDiff is one reported row. Source and Target are aligned with
// TablePlan.Columns; the side that lacks the row is nil.
type RowDiff struct {
	Category    string
	Key         []string
	Source      []*string
	Target      []*string
	DiffColumns []string
}

type RowDiffProgress struct {
	Table       string `json:"table"`
	ChunksDone  int    `json:"chunks_done"`
	ChunksTotal int    `json:"chunks_total"`
	RowsScanned int64  `json:"rows_scanned"`
	Counts
}

type RowDiffConfig struct {
	ChunkSize int
	// Limit caps how many differing rows are passed to Sink. Counting
	// continues past the limit.
	Limit        int
	QueryTimeout time.Duration
	Sink         func(context.Context, []RowDiff) error
	Progress     func(RowDiffProgress)
}

type RowDiffResult struct {
	Counts
	Truncated bool
}

const (
	sinkBatchSize          = 500
	progressInterval       = time.Second
	progressRowGranularity = 5_000
)

// DiffRows compares a table row by row. Tables larger than one chunk are
// split into key ranges using the source keys; ranges whose checksums match
// are skipped, and the rest are merged as two key-ordered streams.
func DiffRows(ctx context.Context, plan TablePlan, src, tgt Side, sum Summary, cfg RowDiffConfig) (RowDiffResult, error) {
	if !plan.HasKey() {
		return RowDiffResult{}, ErrNoKey
	}
	d := &rowDiffer{plan: plan, src: src, tgt: tgt, cfg: cfg}
	for _, c := range src.columns(plan.KeyColumns) {
		d.kinds = append(d.kinds, src.Dialect.KeyKind(c))
	}
	d.progress.Table = plan.Table

	ranges := []keyRange{{}}
	if max(sum.SourceRows, sum.TargetRows) > int64(cfg.ChunkSize) {
		var err error
		if ranges, err = d.chunkRanges(ctx); err != nil {
			return d.result, fmt.Errorf("split table into chunks: %w", err)
		}
	}
	d.progress.ChunksTotal = len(ranges)
	d.report(true)

	for _, r := range ranges {
		if len(ranges) > 1 {
			same, rows, err := d.rangeUnchanged(ctx, r)
			if err != nil {
				return d.result, err
			}
			if same {
				d.result.Identical += rows
				d.progress.RowsScanned += rows
				d.progress.ChunksDone++
				d.report(true)
				continue
			}
		}
		if err := d.mergeRange(ctx, r); err != nil {
			return d.result, err
		}
		d.progress.ChunksDone++
		d.report(true)
	}
	return d.result, d.flush(ctx)
}

type rowDiffer struct {
	plan       TablePlan
	src, tgt   Side
	cfg        RowDiffConfig
	kinds      []KeyKind
	result     RowDiffResult
	progress   RowDiffProgress
	stored     int
	buf        []RowDiff
	lastReport time.Time
}

func (d *rowDiffer) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if d.cfg.QueryTimeout > 0 {
		return context.WithTimeout(ctx, d.cfg.QueryTimeout)
	}
	return context.WithCancel(ctx)
}

func (d *rowDiffer) chunkRanges(ctx context.Context) ([]keyRange, error) {
	ctx, cancel := d.queryContext(ctx)
	defer cancel()

	s := d.src
	keys := s.columns(d.plan.KeyColumns)
	sel := make([]string, len(keys))
	order := make([]string, len(keys))
	for i, c := range keys {
		sel[i] = s.Dialect.TextExpr(c)
		order[i] = s.Dialect.SortExpr(c)
	}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT "+strings.Join(sel, ", ")+" FROM "+s.ref()+" ORDER BY "+strings.Join(order, ", "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	vals := make([]sql.NullString, len(keys))
	dest := make([]any, len(keys))
	for i := range vals {
		dest[i] = &vals[i]
	}
	var bounds [][]string
	var n int64
	for rows.Next() {
		n++
		if n%int64(d.cfg.ChunkSize) != 0 {
			continue
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		key := make([]string, len(vals))
		for i, v := range vals {
			key[i] = v.String
		}
		bounds = append(bounds, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ranges := make([]keyRange, 0, len(bounds)+1)
	var lower []string
	for _, b := range bounds {
		ranges = append(ranges, keyRange{lower: lower, upper: b})
		lower = b
	}
	return append(ranges, keyRange{lower: lower}), nil
}

func (d *rowDiffer) rangeUnchanged(ctx context.Context, r keyRange) (bool, int64, error) {
	ctx, cancel := d.queryContext(ctx)
	defer cancel()
	var srcRows, tgtRows int64
	var srcSum, tgtSum string
	err := parallel(
		func() (err error) {
			srcRows, srcSum, err = checksum(ctx, d.src, d.plan, r)
			return err
		},
		func() (err error) {
			tgtRows, tgtSum, err = checksum(ctx, d.tgt, d.plan, r)
			return err
		},
	)
	if err != nil {
		return false, 0, fmt.Errorf("checksum chunk: %w", err)
	}
	return srcRows == tgtRows && srcSum == tgtSum, srcRows, nil
}

func (d *rowDiffer) mergeRange(ctx context.Context, r keyRange) error {
	ctx, cancel := d.queryContext(ctx)
	defer cancel()

	sr, err := openRowReader(ctx, d.src, d.plan, r, d.kinds)
	if err != nil {
		return fmt.Errorf("read source rows: %w", err)
	}
	defer sr.close()
	tr, err := openRowReader(ctx, d.tgt, d.plan, r, d.kinds)
	if err != nil {
		return fmt.Errorf("read target rows: %w", err)
	}
	defer tr.close()

	if err := sr.next(); err != nil {
		return fmt.Errorf("read source rows: %w", err)
	}
	if err := tr.next(); err != nil {
		return fmt.Errorf("read target rows: %w", err)
	}

	for sr.row != nil || tr.row != nil {
		var c int
		switch {
		case sr.row == nil:
			c = 1
		case tr.row == nil:
			c = -1
		default:
			if c, err = compareKey(d.kinds, sr.key, tr.key); err != nil {
				return err
			}
		}

		switch {
		case c < 0:
			err = d.record(ctx, CategoryOnlySource, sr.key, sr.row, nil, nil)
			if err == nil {
				err = sr.next()
			}
			d.progress.RowsScanned++
		case c > 0:
			err = d.record(ctx, CategoryOnlyTarget, tr.key, nil, tr.row, nil)
			if err == nil {
				err = tr.next()
			}
			d.progress.RowsScanned++
		default:
			err = d.compareRows(ctx, sr.key, sr.row, tr.row)
			if err == nil {
				err = sr.next()
			}
			if err == nil {
				err = tr.next()
			}
			d.progress.RowsScanned++
		}
		if err != nil {
			return err
		}
		if d.progress.RowsScanned%progressRowGranularity == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			d.report(false)
		}
	}
	return nil
}

func (d *rowDiffer) compareRows(ctx context.Context, key []string, a, b []*string) error {
	var changed []string
	for i := len(d.plan.KeyColumns); i < len(d.plan.Columns); i++ {
		if !equalValues(a[i], b[i]) {
			changed = append(changed, d.plan.Columns[i])
		}
	}
	if len(changed) == 0 {
		d.result.Identical++
		return nil
	}
	return d.record(ctx, CategoryDifferent, key, a, b, changed)
}

func (d *rowDiffer) record(ctx context.Context, category string, key []string, src, tgt []*string, changed []string) error {
	switch category {
	case CategoryDifferent:
		d.result.Different++
	case CategoryOnlySource:
		d.result.OnlySource++
	case CategoryOnlyTarget:
		d.result.OnlyTarget++
	}
	if d.stored >= d.cfg.Limit {
		d.result.Truncated = true
		return nil
	}
	d.stored++

	displayKey := make([]string, len(key))
	for i, k := range key {
		displayKey[i] = DisplayValue(k)
		if d.plan.Masked[i] {
			displayKey[i] = MaskedValue
		}
	}
	d.buf = append(d.buf, RowDiff{
		Category:    category,
		Key:         displayKey,
		Source:      d.displayRow(src),
		Target:      d.displayRow(tgt),
		DiffColumns: changed,
	})
	if len(d.buf) >= sinkBatchSize {
		return d.flush(ctx)
	}
	return nil
}

func (d *rowDiffer) displayRow(row []*string) []*string {
	if row == nil {
		return nil
	}
	out := make([]*string, len(row))
	for i, v := range row {
		if v == nil {
			continue
		}
		s := DisplayValue(*v)
		if d.plan.Masked[i] {
			s = MaskedValue
		}
		out[i] = &s
	}
	return out
}

func (d *rowDiffer) flush(ctx context.Context) error {
	if len(d.buf) == 0 || d.cfg.Sink == nil {
		d.buf = d.buf[:0]
		return nil
	}
	err := d.cfg.Sink(ctx, d.buf)
	d.buf = d.buf[:0]
	return err
}

func (d *rowDiffer) report(force bool) {
	if d.cfg.Progress == nil {
		return
	}
	if !force && time.Since(d.lastReport) < progressInterval {
		return
	}
	d.lastReport = time.Now()
	d.progress.Counts = d.result.Counts
	d.cfg.Progress(d.progress)
}

type rowReader struct {
	rows  *sql.Rows
	kinds []KeyKind
	vals  []sql.NullString
	dest  []any
	row   []*string
	key   []string
}

func openRowReader(ctx context.Context, s Side, plan TablePlan, r keyRange, kinds []KeyKind) (*rowReader, error) {
	cols := s.columns(plan.Columns)
	sel := make([]string, len(cols))
	for i, c := range cols {
		sel[i] = s.Dialect.TextExpr(c)
	}
	keys := s.columns(plan.KeyColumns)
	order := make([]string, len(keys))
	for i, c := range keys {
		order[i] = s.Dialect.SortExpr(c)
	}
	where, args, err := r.where(s, plan)
	if err != nil {
		return nil, err
	}
	query := "SELECT " + strings.Join(sel, ", ") + " FROM " + s.ref() + where + " ORDER BY " + strings.Join(order, ", ")
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	rr := &rowReader{rows: rows, kinds: kinds, vals: make([]sql.NullString, len(cols)), dest: make([]any, len(cols))}
	for i := range rr.vals {
		rr.dest[i] = &rr.vals[i]
	}
	return rr, nil
}

// next advances to the following row, or sets row to nil at the end. It
// fails when rows do not arrive in strictly ascending key order, because the
// merge would otherwise report wrong results.
func (r *rowReader) next() error {
	if !r.rows.Next() {
		r.row, r.key = nil, nil
		return r.rows.Err()
	}
	if err := r.rows.Scan(r.dest...); err != nil {
		return err
	}
	row := make([]*string, len(r.vals))
	for i, v := range r.vals {
		if v.Valid {
			s := v.String
			row[i] = &s
		}
	}
	key := make([]string, len(r.kinds))
	for i := range key {
		if row[i] == nil {
			return errors.New("a key column contains NULL; choose key columns without NULL values")
		}
		key[i] = *row[i]
	}
	if r.key != nil {
		c, err := compareKey(r.kinds, r.key, key)
		if err != nil {
			return err
		}
		if c >= 0 {
			return fmt.Errorf("rows are not in strictly ascending key order (%v after %v); the key is not unique or the database orders it differently", key, r.key)
		}
	}
	r.row, r.key = row, key
	return nil
}

func (r *rowReader) close() {
	_ = r.rows.Close()
}

func equalValues(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// DisplayValue returns v unchanged when it is printable text, and a hex
// literal otherwise (invalid UTF-8 or NUL bytes, which JSON storage rejects).
func DisplayValue(v string) string {
	if utf8.ValidString(v) && !strings.ContainsRune(v, 0) {
		return v
	}
	return "0x" + hex.EncodeToString([]byte(v))
}

func parallel(fns ...func() error) error {
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = fn()
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}
