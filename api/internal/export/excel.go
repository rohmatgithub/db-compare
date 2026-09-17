// Package export writes stored compare results as Excel workbooks. Rows are
// written with excelize's stream writer so large results do not have to fit
// in memory.
package export

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"dbcompare/internal/engine"
	"dbcompare/internal/store"
)

var categoryLabels = map[string]string{
	engine.CategoryDifferent:  "Different",
	engine.CategoryOnlySource: "Only in source",
	engine.CategoryOnlyTarget: "Only in target",
}

type styles struct {
	title, header, different, onlySource, onlyTarget int
}

type workbook struct {
	f        *excelize.File
	styles   styles
	reserved map[string]bool
	created  int
}

func newWorkbook() (*workbook, error) {
	f := excelize.NewFile()
	w := &workbook{f: f, reserved: map[string]bool{}}
	fill := func(color string) *excelize.Style {
		return &excelize.Style{Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{color}}}
	}
	defs := []struct {
		into  *int
		style *excelize.Style
	}{
		{&w.styles.title, &excelize.Style{Font: &excelize.Font{Bold: true, Size: 14}}},
		{&w.styles.header, &excelize.Style{
			Font: &excelize.Font{Bold: true},
			Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"#D9E1F2"}},
		}},
		{&w.styles.different, fill("#FFF2CC")},
		{&w.styles.onlySource, fill("#E2EFDA")},
		{&w.styles.onlyTarget, fill("#FCE4D6")},
	}
	for _, d := range defs {
		id, err := f.NewStyle(d.style)
		if err != nil {
			f.Close()
			return nil, err
		}
		*d.into = id
	}
	return w, nil
}

func (w *workbook) categoryStyle(category string) int {
	switch category {
	case engine.CategoryOnlySource:
		return w.styles.onlySource
	case engine.CategoryOnlyTarget:
		return w.styles.onlyTarget
	default:
		return w.styles.different
	}
}

// reserve returns a unique, valid sheet name derived from name.
func (w *workbook) reserve(name string) string {
	base := sanitizeSheetName(name)
	candidate := base
	for i := 2; w.reserved[strings.ToLower(candidate)]; i++ {
		suffix := fmt.Sprintf(" (%d)", i)
		candidate = truncateRunes(base, excelize.MaxSheetNameLength-utf8.RuneCountInString(suffix)) + suffix
	}
	w.reserved[strings.ToLower(candidate)] = true
	return candidate
}

