// Package runner executes compare runs inside the lifetime of the calling
// request. Progress is reported through an Emit callback, results are written
// to the store as they are produced, and cancelling the context stops the
// run.
package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"dbcompare/internal/engine"
	"dbcompare/internal/store"
)

var (
	ErrBusy         = errors.New("another compare is already running for this project")
	ErrNotResumable = errors.New("run has already completed; start a new run instead")
	ErrNoKey        = errors.New("table has no key columns; choose key columns first")
)

// Event is one progress message for the client.
type Event struct {
	Name string
	Data any
}

// Emit must be safe for concurrent use.
type Emit func(Event)

type Config struct {
	MaxParallelism    int
	QueryTimeout      time.Duration
	HeartbeatInterval time.Duration
}

type Manager struct {
	store *store.Store
	pools *Pools
	cfg   Config

	mu sync.Mutex
	// active maps a project ID to the work currently running for it. Only
	// one run or table comparison per project runs at a time.
	active map[int64]*activeWork
}

type activeWork struct {
	runID  int64
	cancel context.CancelFunc
}

func NewManager(st *store.Store, pools *Pools, cfg Config) *Manager {
	return &Manager{store: st, pools: pools, cfg: cfg, active: map[int64]*activeWork{}}
}

func (m *Manager) acquire(projectID, runID int64, cancel context.CancelFunc) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[projectID] != nil {
		return ErrBusy
	}
	m.active[projectID] = &activeWork{runID: runID, cancel: cancel}
	return nil
}

func (m *Manager) release(projectID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.active, projectID)
}

// Cancel stops work on the run and reports whether any was running.
func (m *Manager) Cancel(runID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.active {
		if w.runID == runID {
			w.cancel()
			return true
		}
	}
	return false
}

func (m *Manager) CancelAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.active {
		w.cancel()
	}
}

func (m *Manager) ProjectActive(projectID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[projectID] != nil
}

func (m *Manager) IsActive(runID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.active {
		if w.runID == runID {
			return true
		}
	}
	return false
}

// CheckExecutable validates that a run may be executed now, so that callers
// can report an error before they start streaming.
func (m *Manager) CheckExecutable(run store.Run) error {
	if m.IsActive(run.ID) {
		return ErrBusy
	}
	// A run still marked running with no active work was left behind by a
	// stopped server and may be resumed.
	if !run.Resumable() && run.Status != store.RunRunning {
		return ErrNotResumable
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[run.ProjectID] != nil {
		return ErrBusy
	}
	return nil
}

type target struct {
	conn    store.Connection
	side    engine.Side
	version string
}

func (m *Manager) openTargets(ctx context.Context, project store.Project) (src, tgt target, err error) {
	for _, t := range []struct {
		id  int64
		out *target
	}{{project.SourceConnectionID, &src}, {project.TargetConnectionID, &tgt}} {
		conn, err := m.store.GetConnection(ctx, t.id)
		if err != nil {
			return src, tgt, fmt.Errorf("load connection %d: %w", t.id, err)
		}
		db, dialect, info, err := m.pools.Get(ctx, conn)
		if err != nil {
			return src, tgt, fmt.Errorf("open %s: %w", conn.Name, err)
		}
		version, err := dialect.ServerVersion(ctx, db)
		if err != nil {
			return src, tgt, fmt.Errorf("connect to %s: %w", conn.Name, err)
		}
		if err := m.store.SetServerVersion(ctx, conn.ID, version); err != nil {
			slog.Warn("save server version", "connection", conn.ID, "err", err)
		}
		*t.out = target{
			conn:    conn,
			side:    engine.Side{DB: db, Dialect: dialect, Schema: info.SchemaName()},
			version: version,
		}
	}
	if src.conn.Engine != tgt.conn.Engine {
		return src, tgt, fmt.Errorf("source is %s but target is %s; both must use the same engine", src.conn.Engine, tgt.conn.Engine)
	}
	return src, tgt, nil
}

func inspectBoth(ctx context.Context, src, tgt target, tables []string) (*engine.Schema, *engine.Schema, error) {
	var ss, ts *engine.Schema
	var srcErr, tgtErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ss, srcErr = src.side.Dialect.Inspect(ctx, src.side.DB, src.side.Schema, tables)
	}()
	go func() {
		defer wg.Done()
		ts, tgtErr = tgt.side.Dialect.Inspect(ctx, tgt.side.DB, tgt.side.Schema, tables)
	}()
	wg.Wait()
	if srcErr != nil {
		return nil, nil, fmt.Errorf("inspect source: %w", srcErr)
	}
	if tgtErr != nil {
		return nil, nil, fmt.Errorf("inspect target: %w", tgtErr)
	}
	return ss, ts, nil
}

