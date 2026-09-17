package engine

import (
	"path"
	"strings"
)

// Options controls what a compare run covers. Table and column patterns are
// case-insensitive shell globs. Column patterns take the form "table.column";
// a pattern without a dot applies to every table.
type Options struct {
	IncludeTables    []string            `json:"include_tables"`
	ExcludeTables    []string            `json:"exclude_tables"`
	IgnoreColumns    []string            `json:"ignore_columns"`
	MaskColumns      []string            `json:"mask_columns"`
	KeyOverrides     map[string][]string `json:"key_overrides"`
	CheckColumnOrder bool                `json:"check_column_order"`
	SkipViews        bool                `json:"skip_views"`
	SkipRoutines     bool                `json:"skip_routines"`
	IncludeRowDiff   bool                `json:"include_row_diff"`
	RowDiffLimit     int                 `json:"row_diff_limit"`
	ChunkSize        int                 `json:"chunk_size"`
	Parallelism      int                 `json:"parallelism"`
}

const (
	DefaultRowDiffLimit = 100_000
	DefaultChunkSize    = 100_000
	DefaultParallelism  = 4
	MinChunkSize        = 1_000
)

// WithDefaults fills unset limits and caps parallelism at maxParallelism.
func (o Options) WithDefaults(maxParallelism int) Options {
	if o.RowDiffLimit <= 0 {
		o.RowDiffLimit = DefaultRowDiffLimit
	}
	if o.ChunkSize <= 0 {
		o.ChunkSize = DefaultChunkSize
	}
	if o.ChunkSize < MinChunkSize {
		o.ChunkSize = MinChunkSize
	}
	if o.Parallelism <= 0 {
		o.Parallelism = DefaultParallelism
	}
	if maxParallelism > 0 && o.Parallelism > maxParallelism {
		o.Parallelism = maxParallelism
	}
	return o
}

func (o Options) TableIncluded(name string) bool {
	if len(o.IncludeTables) > 0 && !matchAny(o.IncludeTables, name) {
		return false
	}
	return !matchAny(o.ExcludeTables, name)
}

func (o Options) columnIgnored(table, column string) bool {
	return matchColumn(o.IgnoreColumns, table, column)
}

func (o Options) columnMasked(table, column string) bool {
	return matchColumn(o.MaskColumns, table, column)
}

func (o Options) keyOverride(table string) ([]string, bool) {
	if cols, ok := o.KeyOverrides[table]; ok && len(cols) > 0 {
		return cols, true
	}
	return nil, false
}

func matchAny(patterns []string, name string) bool {
	name = strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(strings.TrimSpace(p)), name); ok {
			return true
		}
	}
	return false
}

func matchColumn(patterns []string, table, column string) bool {
	for _, p := range patterns {
		tablePattern, columnPattern := "*", p
		if i := strings.LastIndex(p, "."); i >= 0 {
			tablePattern, columnPattern = p[:i], p[i+1:]
		}
		if matchAny([]string{tablePattern}, table) && matchAny([]string{columnPattern}, column) {
			return true
		}
	}
	return false
}
