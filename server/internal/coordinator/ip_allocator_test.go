package coordinator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateIPSequentially(t *testing.T) {
	ip, err := AllocateIP("10.7.0.0/24", nil)
	require.NoError(t, err)
	assert.Equal(t, "10.7.0.2", ip)
}

func TestAllocateIPSkipsUsedAddresses(t *testing.T) {
	ip, err := AllocateIP("10.7.0.0/24", []string{"10.7.0.2", "10.7.0.3"})
	require.NoError(t, err)
	assert.Equal(t, "10.7.0.4", ip)
}

func TestAllocateIPReturnsErrorWhenExhausted(t *testing.T) {
	_, err := AllocateIP("10.7.0.0/30", []string{"10.7.0.2"})
	assert.Error(t, err)
}

func TestAllocateIPRejectsInvalidCIDR(t *testing.T) {
	_, err := AllocateIP("invalid-cidr", nil)
	assert.Error(t, err)
}