// ExecuteRun runs or resumes a compare. Work already recorded for the run is
// skipped. The returned error is also reported through emit.
func (m *Manager) ExecuteRun(ctx context.Context, runID int64, emit Emit) error {
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if err := m.CheckExecutable(run); err != nil {
		return err
	}
	project, err := m.store.GetProject(ctx, run.ProjectID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := m.acquire(project.ID, run.ID, cancel); err != nil {
		return err
	}
	defer m.release(project.ID)

	x := &execution{
		m:        m,
		run:      run,
		opts:     run.Options.WithDefaults(m.cfg.MaxParallelism),
		emit:     emit,
		progress: run.Progress,
	}
	return x.execute(ctx, project)
}

type execution struct {
	m    *Manager
	run  store.Run
	opts engine.Options
	emit Emit

	mu       sync.Mutex
	progress store.RunProgress
}

func (x *execution) snapshot() store.RunProgress {
	x.mu.Lock()
	defer x.mu.Unlock()
	p := x.progress
	p.ActiveTables = append([]string{}, x.progress.ActiveTables...)
	sort.Strings(p.ActiveTables)
	return p
}

func (x *execution) update(fn func(p *store.RunProgress)) store.RunProgress {
	x.mu.Lock()
	fn(&x.progress)
	x.mu.Unlock()
	return x.snapshot()
}

func (x *execution) setPhase(phase string) {
	x.emit(Event{"phase", x.update(func(p *store.RunProgress) { p.Phase = phase })})
}

func (x *execution) execute(ctx context.Context, project store.Project) error {
	bg := context.WithoutCancel(ctx)
	resumed := x.run.Status != store.RunPending
	if err := x.m.store.MarkRunStarted(bg, x.run.ID); err != nil {
		return err
	}
	x.emit(Event{"run_started", map[string]any{"run_id": x.run.ID, "resumed": resumed}})

	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	go x.heartbeat(hbCtx)
	workErr := x.work(ctx, project)
	stopHeartbeat()

	status, message := store.RunCompleted, ""
	switch {
	case ctx.Err() != nil:
		status = store.RunCancelled
	case workErr != nil:
		status, message = store.RunFailed, workErr.Error()
	}
	progress := x.update(func(p *store.RunProgress) {
		p.Phase = status
		p.ActiveTables = nil
	})
	if err := x.m.store.FinishRun(bg, x.run.ID, status, message, progress); err != nil {
		slog.Error("finish run", "run", x.run.ID, "err", err)
	}
	summary, err := x.m.store.RunSummary(bg, x.run.ID)
	if err != nil {
		slog.Error("summarize run", "run", x.run.ID, "err", err)
	}
	x.emit(Event{"done", map[string]any{"status": status, "error": message, "summary": summary, "progress": progress}})
	if status == store.RunFailed {
		return workErr
	}
	return nil
}

func (x *execution) heartbeat(ctx context.Context) {
	t := time.NewTicker(x.m.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := x.m.store.Heartbeat(context.WithoutCancel(ctx), x.run.ID, x.snapshot()); err != nil {
				slog.Warn("heartbeat", "run", x.run.ID, "err", err)
			}
		}
	}
}

func (x *execution) work(ctx context.Context, project store.Project) error {
	x.setPhase("connect")
	src, tgt, err := x.m.openTargets(ctx, project)
	if err != nil {
		return err
	}
	x.emit(Event{"connected", map[string]any{"source_version": src.version, "target_version": tgt.version}})

	x.setPhase("inspect")
	srcSchema, tgtSchema, err := inspectBoth(ctx, src, tgt, nil)
	if err != nil {
		return err
	}

	if !x.snapshot().SchemaDone {
		x.setPhase("schema")
		items := engine.DiffSchemas(srcSchema, tgtSchema, x.opts)
		if err := x.m.store.ReplaceSchemaItems(ctx, x.run.ID, items); err != nil {
			return fmt.Errorf("save schema results: %w", err)
		}
		progress := x.update(func(p *store.RunProgress) { p.SchemaDone = true })
		if err := x.m.store.Heartbeat(ctx, x.run.ID, progress); err != nil {
			return err
		}
		counts := map[string]int{}
		for _, it := range items {
			counts[it.Status]++
		}
		x.emit(Event{"schema_done", map[string]any{"counts": counts, "total": len(items)}})
	}

	x.setPhase("data")
	var tables []string
	for name := range srcSchema.Tables {
		if tgtSchema.Tables[name] != nil && x.opts.TableIncluded(name) {
			tables = append(tables, name)
		}
	}
	sort.Strings(tables)

	existing, err := x.m.store.ListTableResults(ctx, x.run.ID)
	if err != nil {
		return err
	}
	finished := map[string]bool{}
	for _, t := range existing {
		dataDone := t.DataStatus == store.DataIdentical || t.DataStatus == store.DataDifferent
		if dataDone && (!x.opts.IncludeRowDiff || t.RowDiffComplete()) {
			finished[t.TableName] = true
		}
	}
	var pending []string
	for _, t := range tables {
		if !finished[t] {
			pending = append(pending, t)
		}
	}
	progress := x.update(func(p *store.RunProgress) {
		p.TablesTotal = len(tables)
		p.TablesDone = len(tables) - len(pending)
	})
	x.emit(Event{"data_started", progress})

	queue := make(chan string)
	var wg sync.WaitGroup
	for range min(x.opts.Parallelism, max(len(pending), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range queue {
				x.compareTable(ctx, name, src, tgt, srcSchema.Tables[name], tgtSchema.Tables[name])
			}
		}()
	}
feed:
	for _, name := range pending {
		select {
		case queue <- name:
		case <-ctx.Done():
			break feed
		}
	}
	close(queue)
	wg.Wait()
	return ctx.Err()
}

