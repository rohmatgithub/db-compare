package engine

import (
	"sort"
	"strings"
)

// Object and change statuses.
const (
	StatusSame       = "same"
	StatusChanged    = "changed"
	StatusOnlySource = "only_source"
	StatusOnlyTarget = "only_target"
)

type SchemaChange struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Source string `json:"source,omitempty"`
	Target string `json:"target,omitempty"`
}

type SchemaDiffItem struct {
	ObjectType string
	Name       string
	Status     string
	Changes    []SchemaChange
	SourceDDL  string
	TargetDDL  string
}

// DiffSchemas compares tables, views, and routines. Items are ordered by
// object type and then by name.
func DiffSchemas(src, tgt *Schema, opts Options) []SchemaDiffItem {
	var items []SchemaDiffItem

	for _, name := range unionKeys(src.Tables, tgt.Tables) {
		if !opts.TableIncluded(name) {
			continue
		}
		s, t := src.Tables[name], tgt.Tables[name]
		item := SchemaDiffItem{ObjectType: "table", Name: name}
		if s != nil {
			item.SourceDDL = s.DDL
		}
		if t != nil {
			item.TargetDDL = t.DDL
		}
		item.Status, item.Changes = compareTables(s, t, opts)
		items = append(items, item)
	}

	if !opts.SkipViews {
		for _, name := range unionKeys(src.Views, tgt.Views) {
			s, t := src.Views[name], tgt.Views[name]
			item := SchemaDiffItem{Name: name}
			var sDef, tDef, sKind, tKind string
			if s != nil {
				sDef, sKind, item.SourceDDL, item.ObjectType = s.Definition, s.Kind, s.Definition, s.Kind
			}
			if t != nil {
				tDef, tKind, item.TargetDDL, item.ObjectType = t.Definition, t.Kind, t.Definition, t.Kind
			}
			item.Status, item.Changes = compareDefinitions(s != nil, t != nil, sKind+" "+sDef, tKind+" "+tDef)
			items = append(items, item)
		}
	}

	if !opts.SkipRoutines {
		for _, key := range unionKeys(src.Routines, tgt.Routines) {
			s, t := src.Routines[key], tgt.Routines[key]
			var item SchemaDiffItem
			var sDef, tDef string
			if s != nil {
				item.ObjectType, item.Name, item.SourceDDL, sDef = s.Kind, s.Name, s.Definition, s.Definition
			}
			if t != nil {
				item.ObjectType, item.Name, item.TargetDDL, tDef = t.Kind, t.Name, t.Definition, t.Definition
			}
			item.Status, item.Changes = compareDefinitions(s != nil, t != nil, sDef, tDef)
			items = append(items, item)
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].ObjectType != items[j].ObjectType {
			return objectTypeRank(items[i].ObjectType) < objectTypeRank(items[j].ObjectType)
		}
		return items[i].Name < items[j].Name
	})
	return items
}

func objectTypeRank(t string) int {
	switch t {
	case "table":
		return 0
	case "view", "materialized_view":
		return 1
	default:
		return 2
	}
}

func presence(inSource, inTarget bool) string {
	switch {
	case inSource && !inTarget:
		return StatusOnlySource
	case !inSource && inTarget:
		return StatusOnlyTarget
	default:
		return ""
	}
}

func compareDefinitions(inSource, inTarget bool, sDef, tDef string) (string, []SchemaChange) {
	if st := presence(inSource, inTarget); st != "" {
		return st, nil
	}
	if normalizeSQL(sDef) == normalizeSQL(tDef) {
		return StatusSame, nil
	}
	return StatusChanged, []SchemaChange{{Kind: "definition", Name: "definition", Status: StatusChanged}}
}

func compareTables(s, t *Table, opts Options) (string, []SchemaChange) {
	if st := presence(s != nil, t != nil); st != "" {
		return st, nil
	}
	var changes []SchemaChange

	sCols, tCols := columnDefs(s), columnDefs(t)
	changes = append(changes, diffDefinitions("column", sCols, tCols)...)

	if opts.CheckColumnOrder {
		sOrder, tOrder := commonColumnOrder(s, t), commonColumnOrder(t, s)
		if !equalStrings(sOrder, tOrder) {
			changes = append(changes, SchemaChange{
				Kind:   "column_order",
				Name:   "column order",
				Status: StatusChanged,
				Source: strings.Join(sOrder, ", "),
				Target: strings.Join(tOrder, ", "),
			})
		}
	}

	if !equalStrings(s.PrimaryKey, t.PrimaryKey) {
		changes = append(changes, SchemaChange{
			Kind:   "primary_key",
			Name:   "primary key",
			Status: StatusChanged,
			Source: keyText(s.PrimaryKey),
			Target: keyText(t.PrimaryKey),
		})
	}

	changes = append(changes, diffDefinitions("index", indexDefs(s), indexDefs(t))...)
	changes = append(changes, diffDefinitions("constraint", constraintDefs(s), constraintDefs(t))...)

	if len(changes) == 0 {
		return StatusSame, nil
	}
	return StatusChanged, changes
}

func diffDefinitions(kind string, a, b map[string]string) []SchemaChange {
	var out []SchemaChange
	for _, name := range unionKeys(a, b) {
		x, inA := a[name]
		y, inB := b[name]
		switch {
		case !inB:
			out = append(out, SchemaChange{Kind: kind, Name: name, Status: StatusOnlySource, Source: x})
		case !inA:
			out = append(out, SchemaChange{Kind: kind, Name: name, Status: StatusOnlyTarget, Target: y})
		case normalizeSQL(x) != normalizeSQL(y):
			out = append(out, SchemaChange{Kind: kind, Name: name, Status: StatusChanged, Source: x, Target: y})
		}
	}
	return out
}

func columnDefs(t *Table) map[string]string {
	m := make(map[string]string, len(t.Columns))
	for _, c := range t.Columns {
		m[c.Name] = c.Definition
	}
	return m
}

func indexDefs(t *Table) map[string]string {
	m := map[string]string{}
	for _, idx := range t.Indexes {
		if !idx.ConstraintBacked {
			m[idx.Name] = idx.Definition
		}
	}
	return m
}

// constraintDefs excludes the primary key, which is compared by columns so
// that differing constraint names alone do not count as a change.
func constraintDefs(t *Table) map[string]string {
	m := map[string]string{}
	for _, c := range t.Constraints {
		if c.Type != ConstraintPrimaryKey {
			m[c.Name] = c.Definition
		}
	}
	return m
}

func commonColumnOrder(a, b *Table) []string {
	var out []string
	for _, c := range a.Columns {
		if b.Column(c.Name) != nil {
			out = append(out, c.Name)
		}
	}
	return out
}

func keyText(cols []string) string {
	if len(cols) == 0 {
		return "(none)"
	}
	return strings.Join(cols, ", ")
}

func unionKeys[V any](a, b map[string]V) []string {
	keys := make([]string, 0, len(a)+len(b))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
