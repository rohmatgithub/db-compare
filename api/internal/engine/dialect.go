package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	EngineMySQL    = "mysql"
	EnginePostgres = "postgres"
)

// SSL modes accepted for both engines.
const (
	SSLDisable    = "disable"
	SSLPrefer     = "prefer"
	SSLRequire    = "require"
	SSLVerifyFull = "verify-full"
)

// ConnInfo holds everything needed to open a connection to a compared
// database. For MySQL, Schema is ignored and Database is the schema.
type ConnInfo struct {
	Engine   string
	Host     string
	Port     int
	Database string
	Schema   string
	Username string
	Password string
	SSLMode  string
}

// SchemaName returns the schema whose objects are compared.
func (c ConnInfo) SchemaName() string {
	if c.Engine == EngineMySQL {
		return c.Database
	}
	if c.Schema == "" {
		return "public"
	}
	return c.Schema
}

// Dialect isolates everything that differs between engines: connection
// strings, catalog queries, and the SQL fragments used to hash rows and to
// order and bound key ranges.
type Dialect interface {
	DriverName() string
	DSN(c ConnInfo) (string, error)
	ServerVersion(ctx context.Context, db *sql.DB) (string, error)
	// WritePrivilegeWarning returns a non-empty message when the connected
	// user can modify data.
	WritePrivilegeWarning(ctx context.Context, db *sql.DB) (string, error)
	// Inspect reads the schema. When tables is non-nil, only those tables are
	// read and views and routines are skipped.
	Inspect(ctx context.Context, db *sql.DB, schema string, tables []string) (*Schema, error)

	QuoteIdent(name string) string
	TableRef(schema, table string) string
	// TextExpr selects a column so that the driver returns its text form.
	TextExpr(c *Column) string
	// ChecksumExpr is an order-independent aggregate over the given columns
	// that yields text. Two row sets with equal row counts and equal
	// checksums are treated as identical.
	ChecksumExpr(cols []*Column) string
	KeyKind(c *Column) KeyKind
	// SortExpr is the ORDER BY and range expression for a key column. Its
	// ordering must match compareKeyPart for the column's KeyKind.
	SortExpr(c *Column) string
	// ParamExpr is the placeholder for a key value compared against SortExpr.
	ParamExpr(c *Column, n int) string
	KeyArg(c *Column, value string) (any, error)
}

func DialectFor(engine string) (Dialect, error) {
	switch engine {
	case EngineMySQL:
		return mysqlDialect{}, nil
	case EnginePostgres:
		return postgresDialect{}, nil
	default:
		return nil, fmt.Errorf("unsupported engine %q", engine)
	}
}

// Open opens a connection pool without verifying connectivity.
func Open(c ConnInfo) (*sql.DB, Dialect, error) {
	d, err := DialectFor(c.Engine)
	if err != nil {
		return nil, nil, err
	}
	dsn, err := d.DSN(c)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open(d.DriverName(), dsn)
	if err != nil {
		return nil, nil, err
	}
	return db, d, nil
}

func queryEach(ctx context.Context, db *sql.DB, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func tableFilter(tables []string) func(string) bool {
	if tables == nil {
		return func(string) bool { return true }
	}
	set := make(map[string]bool, len(tables))
	for _, t := range tables {
		set[t] = true
	}
	return func(name string) bool { return set[name] }
}

func normalizeSQL(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
