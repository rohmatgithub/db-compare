package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
)

type mysqlDialect struct{}

func (mysqlDialect) DriverName() string { return "mysql" }

func (mysqlDialect) DSN(c ConnInfo) (string, error) {
	cfg := mysql.NewConfig()
	cfg.User = c.Username
	cfg.Passwd = c.Password
	cfg.Net = "tcp"
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	cfg.DBName = c.Database
	// A fixed session time zone makes TIMESTAMP values render identically on
	// both servers regardless of their system time zone.
	cfg.Params = map[string]string{"time_zone": "'+00:00'"}
	switch c.SSLMode {
	case "", SSLDisable:
		cfg.TLSConfig = "false"
	case SSLPrefer:
		cfg.TLSConfig = "preferred"
	case SSLRequire:
		cfg.TLSConfig = "skip-verify"
	case SSLVerifyFull:
		cfg.TLSConfig = "true"
	default:
		return "", fmt.Errorf("unsupported ssl mode %q", c.SSLMode)
	}
	return cfg.FormatDSN(), nil
}

func (mysqlDialect) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v)
	return v, err
}

var mysqlWriteGrant = regexp.MustCompile(`(?i)\b(ALL PRIVILEGES|INSERT|UPDATE|DELETE|DROP|ALTER|CREATE)\b`)

func (mysqlDialect) WritePrivilegeWarning(ctx context.Context, db *sql.DB) (string, error) {
	var found []string
	err := queryEach(ctx, db, "SHOW GRANTS", nil, func(r *sql.Rows) error {
		var grant string
		if err := r.Scan(&grant); err != nil {
			return err
		}
		// Only the privilege list before " ON " matters.
		privileges, _, _ := strings.Cut(strings.ToUpper(grant), " ON ")
		if strings.HasPrefix(privileges, "GRANT PROXY") {
			return nil
		}
		if m := mysqlWriteGrant.FindString(privileges); m != "" {
			found = append(found, m)
		}
		return nil
	})
	if err != nil || len(found) == 0 {
		return "", err
	}
	return "user has write privileges (" + strings.Join(dedupe(found), ", ") + "); a read-only user is recommended", nil
}