func (x *execution) compareTable(ctx context.Context, name string, src, tgt target, st, tt *engine.Table) {
	if ctx.Err() != nil {
		return
	}
	x.emit(Event{"table_started", x.update(func(p *store.RunProgress) {
		p.ActiveTables = append(p.ActiveTables, name)
	})})
	defer func() {
		progress := x.update(func(p *store.RunProgress) {
			p.ActiveTables = removeString(p.ActiveTables, name)
			if ctx.Err() == nil {
				p.TablesDone++
			}
		})
		x.emit(Event{"progress", progress})
	}()

	bg := context.WithoutCancel(ctx)
	start := time.Now()
	plan := engine.PlanTable(st, tt, x.opts)
	res := store.TableResult{
		RunID:         x.run.ID,
		TableName:     name,
		KeyColumns:    plan.KeyColumns,
		Columns:       plan.Columns,
		Notes:         plan.Notes,
		DataStatus:    store.DataPending,
		RowDiffStatus: store.RowDiffNotRun,
	}
	if !plan.HasKey() {
		res.RowDiffStatus = store.RowDiffNeedsKey
	}
	srcSide, tgtSide := src.side, tgt.side
	srcSide.Table, tgtSide.Table = st, tt

	qctx, cancel := x.m.queryContext(ctx)
	sum, err := engine.Summarize(qctx, plan, srcSide, tgtSide)
	cancel()
	if ctx.Err() != nil {
		return
	}
	res.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		res.DataStatus, res.Error = store.DataError, err.Error()
		x.save(bg, res)
		x.emit(Event{"table_error", res})
		return
	}

	res.SourceRows, res.TargetRows = &sum.SourceRows, &sum.TargetRows
	if sum.Identical {
		res.DataStatus = store.DataIdentical
		res.Identical = sum.SourceRows
		if res.RowDiffStatus == store.RowDiffNotRun {
			res.RowDiffStatus = store.RowDiffNotNeeded
		}
	} else {
		res.DataStatus = store.DataDifferent
	}
	x.save(bg, res)
	x.emit(Event{"table_done", res})

	if x.opts.IncludeRowDiff && res.DataStatus == store.DataDifferent && plan.HasKey() {
		_ = x.m.diffTableRows(ctx, &res, plan, srcSide, tgtSide, sum, x.opts, x.emit)
	}
}

func (x *execution) save(ctx context.Context, res store.TableResult) {
	if err := x.m.store.SaveTableResult(ctx, res); err != nil {
		slog.Error("save table result", "run", res.RunID, "table", res.TableName, "err", err)
	}
}

func (m *Manager) queryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if m.cfg.QueryTimeout > 0 {
		return context.WithTimeout(ctx, m.cfg.QueryTimeout)
	}
	return context.WithCancel(ctx)
}

