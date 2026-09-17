package engine

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// postgresDialect supports PostgreSQL 12 and later (pg_attribute.attgenerated
// and pg_proc.prokind are required).
type postgresDialect struct{}

func (postgresDialect) DriverName() string { return "pgx" }

func (d postgresDialect) DSN(c ConnInfo) (string, error) {
	switch c.SSLMode {
	case "":
		c.SSLMode = SSLDisable
	case SSLDisable, SSLPrefer, SSLRequire, SSLVerifyFull:
	default:
		return "", fmt.Errorf("unsupported ssl mode %q", c.SSLMode)
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.Username, c.Password),
		Host:   net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:   "/" + c.Database,
	}
	q := url.Values{}
	q.Set("sslmode", c.SSLMode)
	q.Set("application_name", "db-compare")
	// Unrecognized parameters become session settings. They pin the text
	// rendering of values so both servers produce identical output, keep the
	// session read-only, and put the compared schema on the search path so
	// catalog functions print unqualified object names.
	q.Set("timezone", "UTC")
	q.Set("datestyle", "ISO, YMD")
	q.Set("intervalstyle", "postgres")
	q.Set("bytea_output", "hex")
	q.Set("extra_float_digits", "1")
	q.Set("default_transaction_read_only", "on")
	q.Set("search_path", d.QuoteIdent(c.SchemaName()))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (postgresDialect) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, "SHOW server_version").Scan(&v)
	return v, err
}

func (postgresDialect) WritePrivilegeWarning(ctx context.Context, db *sql.DB) (string, error) {
	var superuser, canWrite bool
	err := db.QueryRowContext(ctx, `
		SELECT r.rolsuper,
		       EXISTS (
		         SELECT 1 FROM information_schema.table_privileges p
		         WHERE p.grantee = current_user
		           AND p.privilege_type IN ('INSERT', 'UPDATE', 'DELETE', 'TRUNCATE'))
		FROM pg_roles r WHERE r.rolname = current_user`).Scan(&superuser, &canWrite)
	switch {
	case err != nil:
		return "", err
	case superuser:
		return "user is a superuser; a read-only user is recommended", nil
	case canWrite:
		return "user has write privileges on some tables; a read-only user is recommended", nil
	default:
		return "", nil
	}
}

