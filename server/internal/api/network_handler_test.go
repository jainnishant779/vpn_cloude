package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testNetworkStore struct {
	createFn       func(ctx context.Context, network *models.Network) (*models.Network, error)
	getFn          func(ctx context.Context, networkID string) (*models.Network, error)
	listFn         func(ctx context.Context, userID string) ([]models.Network, error)
	deleteFn       func(ctx context.Context, networkID string) error
	updateFn       func(ctx context.Context, networkID, name, description string) (*models.Network, error)
	capturedCreate *models.Network
}

func (m *testNetworkStore) CreateNetwork(ctx context.Context, network *models.Network) (*models.Network, error) {
	m.capturedCreate = network
	return m.createFn(ctx, network)
}

func (m *testNetworkStore) GetNetwork(ctx context.Context, networkID string) (*models.Network, error) {
	return m.getFn(ctx, networkID)
}

func (m *testNetworkStore) ListUserNetworks(ctx context.Context, userID string) ([]models.Network, error) {
	return m.listFn(ctx, userID)
}

func (m *testNetworkStore) DeleteNetwork(ctx context.Context, networkID string) error {
	return m.deleteFn(ctx, networkID)
}

func (m *testNetworkStore) UpdateNetwork(ctx context.Context, networkID, name, description string) (*models.Network, error) {
	return m.updateFn(ctx, networkID, name, description)
}

type testPeerStore struct {
	listFn func(ctx context.Context, networkID string) ([]models.Peer, error)
}

func (m *testPeerStore) ListNetworkPeers(ctx context.Context, networkID string) ([]models.Peer, error) {
	return m.listFn(ctx, networkID)
}

func TestNetworkHandlerCreateNetworkSuccess(t *testing.T) {
	jwtService, err := auth.NewJWTService("test-secret")
	require.NoError(t, err)

	networkStore := &testNetworkStore{
		createFn: func(_ context.Context, network *models.Network) (*models.Network, error) {
			return &models.Network{
				ID:          "network-1",
				OwnerID:     network.OwnerID,
				Name:        network.Name,
				NetworkID:   network.NetworkID,
				CIDR:        network.CIDR,
				Description: network.Description,
				MaxPeers:    network.MaxPeers,
				IsActive:    true,
				CreatedAt:   time.Now().UTC(),
			}, nil
		},
		getFn:    func(_ context.Context, _ string) (*models.Network, error) { return nil, nil },
		listFn:   func(_ context.Context, _ string) ([]models.Network, error) { return nil, nil },
		deleteFn: func(_ context.Context, _ string) error { return nil },
		updateFn: func(_ context.Context, _, _, _ string) (*models.Network, error) { return nil, nil },
	}

	peerStore := &testPeerStore{listFn: func(_ context.Context, _ string) ([]models.Peer, error) { return nil, nil }}
	handler := NewNetworkHandler(networkStore, peerStore)

	token, err := jwtService.GenerateAccessToken("user-123")
	require.NoError(t, err)

	body := []byte(`{"name":"Office","description":"Main office"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/networks", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	jwtService.AuthMiddleware(http.HandlerFunc(handler.CreateNetwork)).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	require.NotNil(t, networkStore.capturedCreate)
	assert.Equal(t, "user-123", networkStore.capturedCreate.OwnerID)
	assert.Equal(t, "Office", networkStore.capturedCreate.Name)
}

func TestNetworkHandlerGetNetworkIncludesPeerCount(t *testing.T) {
	jwtService, err := auth.NewJWTService("test-secret")
	require.NoError(t, err)

	networkStore := &testNetworkStore{
		createFn: func(_ context.Context, _ *models.Network) (*models.Network, error) { return nil, nil },
		getFn: func(_ context.Context, networkID string) (*models.Network, error) {
			return &models.Network{
				ID:      networkID,
				OwnerID: "user-123",
				Name:    "Office",
			}, nil
		},
		listFn:   func(_ context.Context, _ string) ([]models.Network, error) { return nil, nil },
		deleteFn: func(_ context.Context, _ string) error { return nil },
		updateFn: func(_ context.Context, _, _, _ string) (*models.Network, error) { return nil, nil },
	}

	peerStore := &testPeerStore{listFn: func(_ context.Context, _ string) ([]models.Peer, error) {
		return []models.Peer{{ID: "peer-1"}, {ID: "peer-2"}}, nil
	}}

	handler := NewNetworkHandler(networkStore, peerStore)

	r := chi.NewRouter()
	r.Use(jwtService.AuthMiddleware)
	r.Get("/api/v1/networks/{id}", handler.GetNetwork)

	token, err := jwtService.GenerateAccessToken("user-123")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/networks/network-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, true, envelope["success"])

	data, ok := envelope["data"].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 2, data["peer_count"])
}
