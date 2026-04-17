package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/coordinator"
	"quicktunnel/server/internal/database/queries"
	"quicktunnel/server/internal/models"
)

type peerNetworkReader interface {
	GetNetwork(ctx context.Context, networkID string) (*models.Network, error)
}

type peerRecordStore interface {
	RegisterPeer(ctx context.Context, peer *models.Peer) (*models.Peer, error)
	GetPeer(ctx context.Context, peerID string) (*models.Peer, error)
	ListNetworkPeers(ctx context.Context, networkID string) ([]models.Peer, error)
	UpdatePeerStatus(ctx context.Context, peerID string, status queries.PeerStatusUpdate) error
	DeletePeerByMachineID(ctx context.Context, networkID, machineID string) error
}

// PeerHandler serves peer registration, listing, and heartbeat operations.
type PeerHandler struct {
	networks peerNetworkReader
	peers    peerRecordStore
}

var registerPeerLocks sync.Map

func NewPeerHandler(networks peerNetworkReader, peers peerRecordStore) *PeerHandler {
	return &PeerHandler{
		networks: networks,
		peers:    peers,
	}
}

type peerRegisterRequest struct {
	MachineID string `json:"machine_id"`
	PublicKey string `json:"public_key"`
	Name      string `json:"name"`
	OS        string `json:"os"`
	Version   string `json:"version"`
	VNCPort   int    `json:"vnc_port"`
}

type peerRegistrationResponse struct {
	VirtualIP   string        `json:"virtual_ip"`
	NetworkCIDR string        `json:"network_cidr"`
	PeerID      string        `json:"peer_id"`
	PublicKey   string        `json:"public_key"`
	Peers       []models.Peer `json:"peers,omitempty"`
}

func normalizePeerVirtualIPv4(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	if parsed := net.ParseIP(trimmed); parsed != nil {
		if ipv4 := parsed.To4(); ipv4 != nil {
			return ipv4.String()
		}
		return ""
	}

	ip, _, err := net.ParseCIDR(trimmed)
	if err != nil || ip == nil {
		return ""
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return ipv4.String()
	}
	return ""
}

func lockPeerRegisterNetwork(networkID string) func() {
	key := strings.TrimSpace(networkID)
	if key == "" {
		key = "default"
	}

	lockValue, _ := registerPeerLocks.LoadOrStore(key, &sync.Mutex{})
	mu := lockValue.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func isVirtualIPConflict(err error) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "duplicate key value violates unique constraint") {
		return false
	}

	if strings.Contains(message, "peers_network_virtual_ip_unique") {
		return true
	}

	return strings.Contains(message, "(network_id, virtual_ip)")
}

// RegisterPeer handles machine registration and virtual IP assignment.
func (h *PeerHandler) RegisterPeer(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networkID := chi.URLParam(r, "id")
	network, err := h.networks.GetNetwork(r.Context(), networkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "register.load_network").
			Str("network_id", networkID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "you do not have access to this network")
		return
	}

	var req peerRegisterRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.MachineID = strings.TrimSpace(req.MachineID)
	req.PublicKey = strings.TrimSpace(req.PublicKey)
	req.Name = strings.TrimSpace(req.Name)
	req.OS = strings.TrimSpace(req.OS)
	req.Version = strings.TrimSpace(req.Version)
	if req.MachineID == "" || req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "machine_id and public_key are required")
		return
	}
	if req.VNCPort == 0 {
		req.VNCPort = 5900
	}

	unlock := lockPeerRegisterNetwork(network.ID)
	defer unlock()

	var registered *models.Peer
	const maxRegisterAttempts = 3
	for attempt := 1; attempt <= maxRegisterAttempts; attempt++ {
		currentPeers, err := h.peers.ListNetworkPeers(r.Context(), network.ID)
		if err != nil {
			log.Error().Err(err).
				Str("handler", "peer").
				Str("operation", "register.inspect_existing_peers").
				Str("network_id", network.ID).
				Str("user_id", userID).
				Msg("peer handler operation failed")
			writeError(w, http.StatusInternalServerError, "failed to inspect existing peers")
			return
		}

		usedIPs := make([]string, 0, len(currentPeers))
		assignedIP := ""
		for _, existing := range currentPeers {
			normalizedExisting := normalizePeerVirtualIPv4(existing.VirtualIP)
			if normalizedExisting != "" {
				usedIPs = append(usedIPs, normalizedExisting)
			}
			if existing.MachineID == req.MachineID {
				assignedIP = normalizedExisting
			}
		}

		if assignedIP != "" {
			for _, existing := range currentPeers {
				if existing.MachineID == req.MachineID {
					continue
				}
				if normalizePeerVirtualIPv4(existing.VirtualIP) == assignedIP {
					assignedIP = ""
					break
				}
			}
		}

		if assignedIP == "" {
			assignedIP, err = coordinator.AllocateIP(network.CIDR, usedIPs)
			if err != nil {
				writeError(w, http.StatusConflict, "no available virtual ip in network")
				return
			}
		}

		registered, err = h.peers.RegisterPeer(r.Context(), &models.Peer{
			NetworkID:      network.ID,
			Name:           req.Name,
			MachineID:      req.MachineID,
			PublicKey:      req.PublicKey,
			VirtualIP:      assignedIP,
			LocalEndpoints: []string{},
			OS:             req.OS,
			Version:        req.Version,
			VNCPort:        req.VNCPort,
			VNCAvailable:   false,
		})
		if err == nil {
			break
		}

		if isVirtualIPConflict(err) {
			if attempt < maxRegisterAttempts {
				log.Warn().Err(err).
					Str("handler", "peer").
					Str("operation", "register.virtual_ip_conflict_retry").
					Str("network_id", network.ID).
					Str("user_id", userID).
					Str("machine_id", req.MachineID).
					Int("attempt", attempt).
					Msg("peer register conflict detected; retrying allocation")
				time.Sleep(time.Duration(attempt*25) * time.Millisecond)
				continue
			}

			log.Warn().Err(err).
				Str("handler", "peer").
				Str("operation", "register.virtual_ip_conflict_final").
				Str("network_id", network.ID).
				Str("user_id", userID).
				Str("machine_id", req.MachineID).
				Msg("peer register conflict persists after retries")
			writeError(w, http.StatusConflict, "virtual ip allocation conflict, retry")
			return
		}

		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "register.create_or_update_peer").
			Str("network_id", network.ID).
			Str("user_id", userID).
			Str("machine_id", req.MachineID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to register peer")
		return
	}

	if registered == nil {
		writeError(w, http.StatusConflict, "virtual ip allocation conflict, retry")
		return
	}

	networkPeers, err := h.peers.ListNetworkPeers(r.Context(), network.ID)
	if err != nil {
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "register.load_peer_snapshot").
			Str("network_id", network.ID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load peer snapshot")
		return
	}

	activePeers := make([]models.Peer, 0, len(networkPeers))
	for _, candidate := range networkPeers {
		if candidate.ID == registered.ID || candidate.IsOnline {
			activePeers = append(activePeers, candidate)
		}
	}

	writeSuccess(w, http.StatusOK, peerRegistrationResponse{
		VirtualIP:   registered.VirtualIP,
		NetworkCIDR: network.CIDR,
		PeerID:      registered.ID,
		PublicKey:   registered.PublicKey,
		Peers:       activePeers,
	})
}

