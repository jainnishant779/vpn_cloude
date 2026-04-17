package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/database/queries"
	"quicktunnel/server/internal/models"
)

type peerHandlerTestNetworkStore struct {
	getFn func(ctx context.Context, networkID string) (*models.Network, error)
}

func (m *peerHandlerTestNetworkStore) GetNetwork(ctx context.Context, networkID string) (*models.Network, error) {
	if m.getFn == nil {
		return nil, nil
	}
	return m.getFn(ctx, networkID)
}

type peerHandlerTestPeerStore struct {
	deleteByMachineFn func(ctx context.Context, networkID, machineID string) error
}

func (m *peerHandlerTestPeerStore) RegisterPeer(ctx context.Context, peer *models.Peer) (*models.Peer, error) {
	return nil, nil
}

func (m *peerHandlerTestPeerStore) GetPeer(ctx context.Context, peerID string) (*models.Peer, error) {
	return nil, nil
}

func (m *peerHandlerTestPeerStore) ListNetworkPeers(ctx context.Context, networkID string) ([]models.Peer, error) {
	return nil, nil
}

func (m *peerHandlerTestPeerStore) UpdatePeerStatus(ctx context.Context, peerID string, status queries.PeerStatusUpdate) error {
	return nil
}

func (m *peerHandlerTestPeerStore) DeletePeerByMachineID(ctx context.Context, networkID, machineID string) error {
	if m.deleteByMachineFn == nil {
		return nil
	}
	return m.deleteByMachineFn(ctx, networkID, machineID)
}

type peerHandlerEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func newPeerHandlerTestRouter(handler *PeerHandler) http.Handler {
	apiKeyAuth := auth.NewAPIKeyAuth(func(_ context.Context, apiKey string) (string, error) {
		if apiKey == "key-1" {
			return "user-1", nil
		}
		return "", errors.New("invalid api key")
	})

	r := chi.NewRouter()
	r.Use(apiKeyAuth.APIKeyMiddleware)
	r.Post("/api/v1/networks/{id}/peers/unregister", handler.UnregisterPeer)
	return r
}

func TestUnregisterPeerSuccess(t *testing.T) {
	networks := &peerHandlerTestNetworkStore{getFn: func(_ context.Context, networkID string) (*models.Network, error) {
		return &models.Network{ID: "network-uuid", NetworkID: networkID, OwnerID: "user-1"}, nil
	}}
	var deleteCalled bool
	peers := &peerHandlerTestPeerStore{deleteByMachineFn: func(_ context.Context, networkID, machineID string) error {
		deleteCalled = true
		if networkID != "network-uuid" {
			t.Fatalf("expected network id network-uuid, got %s", networkID)
		}
		if machineID != "machine-1" {
			t.Fatalf("expected machine id machine-1, got %s", machineID)
		}
		return nil
	}}

	handler := NewPeerHandler(networks, peers)
	router := newPeerHandlerTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/networks/net-1/peers/unregister", bytes.NewBufferString(`{"machine_id":"machine-1"}`))
	req.Header.Set("X-API-Key", "key-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !deleteCalled {
		t.Fatal("expected delete by machine to be called")
	}

	var envelope peerHandlerEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if !envelope.Success {
		t.Fatalf("expected success response, got error: %s", envelope.Error)
	}

	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("unmarshal data failed: %v", err)
	}
	if data["status"] != "unregistered" {
		t.Fatalf("expected status unregistered, got %v", data["status"])
	}
}

func TestUnregisterPeerIdempotentWhenMissing(t *testing.T) {
	networks := &peerHandlerTestNetworkStore{getFn: func(_ context.Context, networkID string) (*models.Network, error) {
		return &models.Network{ID: "network-uuid", NetworkID: networkID, OwnerID: "user-1"}, nil
	}}
	peers := &peerHandlerTestPeerStore{deleteByMachineFn: func(_ context.Context, _, _ string) error {
		return queries.ErrNotFound
	}}

	handler := NewPeerHandler(networks, peers)
	router := newPeerHandlerTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/networks/net-1/peers/unregister", bytes.NewBufferString(`{"machine_id":"machine-1"}`))
	req.Header.Set("X-API-Key", "key-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var envelope peerHandlerEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if !envelope.Success {
		t.Fatalf("expected success response, got error: %s", envelope.Error)
	}

	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("unmarshal data failed: %v", err)
	}
	if data["status"] != "already_removed" {
		t.Fatalf("expected status already_removed, got %v", data["status"])
	}
}

func TestUnregisterPeerForbiddenForDifferentOwner(t *testing.T) {
	networks := &peerHandlerTestNetworkStore{getFn: func(_ context.Context, networkID string) (*models.Network, error) {
		return &models.Network{ID: "network-uuid", NetworkID: networkID, OwnerID: "user-2"}, nil
	}}
	peers := &peerHandlerTestPeerStore{}

	handler := NewPeerHandler(networks, peers)
	router := newPeerHandlerTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/networks/net-1/peers/unregister", bytes.NewBufferString(`{"machine_id":"machine-1"}`))
	req.Header.Set("X-API-Key", "key-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", rec.Code)
	}
}

func TestUnregisterPeerBadRequestForMissingMachineID(t *testing.T) {
	networks := &peerHandlerTestNetworkStore{getFn: func(_ context.Context, networkID string) (*models.Network, error) {
		return &models.Network{ID: "network-uuid", NetworkID: networkID, OwnerID: "user-1"}, nil
	}}
	deleteCalled := false
	peers := &peerHandlerTestPeerStore{deleteByMachineFn: func(_ context.Context, _, _ string) error {
		deleteCalled = true
		return nil
	}}

	handler := NewPeerHandler(networks, peers)
	router := newPeerHandlerTestRouter(handler)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/networks/net-1/peers/unregister", bytes.NewBufferString(`{"machine_id":"   "}`))
	req.Header.Set("X-API-Key", "key-1")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
	if deleteCalled {
		t.Fatal("delete should not be called for invalid payload")
	}
}

func TestIsVirtualIPConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
		{
			name: "non conflict error",
			err:  errors.New("register peer: timeout"),
			want: false,
		},
		{
			name: "named virtual ip constraint",
			err:  errors.New("duplicate key value violates unique constraint \"peers_network_virtual_ip_unique\""),
			want: true,
		},
		{
			name: "network virtual ip columns",
			err:  errors.New("duplicate key value violates unique constraint (network_id, virtual_ip)"),
			want: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := isVirtualIPConflict(tc.err); got != tc.want {
				t.Fatalf("isVirtualIPConflict() = %v, want %v", got, tc.want)
			}
		})
	}
}
