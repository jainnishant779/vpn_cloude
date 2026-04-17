package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/database/queries"
	"quicktunnel/server/internal/models"
)

type userStore interface {
	CreateUser(ctx context.Context, user *models.User) (*models.User, error)
	GetUserByEmail(ctx context.Context, email string) (*models.User, error)
}

// AuthHandler serves registration and session endpoints.
type AuthHandler struct {
	users userStore
	jwt   *auth.JWTService
}

func NewAuthHandler(users userStore, jwtService *auth.JWTService) *AuthHandler {
	return &AuthHandler{
		users: users,
		jwt:   jwtService,
	}
}

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate api key")
		return
	}

	created, err := h.users.CreateUser(r.Context(), &models.User{
		Email:        req.Email,
		PasswordHash: hash,
		Name:         req.Name,
		APIKey:       apiKey,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to create user")
		return
	}

	writeSuccess(w, http.StatusCreated, map[string]any{
		"id":         created.ID,
		"email":      created.Email,
		"name":       created.Name,
		"api_key":    created.APIKey,
		"created_at": created.CreatedAt,
	})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := h.users.GetUserByEmail(r.Context(), req.Email)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	if !auth.CheckPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	accessToken, err := h.jwt.GenerateAccessTokenWithEmail(user.ID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue access token")
		return
	}

	refreshToken, err := h.jwt.GenerateRefreshTokenWithEmail(user.ID, user.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue refresh token")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]any{
		"access_token":  accessToken,
		"refresh_token": refreshToken,
		"api_key":       user.APIKey,
		"user": map[string]any{
			"id":    user.ID,
			"email": user.Email,
			"name":  user.Name,
		},
	})
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (h *AuthHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	claims, err := h.jwt.ValidateToken(req.RefreshToken)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	if claims.RegisteredClaims.ID != "refresh" {
		writeError(w, http.StatusUnauthorized, "token is not a refresh token")
		return
	}

	accessToken, err := h.jwt.GenerateAccessTokenWithEmail(claims.UserID, claims.Email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to issue access token")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]any{
		"access_token": accessToken,
	})
}

func generateAPIKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
