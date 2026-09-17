package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"dbcompare/internal/auth"
)

// ErrLastAdmin is returned when a change would leave no active admin.
var ErrLastAdmin = errors.New("at least one active admin is required")

type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	Disabled    bool       `json:"disabled"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

const userColumns = "u.id, u.username, u.role, u.disabled, u.created_at, u.updated_at, u.last_login_at"

func scanUser(row interface{ Scan(...any) error }, extra ...any) (User, error) {
	var u User
	dest := append([]any{&u.ID, &u.Username, &u.Role, &u.Disabled, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt}, extra...)
	err := row.Scan(dest...)
	return u, err
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&n)
	return n, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+userColumns+" FROM users u ORDER BY u.username")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) GetUser(ctx context.Context, id int64) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id = $1", id))
	return u, mapError(err)
}

// UserCredentials returns the user with the given name and its password
// hash.
func (s *Store) UserCredentials(ctx context.Context, username string) (User, string, error) {
	var hash string
	u, err := scanUser(s.db.QueryRowContext(ctx,
		"SELECT "+userColumns+", u.password_hash FROM users u WHERE u.username = $1", username), &hash)
	return u, hash, mapError(err)
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `
		INSERT INTO users AS u (username, password_hash, role) VALUES ($1, $2, $3)
		RETURNING `+userColumns,
		username, passwordHash, role))
	return u, mapError(err)
}

// guardLastAdmin locks the active admins for the rest of tx and fails with
// ErrLastAdmin when user id is the only one and keepsAdmin is false.
func guardLastAdmin(ctx context.Context, tx *sql.Tx, id int64, keepsAdmin bool) error {
	rows, err := tx.QueryContext(ctx,
		"SELECT id FROM users WHERE role = $1 AND NOT disabled ORDER BY id FOR UPDATE", auth.RoleAdmin)
	if err != nil {
		return err
	}
	defer rows.Close()
	var admins []int64
	for rows.Next() {
		var a int64
		if err := rows.Scan(&a); err != nil {
			return err
		}
		admins = append(admins, a)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !keepsAdmin && len(admins) == 1 && slices.Contains(admins, id) {
		return ErrLastAdmin
	}
	return nil
}

// UpdateUser changes the role and disabled flag of a user. Disabling a user
// ends all of its sessions.
func (s *Store) UpdateUser(ctx context.Context, id int64, role string, disabled bool) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()

	if err := guardLastAdmin(ctx, tx, id, role == auth.RoleAdmin && !disabled); err != nil {
		return User{}, err
	}
	u, err := scanUser(tx.QueryRowContext(ctx, `
		UPDATE users AS u SET role = $2, disabled = $3, updated_at = now()
		WHERE u.id = $1
		RETURNING `+userColumns,
		id, role, disabled))
	if err != nil {
		return User{}, mapError(err)
	}
	if disabled {
		if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = $1", id); err != nil {
			return User{}, err
		}
	}
	return u, tx.Commit()
}

// SetUserPassword replaces the password of a user and ends its sessions,
// except the session whose token hash is keepSession (which may be nil).
func (s *Store) SetUserPassword(ctx context.Context, id int64, passwordHash string, keepSession []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		"UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1", id, passwordHash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM sessions WHERE user_id = $1 AND token_hash IS DISTINCT FROM $2", id, keepSession); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := guardLastAdmin(ctx, tx, id, false); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// CreateSession stores a session for the user and records the login time.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, expiresAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		"INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)",
		tokenHash, userID, expiresAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET last_login_at = now() WHERE id = $1", userID); err != nil {
		return err
	}
	return tx.Commit()
}

// SessionUser returns the enabled user owning an unexpired session.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte) (User, error) {
	u, err := scanUser(s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+`
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND NOT u.disabled`,
		tokenHash))
	return u, mapError(err)
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = $1", tokenHash)
	return err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= now()")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
