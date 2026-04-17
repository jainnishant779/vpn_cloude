package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	accessTokenTTL  = 15 * time.Minute
	refreshTokenTTL = 7 * 24 * time.Hour
)

// Claims carries identity information needed by protected routes.
type Claims struct {
	UserID    string `json:"user_id"`
	Email     string `json:"email,omitempty"`
	ExpiresAt int64  `json:"expires_at"`
	jwt.RegisteredClaims
}

// JWTService issues and validates signed JWT tokens.
type JWTService struct {
	secret []byte
}

func NewJWTService(secret string) (*JWTService, error) {
	if secret == "" {
		return nil, fmt.Errorf("new jwt service: secret is required")
	}
	return &JWTService{secret: []byte(secret)}, nil
}

// GenerateAccessToken creates a short-lived token for API access.
func (s *JWTService) GenerateAccessToken(userID string) (string, error) {
	return s.generateToken(userID, "", accessTokenTTL, "access")
}

// GenerateRefreshToken creates a longer-lived token for session renewal.
func (s *JWTService) GenerateRefreshToken(userID string) (string, error) {
	return s.generateToken(userID, "", refreshTokenTTL, "refresh")
}

// GenerateAccessTokenWithEmail allows callers to embed email in claims.
func (s *JWTService) GenerateAccessTokenWithEmail(userID, email string) (string, error) {
	return s.generateToken(userID, email, accessTokenTTL, "access")
}

// GenerateRefreshTokenWithEmail allows callers to embed email in claims.
func (s *JWTService) GenerateRefreshTokenWithEmail(userID, email string) (string, error) {
	return s.generateToken(userID, email, refreshTokenTTL, "refresh")
}

func (s *JWTService) generateToken(userID, email string, ttl time.Duration, tokenType string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("generate token: user id is required")
	}

	expiresAt := time.Now().UTC().Add(ttl)
	claims := &Claims{
		UserID:    userID,
		Email:     email,
		ExpiresAt: expiresAt.Unix(),
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			ID:        tokenType,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("generate token: sign token: %w", err)
	}
	return signed, nil
}

// ValidateToken parses and verifies an incoming token.
func (s *JWTService) ValidateToken(tokenStr string) (*Claims, error) {
	if tokenStr == "" {
		return nil, fmt.Errorf("validate token: token is required")
	}

	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Method)
		}
		return s.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}))
	if err != nil {
		return nil, fmt.Errorf("validate token: parse token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("validate token: invalid claims")
	}

	return claims, nil
}
