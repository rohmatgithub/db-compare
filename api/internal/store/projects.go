package store

import (
	"context"
	"time"

	"dbcompare/internal/engine"
)

type Project struct {
	ID                 int64          `json:"id"`
	Name               string         `json:"name"`
	SourceConnectionID int64          `json:"source_connection_id"`
	TargetConnectionID int64          `json:"target_connection_id"`
	Options            engine.Options `json:"options"`
	CreatedAt          time.Time      `json:"created_at"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type ProjectInput struct {
	Name               string         `json:"name"`
	SourceConnectionID int64          `json:"source_connection_id"`
	TargetConnectionID int64          `json:"target_connection_id"`
	Options            engine.Options `json:"options"`
}

const projectColumns = "id, name, source_connection_id, target_connection_id, options, created_at, updated_at"

func scanProject(row interface{ Scan(...any) error }) (Project, error) {
	var p Project
	var opts []byte
	if err := row.Scan(&p.ID, &p.Name, &p.SourceConnectionID, &p.TargetConnectionID, &opts, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return p, err
	}
	return p, fromJSON(opts, &p.Options)
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+projectColumns+" FROM compare_projects ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetProject(ctx context.Context, id int64) (Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, "SELECT "+projectColumns+" FROM compare_projects WHERE id = $1", id))
	return p, mapError(err)
}

func (s *Store) CreateProject(ctx context.Context, in ProjectInput) (Project, error) {
	opts, err := toJSON(in.Options)
	if err != nil {
		return Project{}, err
	}
	p, err := scanProject(s.db.QueryRowContext(ctx, `
		INSERT INTO compare_projects (name, source_connection_id, target_connection_id, options)
		VALUES ($1, $2, $3, $4)
		RETURNING `+projectColumns,
		in.Name, in.SourceConnectionID, in.TargetConnectionID, opts))
	return p, mapError(err)
}

func (s *Store) UpdateProject(ctx context.Context, id int64, in ProjectInput) (Project, error) {
	opts, err := toJSON(in.Options)
	if err != nil {
		return Project{}, err
	}
	p, err := scanProject(s.db.QueryRowContext(ctx, `
		UPDATE compare_projects
		SET name = $2, source_connection_id = $3, target_connection_id = $4, options = $5, updated_at = now()
		WHERE id = $1
		RETURNING `+projectColumns,
		id, in.Name, in.SourceConnectionID, in.TargetConnectionID, opts))
	return p, mapError(err)
}

func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM compare_projects WHERE id = $1", id)
	if err != nil {
		return mapError(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ProjectProtected reports whether the source or target connection of the
// project is protected.
func (s *Store) ProjectProtected(ctx context.Context, projectID int64) (bool, error) {
	var protected bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM compare_projects p
			JOIN connections c ON c.id IN (p.source_connection_id, p.target_connection_id)
			WHERE p.id = $1 AND c.protected)`,
		projectID).Scan(&protected)
	return protected, err
}