// ListPeers returns all peers for a network with online/offline state.
func (h *PeerHandler) ListPeers(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networkID := chi.URLParam(r, "id")
	network, err := h.networks.GetNetwork(r.Context(), networkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "list.load_network").
			Str("network_id", networkID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "you do not have access to this network")
		return
	}

	peers, err := h.peers.ListNetworkPeers(r.Context(), network.ID)
	if err != nil {
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "list.load_peers").
			Str("network_id", network.ID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load peers")
		return
	}

	writeSuccess(w, http.StatusOK, peers)
}

type peerHeartbeatRequest struct {
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
	VNCAvailable   bool     `json:"vnc_available"`
	RXBytes        int64    `json:"rx_bytes"`
	TXBytes        int64    `json:"tx_bytes"`
	RelayID        string   `json:"relay_id"`
}

type peerUnregisterRequest struct {
	MachineID string `json:"machine_id"`
}

// UnregisterPeer removes a machine peer record from the network on graceful shutdown.
func (h *PeerHandler) UnregisterPeer(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networkID := chi.URLParam(r, "id")
	network, err := h.networks.GetNetwork(r.Context(), networkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "unregister.load_network").
			Str("network_id", networkID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "you do not have access to this network")
		return
	}

	var req peerUnregisterRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.MachineID = strings.TrimSpace(req.MachineID)
	if req.MachineID == "" {
		writeError(w, http.StatusBadRequest, "machine_id is required")
		return
	}

	if err := h.peers.DeletePeerByMachineID(r.Context(), network.ID, req.MachineID); err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeSuccess(w, http.StatusOK, map[string]any{
				"status":     "already_removed",
				"machine_id": req.MachineID,
			})
			return
		}
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "unregister.delete_peer_by_machine").
			Str("network_id", network.ID).
			Str("user_id", userID).
			Str("machine_id", req.MachineID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to unregister peer")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]any{
		"status":     "unregistered",
		"machine_id": req.MachineID,
	})
}

// Heartbeat updates peer reachability and runtime statistics.
func (h *PeerHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networkID := chi.URLParam(r, "id")
	peerID := chi.URLParam(r, "peerId")

	network, err := h.networks.GetNetwork(r.Context(), networkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "heartbeat.load_network").
			Str("network_id", networkID).
			Str("peer_id", peerID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "you do not have access to this network")
		return
	}

	peer, err := h.peers.GetPeer(r.Context(), peerID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "peer not found")
			return
		}
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "heartbeat.load_peer").
			Str("network_id", network.ID).
			Str("peer_id", peerID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load peer")
		return
	}
	if peer.NetworkID != network.ID {
		writeError(w, http.StatusNotFound, "peer not found in network")
		return
	}

	var req peerHeartbeatRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.peers.UpdatePeerStatus(r.Context(), peerID, queries.PeerStatusUpdate{
		PublicEndpoint: strings.TrimSpace(req.PublicEndpoint),
		LocalEndpoints: req.LocalEndpoints,
		VNCAvailable:   req.VNCAvailable,
		RXBytes:        req.RXBytes,
		TXBytes:        req.TXBytes,
		RelayID:        strings.TrimSpace(req.RelayID),
	}); err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "peer not found")
			return
		}
		log.Error().Err(err).
			Str("handler", "peer").
			Str("operation", "heartbeat.update_status").
			Str("network_id", network.ID).
			Str("peer_id", peerID).
			Str("user_id", userID).
			Msg("peer handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to update peer status")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]string{"status": "updated"})
}
