package store

import (
	"context"
	"database/sql"
	"time"

	"dbcompare/internal/engine"
)

type Connection struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Engine        string    `json:"engine"`
	Host          string    `json:"host"`
	Port          int       `json:"port"`
	Database      string    `json:"database"`
	Schema        string    `json:"schema"`
	Username      string    `json:"username"`
	HasPassword   bool      `json:"has_password"`
	SSLMode       string    `json:"ssl_mode"`
	ServerVersion string    `json:"server_version"`
	Protected     bool      `json:"protected"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Label identifies a connection in reports.
func (c Connection) Label() string {
	label := c.Name + " (" + c.Engine + " " + c.Host + "/" + c.Database
	if c.Engine == engine.EnginePostgres {
		label += "." + c.ConnInfo("").SchemaName()
	}
	return label + ")"
}

// ConnInfo converts the connection to engine settings with the given
// password.
func (c Connection) ConnInfo(password string) engine.ConnInfo {
	return engine.ConnInfo{
		Engine:   c.Engine,
		Host:     c.Host,
		Port:     c.Port,
		Database: c.Database,
		Schema:   c.Schema,
		Username: c.Username,
		Password: password,
		SSLMode:  c.SSLMode,
	}
}

// ConnectionInput is the writable part of a connection. A nil Password
// keeps the stored password on update.
type ConnectionInput struct {
	Name     string  `json:"name"`
	Engine   string  `json:"engine"`
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	Database string  `json:"database"`
	Schema   string  `json:"schema"`
	Username string  `json:"username"`
	Password *string `json:"password"`
	SSLMode  string  `json:"ssl_mode"`
	// Protected limits compares that use this connection to admins.
	Protected bool `json:"protected"`
}

const connectionColumns = `id, name, engine, host, port, database_name, schema_name, username,
	password_enc IS NOT NULL, ssl_mode, server_version, protected, created_at, updated_at`

func scanConnection(row interface{ Scan(...any) error }) (Connection, error) {
	var c Connection
	err := row.Scan(&c.ID, &c.Name, &c.Engine, &c.Host, &c.Port, &c.Database, &c.Schema, &c.Username,
		&c.HasPassword, &c.SSLMode, &c.ServerVersion, &c.Protected, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+connectionColumns+" FROM connections ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConnection(ctx context.Context, id int64) (Connection, error) {
	c, err := scanConnection(s.db.QueryRowContext(ctx, "SELECT "+connectionColumns+" FROM connections WHERE id = $1", id))
	return c, mapError(err)
}

// ConnectionPassword decrypts the stored password.
func (s *Store) ConnectionPassword(ctx context.Context, id int64) (string, error) {
	var sealed []byte
	if err := s.db.QueryRowContext(ctx, "SELECT password_enc FROM connections WHERE id = $1", id).Scan(&sealed); err != nil {
		return "", mapError(err)
	}
	if sealed == nil {
		return "", nil
	}
	return s.box.Open(sealed)
}

func (s *Store) sealPassword(p *string) (any, error) {
	if p == nil || *p == "" {
		return nil, nil
	}
	return s.box.Seal(*p)
}

func (s *Store) CreateConnection(ctx context.Context, in ConnectionInput) (Connection, error) {
	sealed, err := s.sealPassword(in.Password)
	if err != nil {
		return Connection{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO connections (name, engine, host, port, database_name, schema_name, username, password_enc, ssl_mode, protected)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+connectionColumns,
		in.Name, in.Engine, in.Host, in.Port, in.Database, in.Schema, in.Username, sealed, in.SSLMode, in.Protected)
	c, err := scanConnection(row)
	return c, mapError(err)
}

func (s *Store) UpdateConnection(ctx context.Context, id int64, in ConnectionInput) (Connection, error) {
	var row *sql.Row
	if in.Password == nil {
		row = s.db.QueryRowContext(ctx, `
			UPDATE connections SET name = $2, engine = $3, host = $4, port = $5, database_name = $6,
			       schema_name = $7, username = $8, ssl_mode = $9, protected = $10, updated_at = now()
			WHERE id = $1
			RETURNING `+connectionColumns,
			id, in.Name, in.Engine, in.Host, in.Port, in.Database, in.Schema, in.Username, in.SSLMode, in.Protected)
	} else {
		sealed, err := s.sealPassword(in.Password)
		if err != nil {
			return Connection{}, err
		}
		row = s.db.QueryRowContext(ctx, `
			UPDATE connections SET name = $2, engine = $3, host = $4, port = $5, database_name = $6,
			       schema_name = $7, username = $8, ssl_mode = $9, protected = $10, password_enc = $11, updated_at = now()
			WHERE id = $1
			RETURNING `+connectionColumns,
			id, in.Name, in.Engine, in.Host, in.Port, in.Database, in.Schema, in.Username, in.SSLMode, in.Protected, sealed)
	}
	c, err := scanConnection(row)
	return c, mapError(err)
}

func (s *Store) DeleteConnection(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM connections WHERE id = $1", id)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetServerVersion(ctx context.Context, id int64, version string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE connections SET server_version = $2 WHERE id = $1 AND server_version <> $2", id, version)
	return err
}
