package main

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Video struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func (s *Store) Migrate(ctx context.Context, schema string) error {
	_, err := s.db.Exec(ctx, schema)
	return err
}

func (s *Store) Create(ctx context.Context, v *Video) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO videos (id, title, description, filename, content_type, size_bytes)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		v.ID, v.Title, v.Description, v.Filename, v.ContentType, v.SizeBytes,
	).Scan(&v.CreatedAt)
}

func (s *Store) List(ctx context.Context) ([]Video, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, title, description, filename, content_type, size_bytes, created_at
		FROM videos
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Video])
}
