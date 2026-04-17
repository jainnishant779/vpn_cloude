package queries

import (
	"context"
	"fmt"

	"quicktunnel/server/internal/database"
	"quicktunnel/server/internal/models"
)

// UserStore handles persistence for user records.
type UserStore struct {
	db *database.DB
}

func NewUserStore(db *database.DB) *UserStore {
	return &UserStore{db: db}
}

func (s *UserStore) CreateUser(ctx context.Context, user *models.User) (*models.User, error) {
	if user == nil {
		return nil, fmt.Errorf("create user: user is nil")
	}

	const query = `
INSERT INTO users (email, password_hash, name, api_key)
VALUES ($1, $2, $3, $4)
RETURNING id, email, password_hash, name, api_key, created_at, updated_at;`

	out := &models.User{}
	if err := s.db.Pool.QueryRow(ctx, query, user.Email, user.PasswordHash, user.Name, user.APIKey).Scan(
		&out.ID,
		&out.Email,
		&out.PasswordHash,
		&out.Name,
		&out.APIKey,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	return out, nil
}

func (s *UserStore) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	const query = `
SELECT id, email, password_hash, name, api_key, created_at, updated_at
FROM users
WHERE email = $1;`

	out := &models.User{}
	err := s.db.Pool.QueryRow(ctx, query, email).Scan(
		&out.ID,
		&out.Email,
		&out.PasswordHash,
		&out.Name,
		&out.APIKey,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", mapNotFound(err))
	}

	return out, nil
}

func (s *UserStore) GetUserByAPIKey(ctx context.Context, apiKey string) (*models.User, error) {
	const query = `
SELECT id, email, password_hash, name, api_key, created_at, updated_at
FROM users
WHERE api_key = $1;`

	out := &models.User{}
	err := s.db.Pool.QueryRow(ctx, query, apiKey).Scan(
		&out.ID,
		&out.Email,
		&out.PasswordHash,
		&out.Name,
		&out.APIKey,
		&out.CreatedAt,
		&out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get user by api key: %w", mapNotFound(err))
	}

	return out, nil
}
