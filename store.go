package main

import (
	"context"
	"errors"
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

type User struct {
	ID        uuid.UUID `json:"-"`
	GoogleSub string    `json:"-"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Picture   string    `json:"picture"`
}

type Store struct {
	db *pgxpool.Pool
}

func (s *Store) Migrate(ctx context.Context, schema string) error {
	_, err := s.db.Exec(ctx, schema)
	return err
}

func (s *Store) Create(ctx context.Context, v *Video, userID uuid.UUID) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO videos (id, title, description, filename, content_type, size_bytes, user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`,
		v.ID, v.Title, v.Description, v.Filename, v.ContentType, v.SizeBytes, userID,
	).Scan(&v.CreatedAt)
}

// UpsertUser keeps the profile current, because people can change it at Google.
// It sets u.ID.
func (s *Store) UpsertUser(ctx context.Context, u *User) error {
	return s.db.QueryRow(ctx, `
		INSERT INTO users (id, google_sub, email, name, picture)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (google_sub) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name, picture = EXCLUDED.picture
		RETURNING id`,
		uuid.New(), u.GoogleSub, u.Email, u.Name, u.Picture,
	).Scan(&u.ID)
}

// CreateSession also clears expired sessions, so the table does not keep growing.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID uuid.UUID, expiresAt time.Time) error {
	if _, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// UserBySession returns errSignInRequired for an unknown or expired session.
func (s *Store) UserBySession(ctx context.Context, tokenHash []byte) (*User, error) {
	var u User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.google_sub, u.email, u.name, u.picture
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`,
		tokenHash,
	).Scan(&u.ID, &u.GoogleSub, &u.Email, &u.Name, &u.Picture)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errSignInRequired
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
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