func sanitizeSheetName(name string) string {
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:\/?*[]`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, "'")
	if name == "" {
		name = "Sheet"
	}
	return truncateRunes(name, excelize.MaxSheetNameLength)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

type sheet struct {
	w    *workbook
	sw   *excelize.StreamWriter
	name string
	row  int
}

// open creates a sheet with a name obtained from reserve.
func (w *workbook) open(name string, widths ...float64) (*sheet, error) {
	if w.created == 0 {
		if err := w.f.SetSheetName("Sheet1", name); err != nil {
			return nil, err
		}
	} else if _, err := w.f.NewSheet(name); err != nil {
		return nil, err
	}
	w.created++
	sw, err := w.f.NewStreamWriter(name)
	if err != nil {
		return nil, err
	}
	for i, width := range widths {
		if err := sw.SetColWidth(i+1, i+1, width); err != nil {
			return nil, err
		}
	}
	return &sheet{w: w, sw: sw, name: name, row: 1}, nil
}

func (s *sheet) add(values ...any) error {
	cell, err := excelize.CoordinatesToCellName(1, s.row)
	if err != nil {
		return err
	}
	for i, v := range values {
		values[i] = cellValue(v)
	}
	s.row++
	return s.sw.SetRow(cell, values)
}

func (s *sheet) styled(style int, values ...any) error {
	cells := make([]any, len(values))
	for i, v := range values {
		cells[i] = excelize.Cell{StyleID: style, Value: v}
	}
	return s.add(cells...)
}

func (s *sheet) full() bool {
	return s.row > excelize.TotalRows
}

func (s *sheet) close() error {
	return s.sw.Flush()
}

// cellValue keeps text within Excel's per-cell limit.
func cellValue(v any) any {
	switch x := v.(type) {
	case string:
		return clip(x)
	case *string:
		if x == nil {
			return nil
		}
		return clip(*x)
	case excelize.Cell:
		x.Value = cellValue(x.Value)
		return x
	}
	return v
}

func clip(s string) string {
	const marker = "…[truncated]"
	if utf8.RuneCountInString(s) <= excelize.TotalCellChars {
		return s
	}
	return truncateRunes(s, excelize.TotalCellChars-utf8.RuneCountInString(marker)) + marker
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func optionalInt(n *int64) any {
	if n == nil {
		return ""
	}
	return *n
}

// WriteRun writes the whole run: summary, schema results, per-table data
// results, and one sheet per table with stored row differences.
func WriteRun(ctx context.Context, out io.Writer, st *store.Store, runID int64) error {
	run, err := st.GetRun(ctx, runID)
	if err != nil {
		return err
	}
	projectName := ""
	if p, err := st.GetProject(ctx, run.ProjectID); err == nil {
		projectName = p.Name
	}
	summary, err := st.RunSummary(ctx, runID)
	if err != nil {
		return err
	}
	items, err := st.ListSchemaItems(ctx, runID)
	if err != nil {
		return err
	}
	tables, err := st.ListTableResults(ctx, runID)
	if err != nil {
		return err
	}

	w, err := newWorkbook()
	if err != nil {
		return err
	}
	defer w.f.Close()

	summaryName, schemaName, dataName := w.reserve("Summary"), w.reserve("Schema"), w.reserve("Data")
	detailNames := map[string]string{}
	for _, t := range tables {
		if hasStoredRows(t) {
			detailNames[t.TableName] = w.reserve(t.TableName)
		}
	}

	if err := writeSummarySheet(w, summaryName, projectName, run, summary, tables, detailNames); err != nil {
		return err
	}
	if err := writeSchemaSheet(w, schemaName, items); err != nil {
		return err
	}
	if err := writeDataSheet(w, dataName, tables, detailNames); err != nil {
		return err
	}
	for _, t := range tables {
		if name, ok := detailNames[t.TableName]; ok {
			if err := writeDetailSheets(ctx, w, st, name, t); err != nil {
				return fmt.Errorf("table %s: %w", t.TableName, err)
			}
		}
	}
	return w.f.Write(out)
}

// WriteTable writes the row differences of one table.
func WriteTable(ctx context.Context, out io.Writer, st *store.Store, runID int64, table string) error {
	t, err := st.GetTableResult(ctx, runID, table)
	if err != nil {
		return err
	}
	w, err := newWorkbook()
	if err != nil {
		return err
	}
	defer w.f.Close()
	if err := writeDetailSheets(ctx, w, st, w.reserve(table), t); err != nil {
		return err
	}
	return w.f.Write(out)
}

func hasStoredRows(t store.TableResult) bool {
	switch t.RowDiffStatus {
	case store.RowDiffNotRun, store.RowDiffNotNeeded, store.RowDiffNeedsKey:
		return false
	}
	return t.Different+t.OnlySource+t.OnlyTarget > 0
}

func writeSummarySheet(w *workbook, name, projectName string, run store.Run, sum store.RunSummary,
	tables []store.TableResult, detailNames map[string]string) error {
	s, err := w.open(name, 28, 60)
	if err != nil {
		return err
	}
	rows := [][]any{
		{"Run ID", run.ID},
		{"Project", projectName},
		{"Source", run.SourceLabel},
		{"Target", run.TargetLabel},
		{"Status", run.Status},
		{"Error", run.Error},
		{"Started by", run.CreatedBy},
		{"Started at", formatTime(run.StartedAt)},
		{"Finished at", formatTime(run.FinishedAt)},
		{"Exported at", formatTime(ptr(time.Now()))},
	}
	if err := s.styled(w.styles.title, "Database compare report"); err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.add(r...); err != nil {
			return err
		}
	}

	section := func(title string, counts [][2]any) error {
		if err := s.add(); err != nil {
			return err
		}
		if err := s.styled(w.styles.header, title, "Count"); err != nil {
			return err
		}
		for _, c := range counts {
			if err := s.add(c[0], c[1]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := section("Schema objects", [][2]any{
		{"Same", sum.Schema[engine.StatusSame]},
		{"Changed", sum.Schema[engine.StatusChanged]},
		{"Only in source", sum.Schema[engine.StatusOnlySource]},
		{"Only in target", sum.Schema[engine.StatusOnlyTarget]},
	}); err != nil {
		return err
	}
	if err := section("Table data", [][2]any{
		{"Identical", sum.Data[store.DataIdentical]},
		{"Different", sum.Data[store.DataDifferent]},
		{"Error", sum.Data[store.DataError]},
		{"Needs key for row detail", sum.RowDiff[store.RowDiffNeedsKey]},
	}); err != nil {
		return err
	}
	if err := section("Rows", [][2]any{
		{"Different", sum.Rows.Different},
		{"Only in source", sum.Rows.OnlySource},
		{"Only in target", sum.Rows.OnlyTarget},
	}); err != nil {
		return err
	}

	opts, _ := json.MarshalIndent(run.Options, "", "  ")
	if err := s.add(); err != nil {
		return err
	}
	if err := s.add("Options", string(opts)); err != nil {
		return err
	}

	var renamed [][2]any
	for _, t := range tables {
		if n, ok := detailNames[t.TableName]; ok && n != t.TableName {
			renamed = append(renamed, [2]any{n, t.TableName})
		}
	}
	if len(renamed) > 0 {
		if err := s.add(); err != nil {
			return err
		}
		if err := s.styled(w.styles.header, "Sheet", "Table"); err != nil {
			return err
		}
		for _, r := range renamed {
			if err := s.add(r[0], r[1]); err != nil {
				return err
			}
		}
	}
	return s.close()
}

func writeSchemaSheet(w *workbook, name string, items []store.SchemaItem) error {
	s, err := w.open(name, 14, 40, 14, 100)
	if err != nil {
		return err
	}
	if err := s.styled(w.styles.header, "Type", "Name", "Status", "Changes"); err != nil {
		return err
	}
	for _, it := range items {
		var lines []string
		for _, c := range it.Changes {
			line := fmt.Sprintf("%s %s: %s", c.Kind, c.Name, c.Status)
			switch {
			case c.Source != "" && c.Target != "":
				line += fmt.Sprintf(" (%s → %s)", c.Source, c.Target)
			case c.Source != "":
				line += " (" + c.Source + ")"
			case c.Target != "":
				line += " (" + c.Target + ")"
			}
			lines = append(lines, line)
		}
		status := any(it.Status)
		if it.Status != engine.StatusSame {
			status = excelize.Cell{StyleID: w.styles.different, Value: it.Status}
		}
		if err := s.add(it.ObjectType, it.Name, status, strings.Join(lines, "\n")); err != nil {
			return err
		}
	}
	return s.close()
}

func writeDataSheet(w *workbook, name string, tables []store.TableResult, detailNames map[string]string) error {
	s, err := w.open(name, 32, 20, 14, 14, 12, 14, 12, 14, 14, 10, 40, 24)
	if err != nil {
		return err
	}
	err = s.styled(w.styles.header, "Table", "Key columns", "Source rows", "Target rows", "Data status",
		"Row detail", "Different", "Only in source", "Only in target", "Truncated", "Notes / error", "Detail sheet")
	if err != nil {
		return err
	}
	for _, t := range tables {
		notes := strings.Join(t.Notes, "; ")
		if t.Error != "" {
			notes = strings.TrimPrefix(notes+"; "+t.Error, "; ")
		}
		status := any(t.DataStatus)
		if t.DataStatus != store.DataIdentical {
			status = excelize.Cell{StyleID: w.styles.different, Value: t.DataStatus}
		}
		truncated := ""
		if t.Truncated {
			truncated = "yes"
		}
		err := s.add(t.TableName, strings.Join(t.KeyColumns, ", "), optionalInt(t.SourceRows), optionalInt(t.TargetRows),
			status, t.RowDiffStatus, t.Different, t.OnlySource, t.OnlyTarget, truncated, notes, detailNames[t.TableName])
		if err != nil {
			return err
		}
	}
	return s.close()
}

// writeDetailSheets lists stored row differences. Key columns come first,
// followed by a source/target pair for every other compared column. When a
// sheet reaches Excel's row limit the rows continue on a new sheet.
func writeDetailSheets(ctx context.Context, w *workbook, st *store.Store, name string, t store.TableResult) error {
	keyCount := len(t.KeyColumns)
	header := []any{"Category"}
	widths := []float64{16}
	for _, k := range t.KeyColumns {
		header = append(header, k+" (key)")
		widths = append(widths, 18)
	}
	for _, c := range t.Columns[keyCount:] {
		header = append(header, c+" [source]", c+" [target]")
		widths = append(widths, 20, 20)
	}

	part := 1
	openPart := func(sheetName string) (*sheet, error) {
		s, err := w.open(sheetName, widths...)
		if err != nil {
			return nil, err
		}
		return s, s.styled(w.styles.header, header...)
	}
	s, err := openPart(name)
	if err != nil {
		return err
	}

	err = st.ForEachRowDiff(ctx, t.RunID, t.TableName, func(d store.RowDiff) error {
		if s.full() {
			if err := s.close(); err != nil {
				return err
			}
			part++
			if s, err = openPart(w.reserve(name + " " + strconv.Itoa(part))); err != nil {
				return err
			}
		}
		changed := map[string]bool{}
		for _, c := range d.DiffColumns {
			changed[c] = true
		}
		row := []any{excelize.Cell{StyleID: w.categoryStyle(d.Category), Value: categoryLabels[d.Category]}}
		for _, k := range d.Key {
			row = append(row, k)
		}
		for i, c := range t.Columns[keyCount:] {
			idx := keyCount + i
			src, tgt := valueAt(d.Source, idx), valueAt(d.Target, idx)
			if changed[c] {
				row = append(row,
					excelize.Cell{StyleID: w.styles.different, Value: src},
					excelize.Cell{StyleID: w.styles.different, Value: tgt})
			} else {
				row = append(row, src, tgt)
			}
		}
		return s.add(row...)
	})
	if err != nil {
		return err
	}
	return s.close()
}

// valueAt leaves the cell empty when the side has no such row and writes
// "(NULL)" for a NULL value, so the two cases stay distinguishable.
func valueAt(values []*string, i int) any {
	if values == nil || i >= len(values) {
		return nil
	}
	if values[i] == nil {
		return "(NULL)"
	}
	return *values[i]
}

func ptr[T any](v T) *T { return &v }