// diffTableRows replaces the stored row differences of one table.
func (m *Manager) diffTableRows(ctx context.Context, res *store.TableResult, plan engine.TablePlan,
	src, tgt engine.Side, sum engine.Summary, opts engine.Options, emit Emit) error {
	bg := context.WithoutCancel(ctx)
	if err := m.store.DeleteRowDiffs(ctx, res.RunID, res.TableName); err != nil {
		return err
	}
	res.RowDiffStatus = store.RowDiffRunning
	res.Counts = engine.Counts{}
	res.Truncated = false
	res.Error = ""
	if err := m.store.SaveTableResult(ctx, *res); err != nil {
		return err
	}
	emit(Event{"rowdiff_started", *res})

	start := time.Now()
	result, err := engine.DiffRows(ctx, plan, src, tgt, sum, engine.RowDiffConfig{
		ChunkSize:    opts.ChunkSize,
		Limit:        opts.RowDiffLimit,
		QueryTimeout: m.cfg.QueryTimeout,
		Sink: func(ctx context.Context, diffs []engine.RowDiff) error {
			return m.store.InsertRowDiffs(ctx, res.RunID, res.TableName, diffs)
		},
		Progress: func(p engine.RowDiffProgress) {
			emit(Event{"rowdiff_progress", p})
		},
	})

	res.Counts = result.Counts
	res.Truncated = result.Truncated
	res.DurationMs += time.Since(start).Milliseconds()
	switch {
	case ctx.Err() != nil:
		res.RowDiffStatus = store.RowDiffCancelled
	case err != nil:
		res.RowDiffStatus, res.Error = store.RowDiffError, err.Error()
	default:
		res.RowDiffStatus = store.RowDiffDone
	}
	if saveErr := m.store.SaveTableResult(bg, *res); saveErr != nil {
		slog.Error("save table result", "run", res.RunID, "table", res.TableName, "err", saveErr)
	}
	emit(Event{"rowdiff_done", *res})
	return err
}

// ExecuteTableRowDiff compares the rows of one table of an existing run.
// keyColumns, when set, overrides the key chosen for the run.
func (m *Manager) ExecuteTableRowDiff(ctx context.Context, runID int64, table string, keyColumns []string, emit Emit) error {
	run, err := m.store.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == store.RunRunning || m.IsActive(runID) {
		return ErrBusy
	}
	project, err := m.store.GetProject(ctx, run.ProjectID)
	if err != nil {
		return err
	}
	res, err := m.store.GetTableResult(ctx, runID, table)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := m.acquire(project.ID, run.ID, cancel); err != nil {
		return err
	}
	defer m.release(project.ID)

	opts := run.Options.WithDefaults(m.cfg.MaxParallelism)
	if len(keyColumns) > 0 {
		overrides := map[string][]string{}
		for k, v := range opts.KeyOverrides {
			overrides[k] = v
		}
		overrides[table] = keyColumns
		opts.KeyOverrides = overrides
	}

	emit(Event{"phase", map[string]any{"phase": "connect", "table": table}})
	src, tgt, err := m.openTargets(ctx, project)
	if err != nil {
		return err
	}
	emit(Event{"phase", map[string]any{"phase": "inspect", "table": table}})
	srcSchema, tgtSchema, err := inspectBoth(ctx, src, tgt, []string{table})
	if err != nil {
		return err
	}
	st, tt := srcSchema.Tables[table], tgtSchema.Tables[table]
	if st == nil || tt == nil {
		return fmt.Errorf("table %s no longer exists on both sides", table)
	}

	plan := engine.PlanTable(st, tt, opts)
	res.KeyColumns, res.Columns, res.Notes = plan.KeyColumns, plan.Columns, plan.Notes
	if !plan.HasKey() {
		res.RowDiffStatus = store.RowDiffNeedsKey
		if err := m.store.SaveTableResult(ctx, res); err != nil {
			return err
		}
		emit(Event{"rowdiff_done", res})
		return ErrNoKey
	}

	srcSide, tgtSide := src.side, tgt.side
	srcSide.Table, tgtSide.Table = st, tt

	// Row counts only decide whether to split the table into chunks, so
	// stored counts are good enough when present.
	var sum engine.Summary
	if res.SourceRows != nil && res.TargetRows != nil {
		sum.SourceRows, sum.TargetRows = *res.SourceRows, *res.TargetRows
	} else {
		emit(Event{"phase", map[string]any{"phase": "summary", "table": table}})
		qctx, qcancel := m.queryContext(ctx)
		sum, err = engine.Summarize(qctx, plan, srcSide, tgtSide)
		qcancel()
		if err != nil {
			return err
		}
		res.SourceRows, res.TargetRows = &sum.SourceRows, &sum.TargetRows
		res.DataStatus = store.DataDifferent
		if sum.Identical {
			res.DataStatus = store.DataIdentical
		}
	}

	emit(Event{"phase", map[string]any{"phase": "rowdiff", "table": table}})
	return m.diffTableRows(ctx, &res, plan, srcSide, tgtSide, sum, opts, emit)
}

func removeString(list []string, s string) []string {
	out := list[:0]
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}