func (d postgresDialect) Inspect(ctx context.Context, db *sql.DB, schema string, tables []string) (*Schema, error) {
	s := newSchema()
	want := tableFilter(tables)
	args := []any{schema}

	err := queryEach(ctx, db, `
		SELECT c.relname
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition`, args, func(r *sql.Rows) error {
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
		SELECT c.relname, a.attname, a.attnum, format_type(a.atttypid, a.atttypmod), t.typname,
		       a.attnotnull, pg_get_expr(ad.adbin, ad.adrelid), a.attidentity, a.attgenerated
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_type t ON t.oid = a.atttypid
		LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
		WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND NOT c.relispartition
		  AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY c.relname, a.attnum`, args, func(r *sql.Rows) error {
		var table, name, dataType, typeName, identity, generated string
		var position int
		var notNull bool
		var def sql.NullString
		if err := r.Scan(&table, &name, &position, &dataType, &typeName, &notNull, &def, &identity, &generated); err != nil {
			return err
		}
		t := s.Tables[table]
		if t == nil {
			return nil
		}
		c := &Column{Name: name, Position: position, DataType: dataType, TypeName: typeName, Nullable: !notNull}
		c.Definition = postgresColumnDefinition(c, def, identity, generated)
		t.Columns = append(t.Columns, c)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read columns: %w", err)
	}

	constraintNames := map[string]map[string]bool{}
	err = queryEach(ctx, db, `
		SELECT t.relname, con.conname, con.contype, pg_get_constraintdef(con.oid, true)
		FROM pg_constraint con
		JOIN pg_class t ON t.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = $1 AND con.contype IN ('p', 'f', 'u', 'c', 'x')
		ORDER BY t.relname, con.conname`, args, func(r *sql.Rows) error {
		var table, name, kind, def string
		if err := r.Scan(&table, &name, &kind, &def); err != nil {
			return err
		}
		t := s.Tables[table]
		if t == nil {
			return nil
		}
		if constraintNames[table] == nil {
			constraintNames[table] = map[string]bool{}
		}
		constraintNames[table][name] = true
		t.Constraints = append(t.Constraints, &Constraint{
			Name:       name,
			Type:       postgresConstraintType(kind),
			Definition: stripSchemaPrefix(def, d.QuoteIdent(schema), schema),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read constraints: %w", err)
	}

	err = queryEach(ctx, db, `
		SELECT t.relname, i.relname, ix.indisunique, ix.indisprimary,
		       ix.indexprs IS NULL AND ix.indpred IS NULL,
		       pg_get_indexdef(ix.indexrelid),
		       COALESCE((
		         SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		         FROM unnest(ix.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
		         JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		         WHERE k.ord <= ix.indnkeyatts
		       ), '')
		FROM pg_index ix
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_class t ON t.oid = ix.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = $1 AND t.relkind IN ('r', 'p')
		ORDER BY t.relname, i.relname`, args, func(r *sql.Rows) error {
		var table, name, def, columns string
		var unique, primary, simple bool
		if err := r.Scan(&table, &name, &unique, &primary, &simple, &def, &columns); err != nil {
			return err
		}
		t := s.Tables[table]
		if t == nil {
			return nil
		}
		cols := strings.Split(columns, ",")
		if primary {
			t.PrimaryKey = cols
			return nil
		}
		t.Indexes = append(t.Indexes, &Index{
			Name:             name,
			Columns:          cols,
			Unique:           unique,
			Simple:           simple && columns != "",
			ConstraintBacked: constraintNames[table][name],
			Definition:       stripSchemaPrefix(def, d.QuoteIdent(schema), schema),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read indexes: %w", err)
	}

	for _, t := range s.Tables {
		t.DDL = d.tableDDL(t)
	}

	if tables != nil {
		return s.finish(), nil
	}

	err = queryEach(ctx, db, `
		SELECT c.relname, c.relkind, pg_get_viewdef(c.oid, true)
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind IN ('v', 'm')`, args, func(r *sql.Rows) error {
		var name, kind string
		var def sql.NullString
		if err := r.Scan(&name, &kind, &def); err != nil {
			return err
		}
		k := "view"
		if kind == "m" {
			k = "materialized_view"
		}
		s.Views[name] = &View{Name: name, Kind: k, Definition: def.String}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read views: %w", err)
	}

	err = queryEach(ctx, db, `
		SELECT p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')',
		       CASE p.prokind WHEN 'p' THEN 'procedure' ELSE 'function' END,
		       pg_get_functiondef(p.oid)
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = $1 AND p.prokind IN ('f', 'p')
		  AND NOT EXISTS (
		    SELECT 1 FROM pg_depend dep
		    WHERE dep.classid = 'pg_proc'::regclass AND dep.objid = p.oid AND dep.deptype = 'e')`,
		args, func(r *sql.Rows) error {
			var name, kind, def string
			if err := r.Scan(&name, &kind, &def); err != nil {
				return err
			}
			s.Routines[routineKey(kind, name)] = &Routine{
				Name:       name,
				Kind:       kind,
				Definition: stripSchemaPrefix(def, d.QuoteIdent(schema), schema),
			}
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read routines: %w", err)
	}

	return s.finish(), nil
}

func postgresColumnDefinition(c *Column, def sql.NullString, identity, generated string) string {
	parts := []string{c.DataType}
	if !c.Nullable {
		parts = append(parts, "NOT NULL")
	}
	switch {
	case generated == "s":
		parts = append(parts, "GENERATED ALWAYS AS ("+def.String+") STORED")
	case identity == "a":
		parts = append(parts, "GENERATED ALWAYS AS IDENTITY")
	case identity == "d":
		parts = append(parts, "GENERATED BY DEFAULT AS IDENTITY")
	case def.Valid:
		parts = append(parts, "DEFAULT "+def.String)
	}
	return strings.Join(parts, " ")
}

func postgresConstraintType(kind string) string {
	switch kind {
	case "p":
		return ConstraintPrimaryKey
	case "f":
		return ConstraintForeignKey
	case "u":
		return ConstraintUnique
	case "c":
		return ConstraintCheck
	default:
		return ConstraintExclusion
	}
}

// stripSchemaPrefix removes the compared schema's qualifier so that
// definitions from differently named schemas remain comparable.
func stripSchemaPrefix(def, quoted, plain string) string {
	def = strings.ReplaceAll(def, quoted+".", "")
	re := regexp.MustCompile(`(^|[^A-Za-z0-9_$"])` + regexp.QuoteMeta(plain) + `\.`)
	return re.ReplaceAllString(def, "${1}")
}

func (d postgresDialect) tableDDL(t *Table) string {
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "    "+d.QuoteIdent(c.Name)+" "+c.Definition)
	}
	constraints := append([]*Constraint(nil), t.Constraints...)
	sort.SliceStable(constraints, func(i, j int) bool {
		return constraints[i].Type == ConstraintPrimaryKey && constraints[j].Type != ConstraintPrimaryKey
	})
	for _, c := range constraints {
		lines = append(lines, "    CONSTRAINT "+d.QuoteIdent(c.Name)+" "+c.Definition)
	}
	var b strings.Builder
	b.WriteString("CREATE TABLE " + d.QuoteIdent(t.Name) + " (\n")
	b.WriteString(strings.Join(lines, ",\n"))
	b.WriteString("\n);")
	for _, idx := range t.Indexes {
		if !idx.ConstraintBacked {
			b.WriteString("\n" + idx.Definition + ";")
		}
	}
	return b.String()
}

func (postgresDialect) QuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (d postgresDialect) TableRef(schema, table string) string {
	return d.QuoteIdent(schema) + "." + d.QuoteIdent(table)
}

func (d postgresDialect) TextExpr(c *Column) string {
	return d.QuoteIdent(c.Name) + "::text"
}

func (d postgresDialect) ChecksumExpr(cols []*Column) string {
	// Concatenation with || avoids the 100-argument limit of concat_ws.
	// quote_nullable renders NULL as the bare word NULL and every other value
	// as a quoted literal, so the parts cannot be confused with each other.
	parts := make([]string, len(cols))
	for i, c := range cols {
		parts[i] = "quote_nullable(" + d.QuoteIdent(c.Name) + "::text)"
	}
	return "COALESCE(SUM(('x' || substr(md5(" + strings.Join(parts, " || ',' || ") +
		"), 1, 16))::bit(64)::bigint), 0)::text"
}

func (postgresDialect) KeyKind(c *Column) KeyKind {
	switch c.TypeName {
	case "int2", "int4", "int8", "oid":
		return KeyInt
	case "numeric", "float4", "float8":
		return KeyDecimal
	default:
		return KeyBytes
	}
}

func (d postgresDialect) SortExpr(c *Column) string {
	if d.KeyKind(c) == KeyBytes {
		// The "C" collation orders by byte value, matching KeyBytes.
		return "(" + d.QuoteIdent(c.Name) + `::text COLLATE "C")`
	}
	return d.QuoteIdent(c.Name)
}

func (d postgresDialect) ParamExpr(c *Column, n int) string {
	// Binding every key as text and casting on the server keeps parameter
	// types independent of how the driver would encode Go values.
	p := "CAST($" + strconv.Itoa(n) + " AS text)"
	if d.KeyKind(c) == KeyBytes {
		return "(" + p + ` COLLATE "C")`
	}
	return "CAST(" + p + " AS " + c.DataType + ")"
}

func (postgresDialect) KeyArg(_ *Column, value string) (any, error) {
	return value, nil
}
