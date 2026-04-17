package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPasswordAndCheckPassword(t *testing.T) {
	hash, err := HashPassword("StrongPass123")
	require.NoError(t, err)
	require.NotEmpty(t, hash)

	assert.True(t, CheckPassword("StrongPass123", hash))
	assert.False(t, CheckPassword("wrong-password", hash))
}

func TestHashPasswordRejectsEmptyPassword(t *testing.T) {
	hash, err := HashPassword("")
	assert.Error(t, err)
	assert.Empty(t, hash)
}

func TestCheckPasswordRejectsEmptyInputs(t *testing.T) {
	assert.False(t, CheckPassword("", ""))
	assert.False(t, CheckPassword("password", ""))
	assert.False(t, CheckPassword("", "hash"))
}
