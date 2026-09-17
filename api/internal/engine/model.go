// Package engine compares the structure and data of two databases that run
// on the same engine. It has no knowledge of HTTP or of where results are
// stored; callers receive results through return values and callbacks.
package engine

// Column describes one table column. Definition is the engine-specific
// rendering (type, nullability, default, extras) used for structural diffs.
type Column struct {
	Name       string
	Position   int
	DataType   string
	TypeName   string
	Nullable   bool
	Definition string
}

// Index describes a non-primary index.
type Index struct {
	Name    string
	Columns []string
	Unique  bool
	// Simple is true when the index covers plain columns only, without
	// expressions, prefixes, or a partial predicate. Only simple unique
	// indexes qualify as row keys.
	Simple bool
	// ConstraintBacked is true when the index exists to enforce a constraint
	// that is already reported under the table's constraints.
	ConstraintBacked bool
	Definition       string
}

// Constraint types.
const (
	ConstraintPrimaryKey = "primary_key"
	ConstraintForeignKey = "foreign_key"
	ConstraintUnique     = "unique"
	ConstraintCheck      = "check"
	ConstraintExclusion  = "exclusion"
)

type Constraint struct {
	Name       string
	Type       string
	Definition string
}

type Table struct {
	Name        string
	Columns     []*Column
	PrimaryKey  []string
	Indexes     []*Index
	Constraints []*Constraint
	DDL         string

	columnsByName map[string]*Column
}

// Column returns the named column, or nil when the table has no such column.
func (t *Table) Column(name string) *Column {
	return t.columnsByName[name]
}

func (t *Table) indexColumns() {
	t.columnsByName = make(map[string]*Column, len(t.Columns))
	for _, c := range t.Columns {
		t.columnsByName[c.Name] = c
	}
}

type View struct {
	Name       string
	Kind       string
	Definition string
}

type Routine struct {
	Name       string
	Kind       string
	Definition string
}

// Schema is a snapshot of one database schema. Routines are keyed by
// "<kind> <name>" because an engine may allow a function and a procedure to
// share a name.
type Schema struct {
	Tables   map[string]*Table
	Views    map[string]*View
	Routines map[string]*Routine
}

func newSchema() *Schema {
	return &Schema{
		Tables:   map[string]*Table{},
		Views:    map[string]*View{},
		Routines: map[string]*Routine{},
	}
}

// finish builds lookup indexes; it must run before the schema is shared
// between goroutines.
func (s *Schema) finish() *Schema {
	for _, t := range s.Tables {
		t.indexColumns()
	}
	return s
}

func routineKey(kind, name string) string {
	return kind + " " + name
}
