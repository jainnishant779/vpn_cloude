package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/database/queries"
	"quicktunnel/server/internal/models"
)

type networkStore interface {
	CreateNetwork(ctx context.Context, network *models.Network) (*models.Network, error)
	GetNetwork(ctx context.Context, networkID string) (*models.Network, error)
	ListUserNetworks(ctx context.Context, userID string) ([]models.Network, error)
	DeleteNetwork(ctx context.Context, networkID string) error
	UpdateNetwork(ctx context.Context, networkID, name, description string) (*models.Network, error)
}

type peerStore interface {
	ListNetworkPeers(ctx context.Context, networkID string) ([]models.Peer, error)
}

// NetworkHandler serves network CRUD routes.
type NetworkHandler struct {
	networks networkStore
	peers    peerStore
}

func NewNetworkHandler(networks networkStore, peers peerStore) *NetworkHandler {
	return &NetworkHandler{
		networks: networks,
		peers:    peers,
	}
}

type createNetworkRequest struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	MaxPeers      int    `json:"max_peers"`
	CIDR          string `json:"cidr"`
	AccessControl string `json:"access_control"`
}

func (h *NetworkHandler) CreateNetwork(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	var req createNetworkRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.MaxPeers <= 0 {
		req.MaxPeers = 25
	}
	if req.CIDR == "" {
		req.CIDR = "10.7.0.0/16"
	}
	if req.AccessControl == "" {
		req.AccessControl = "approve"
	}
	if req.AccessControl != "approve" && req.AccessControl != "auto" {
		writeError(w, http.StatusBadRequest, "access_control must be 'approve' or 'auto'")
		return
	}

	networkID, err := generateNetworkID(12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to generate network id")
		return
	}

	created, err := h.networks.CreateNetwork(r.Context(), &models.Network{
		OwnerID:       userID,
		Name:          req.Name,
		NetworkID:     networkID,
		CIDR:          req.CIDR,
		Description:   req.Description,
		MaxPeers:      req.MaxPeers,
		IsActive:      true,
		AccessControl: req.AccessControl,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create network")
		return
	}

	writeSuccess(w, http.StatusCreated, created)
}

func (h *NetworkHandler) ListNetworks(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networks, err := h.networks.ListUserNetworks(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load networks")
		return
	}

	writeSuccess(w, http.StatusOK, networks)
}

func (h *NetworkHandler) GetNetwork(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	id := chi.URLParam(r, "id")
	network, err := h.networks.GetNetwork(r.Context(), id)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}

	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "you do not have access to this network")
		return
	}

	peers, err := h.peers.ListNetworkPeers(r.Context(), network.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load peers")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]any{
		"network":    network,
		"peer_count": len(peers),
	})
}

type updateNetworkRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (h *NetworkHandler) UpdateNetwork(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	id := chi.URLParam(r, "id")
	existing, err := h.networks.GetNetwork(r.Context(), id)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if existing.OwnerID != userID {
		writeError(w, http.StatusForbidden, "you do not have access to this network")
		return
	}

	var req updateNetworkRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	description := strings.TrimSpace(req.Description)
	if name == "" {
		name = existing.Name
	}
	if description == "" {
		description = existing.Description
	}

	updated, err := h.networks.UpdateNetwork(r.Context(), id, name, description)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update network")
		return
	}

	writeSuccess(w, http.StatusOK, updated)
}

func (h *NetworkHandler) DeleteNetwork(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	id := chi.URLParam(r, "id")
	network, err := h.networks.GetNetwork(r.Context(), id)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "you do not have access to this network")
		return
	}

	if err := h.networks.DeleteNetwork(r.Context(), id); err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete network")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func generateNetworkID(length int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	if length <= 0 {
		return "", fmt.Errorf("generate network id: invalid length")
	}

	buf := make([]byte, length)
	for i := range buf {
		index, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("generate network id: random index: %w", err)
		}
		buf[i] = alphabet[index.Int64()]
	}

	return string(buf), nil
}