func (d mysqlDialect) Inspect(ctx context.Context, db *sql.DB, schema string, tables []string) (*Schema, error) {
	s := newSchema()
	want := tableFilter(tables)
	args := []any{schema}

	err := queryEach(ctx, db, `
		SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE = 'BASE TABLE'`, args, func(r *sql.Rows) error {
		var name string
		if err := r.Scan(&name); err != nil {
			return err
		}
		if want(name) {
			s.Tables[name] = &Table{Name: name}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read tables: %w", err)
	}

	err = queryEach(ctx, db, `
		SELECT TABLE_NAME, COLUMN_NAME, ORDINAL_POSITION, COLUMN_DEFAULT, IS_NULLABLE,
		       DATA_TYPE, COLUMN_TYPE, EXTRA
		FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = ?
		ORDER BY TABLE_NAME, ORDINAL_POSITION`, args, func(r *sql.Rows) error {
		var table, name, nullable, dataType, columnType, extra string
		var position int
		var def sql.NullString
		if err := r.Scan(&table, &name, &position, &def, &nullable, &dataType, &columnType, &extra); err != nil {
			return err
		}
		t := s.Tables[table]
		if t == nil {
			return nil
		}
		c := &Column{
			Name:     name,
			Position: position,
			DataType: mysqlNormalizeColumnType(columnType),
			TypeName: strings.ToLower(dataType),
			Nullable: nullable == "YES",
		}
		c.Definition = mysqlColumnDefinition(c, def, extra)
		t.Columns = append(t.Columns, c)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read columns: %w", err)
	}

	type indexKey struct{ table, name string }
	indexes := map[indexKey]*Index{}
	indexTypes := map[indexKey]string{}
	var indexOrder []indexKey
	err = queryEach(ctx, db, `
		SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME, SUB_PART, INDEX_TYPE
		FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = ?
		ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`, args, func(r *sql.Rows) error {
		var table, name, indexType string
		var nonUnique int
		var column sql.NullString
		var subPart sql.NullInt64
		if err := r.Scan(&table, &name, &nonUnique, &column, &subPart, &indexType); err != nil {
			return err
		}
		if s.Tables[table] == nil {
			return nil
		}
		k := indexKey{table, name}
		idx := indexes[k]
		if idx == nil {
			idx = &Index{Name: name, Unique: nonUnique == 0, Simple: true}
			indexes[k] = idx
			indexTypes[k] = indexType
			indexOrder = append(indexOrder, k)
		}
		part := "(expression)"
		if column.Valid {
			part = column.String
		}
		if !column.Valid || subPart.Valid {
			idx.Simple = false
		}
		if subPart.Valid {
			part = fmt.Sprintf("%s(%d)", part, subPart.Int64)
		}
		idx.Columns = append(idx.Columns, part)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read indexes: %w", err)
	}
	for _, k := range indexOrder {
		t, idx := s.Tables[k.table], indexes[k]
		if idx.Name == "PRIMARY" {
			t.PrimaryKey = idx.Columns
			t.Constraints = append(t.Constraints, &Constraint{
				Name:       "PRIMARY",
				Type:       ConstraintPrimaryKey,
				Definition: "PRIMARY KEY (" + strings.Join(idx.Columns, ", ") + ")",
			})
			continue
		}
		prefix := "INDEX"
		if idx.Unique {
			prefix = "UNIQUE INDEX"
		}
		idx.Definition = fmt.Sprintf("%s USING %s (%s)", prefix, indexTypes[k], strings.Join(idx.Columns, ", "))
		t.Indexes = append(t.Indexes, idx)
	}

	type fkParts struct {
		columns, refColumns       []string
		refTable, onUpdate, onDel string
	}
	fks := map[indexKey]*fkParts{}
	var fkOrder []indexKey
	err = queryEach(ctx, db, `
		SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_NAME,
		       k.REFERENCED_COLUMN_NAME, rc.UPDATE_RULE, rc.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS rc
		  ON rc.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
		 AND rc.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		 AND rc.TABLE_NAME = k.TABLE_NAME
		WHERE k.TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, args, func(r *sql.Rows) error {
		var table, name, column, refTable, refColumn, onUpdate, onDelete string
		if err := r.Scan(&table, &name, &column, &refTable, &refColumn, &onUpdate, &onDelete); err != nil {
			return err
		}
		if s.Tables[table] == nil {
			return nil
		}
		k := indexKey{table, name}
		fk := fks[k]
		if fk == nil {
			fk = &fkParts{refTable: refTable, onUpdate: onUpdate, onDel: onDelete}
			fks[k] = fk
			fkOrder = append(fkOrder, k)
		}
		fk.columns = append(fk.columns, column)
		fk.refColumns = append(fk.refColumns, refColumn)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read foreign keys: %w", err)
	}
	for _, k := range fkOrder {
		fk := fks[k]
		s.Tables[k.table].Constraints = append(s.Tables[k.table].Constraints, &Constraint{
			Name: k.name,
			Type: ConstraintForeignKey,
			Definition: fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s) ON UPDATE %s ON DELETE %s",
				strings.Join(fk.columns, ", "), fk.refTable, strings.Join(fk.refColumns, ", "), fk.onUpdate, fk.onDel),
		})
	}

	for _, t := range s.Tables {
		var name, ddl string
		if err := db.QueryRowContext(ctx, "SHOW CREATE TABLE "+d.TableRef(schema, t.Name)).Scan(&name, &ddl); err != nil {
			return nil, fmt.Errorf("read DDL of %s: %w", t.Name, err)
		}
		t.DDL = mysqlAutoIncrement.ReplaceAllString(ddl, "") + ";"
	}

	if tables != nil {
		return s.finish(), nil
	}

	schemaPrefix := d.QuoteIdent(schema) + "."
	err = queryEach(ctx, db, `
		SELECT TABLE_NAME, VIEW_DEFINITION FROM information_schema.VIEWS
		WHERE TABLE_SCHEMA = ?`, args, func(r *sql.Rows) error {
		var name string
		var def sql.NullString
		if err := r.Scan(&name, &def); err != nil {
			return err
		}
		s.Views[name] = &View{Name: name, Kind: "view", Definition: strings.ReplaceAll(def.String, schemaPrefix, "")}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read views: %w", err)
	}

	err = queryEach(ctx, db, `
		SELECT ROUTINE_NAME, ROUTINE_TYPE, ROUTINE_DEFINITION FROM information_schema.ROUTINES
		WHERE ROUTINE_SCHEMA = ?`, args, func(r *sql.Rows) error {
		var name, kind string
		var def sql.NullString
		if err := r.Scan(&name, &kind, &def); err != nil {
			return err
		}
		kind = strings.ToLower(kind)
		s.Routines[routineKey(kind, name)] = &Routine{Name: name, Kind: kind, Definition: def.String}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read routines: %w", err)
	}

	return s.finish(), nil
}

var (
	mysqlAutoIncrement = regexp.MustCompile(` AUTO_INCREMENT=\d+`)
	// MySQL 8.0.19+ omits integer display widths except for tinyint(1), so
	// widths are dropped to keep 5.7 and 8.0 definitions comparable.
	mysqlIntWidth = regexp.MustCompile(`^(tinyint|smallint|mediumint|int|bigint)\(\d+\)`)
)

func mysqlNormalizeColumnType(t string) string {
	if strings.HasPrefix(t, "tinyint(1)") {
		return t
	}
	return mysqlIntWidth.ReplaceAllString(t, "$1")
}

func mysqlColumnDefinition(c *Column, def sql.NullString, extra string) string {
	parts := []string{c.DataType}
	if c.Nullable {
		parts = append(parts, "NULL")
	} else {
		parts = append(parts, "NOT NULL")
	}
	if def.Valid {
		parts = append(parts, "DEFAULT "+def.String)
	}
	// MySQL 8 marks expression defaults with DEFAULT_GENERATED; 5.7 does not.
	extra = strings.TrimSpace(strings.ReplaceAll(extra, "DEFAULT_GENERATED", ""))
	if extra != "" {
		parts = append(parts, strings.ToUpper(extra))
	}
	return strings.Join(parts, " ")
}

func (mysqlDialect) QuoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (d mysqlDialect) TableRef(schema, table string) string {
	return d.QuoteIdent(schema) + "." + d.QuoteIdent(table)
}

func (d mysqlDialect) TextExpr(c *Column) string {
	return d.QuoteIdent(c.Name)
}

func (d mysqlDialect) ChecksumExpr(cols []*Column) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		// QUOTE renders NULL as the bare word NULL and every other value as a
		// quoted literal, so NULL and the string 'NULL' hash differently.
		parts[i] = "QUOTE(" + d.QuoteIdent(c.Name) + ")"
	}
	return "CAST(COALESCE(SUM(CAST(CONV(SUBSTRING(MD5(CONCAT_WS(',', " +
		strings.Join(parts, ", ") + ")), 1, 16), 16, 10) AS UNSIGNED)), 0) AS CHAR)"
}

func (mysqlDialect) KeyKind(c *Column) KeyKind {
	switch c.TypeName {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "year":
		return KeyInt
	case "decimal", "numeric", "float", "double", "real":
		return KeyDecimal
	default:
		return KeyBytes
	}
}

func (d mysqlDialect) SortExpr(c *Column) string {
	if d.KeyKind(c) == KeyBytes {
		return "CAST(" + d.QuoteIdent(c.Name) + " AS BINARY)"
	}
	return d.QuoteIdent(c.Name)
}

func (d mysqlDialect) ParamExpr(c *Column, _ int) string {
	switch d.KeyKind(c) {
	case KeyInt:
		return "?"
	case KeyDecimal:
		return "CAST(? AS DECIMAL(65,30))"
	default:
		return "CAST(? AS BINARY)"
	}
}

func (d mysqlDialect) KeyArg(c *Column, value string) (any, error) {
	if d.KeyKind(c) == KeyInt {
		return intKeyArg(value)
	}
	return value, nil
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := values[:0]
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
