package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testAuthUserStore struct {
	createFn    func(ctx context.Context, user *models.User) (*models.User, error)
	getByEmail  func(ctx context.Context, email string) (*models.User, error)
}

func (m *testAuthUserStore) CreateUser(ctx context.Context, user *models.User) (*models.User, error) {
	return m.createFn(ctx, user)
}

func (m *testAuthUserStore) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	return m.getByEmail(ctx, email)
}

func TestAuthHandlerRegisterSuccess(t *testing.T) {
	jwtService, err := auth.NewJWTService("test-secret")
	require.NoError(t, err)

	store := &testAuthUserStore{
		createFn: func(_ context.Context, user *models.User) (*models.User, error) {
			return &models.User{
				ID:        "user-1",
				Email:     user.Email,
				Name:      user.Name,
				APIKey:    user.APIKey,
				CreatedAt: time.Now().UTC(),
			}, nil
		},
		getByEmail: func(_ context.Context, _ string) (*models.User, error) {
			return nil, nil
		},
	}

	handler := NewAuthHandler(store, jwtService)

	body := []byte(`{"email":"alice@example.com","password":"password123","name":"Alice"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.Register(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, true, envelope["success"])
	assert.Empty(t, envelope["error"])
}

func TestAuthHandlerLoginRejectsInvalidPassword(t *testing.T) {
	jwtService, err := auth.NewJWTService("test-secret")
	require.NoError(t, err)

	hash, err := auth.HashPassword("password123")
	require.NoError(t, err)

	store := &testAuthUserStore{
		createFn: func(_ context.Context, _ *models.User) (*models.User, error) {
			return nil, nil
		},
		getByEmail: func(_ context.Context, email string) (*models.User, error) {
			return &models.User{
				ID:           "user-1",
				Email:        email,
				PasswordHash: hash,
				APIKey:       "api-key",
			}, nil
		},
	}

	handler := NewAuthHandler(store, jwtService)
	body := []byte(`{"email":"alice@example.com","password":"wrong"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.Login(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
