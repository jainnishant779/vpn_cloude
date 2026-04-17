package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJWTServiceGenerateAndValidateAccessToken(t *testing.T) {
	svc, err := NewJWTService("test-secret")
	require.NoError(t, err)

	token, err := svc.GenerateAccessToken("user-123")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := svc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID)
	assert.Equal(t, "access", claims.RegisteredClaims.ID)
	assert.NotZero(t, claims.ExpiresAt)
}

func TestJWTServiceGenerateAndValidateRefreshToken(t *testing.T) {
	svc, err := NewJWTService("test-secret")
	require.NoError(t, err)

	token, err := svc.GenerateRefreshToken("user-456")
	require.NoError(t, err)

	claims, err := svc.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-456", claims.UserID)
	assert.Equal(t, "refresh", claims.RegisteredClaims.ID)
}

func TestJWTServiceValidateTokenRejectsInvalidToken(t *testing.T) {
	svc, err := NewJWTService("test-secret")
	require.NoError(t, err)

	claims, err := svc.ValidateToken("not-a-token")
	assert.Error(t, err)
	assert.Nil(t, claims)
}
