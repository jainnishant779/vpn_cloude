package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/rs/zerolog/log"
	"quicktunnel/server/internal/coordinator"
	"quicktunnel/server/internal/database/queries"
	"quicktunnel/server/internal/models"
)

// joinNetworkReader reads network info for the unauthenticated join flow.
type joinNetworkReader interface {
	GetNetworkByPublicID(ctx context.Context, publicNetworkID string) (*models.Network, error)
}

// joinPeerWriter manages peer records during the join flow.
type joinPeerWriter interface {
	RegisterPendingPeer(ctx context.Context, peer *models.Peer) (*models.Peer, error)
	RegisterPeer(ctx context.Context, peer *models.Peer) (*models.Peer, error)
	GetPeerByPublicKey(ctx context.Context, networkID, publicKey string) (*models.Peer, error)
	ListNetworkPeers(ctx context.Context, networkID string) ([]models.Peer, error)
}

// JoinHandler serves the unauthenticated network join endpoint.
// Devices call POST /api/v1/join with a network_id and WireGuard public key.
// Depending on the network's access_control mode, they are either:
//   - auto-approved with an IP assigned immediately, or
//   - placed in "pending" status until the network admin approves via the dashboard.
type JoinHandler struct {
	networks joinNetworkReader
	peers    joinPeerWriter
}

func NewJoinHandler(networks joinNetworkReader, peers joinPeerWriter) *JoinHandler {
	return &JoinHandler{
		networks: networks,
		peers:    peers,
	}
}

type joinRequest struct {
	NetworkID   string `json:"network_id"`
	Hostname    string `json:"hostname"`
	WGPublicKey string `json:"wg_public_key"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
}

type joinResponse struct {
	MemberID    string `json:"member_id"`
	MemberToken string `json:"member_token"`
	Status      string `json:"status"`
	VirtualIP   string `json:"virtual_ip,omitempty"`
	NetworkCIDR string `json:"network_cidr,omitempty"`
	NetworkName string `json:"network_name"`
	Message     string `json:"message"`
}

// Join handles POST /api/v1/join — the ZeroTier-style join endpoint.
func (h *JoinHandler) Join(w http.ResponseWriter, r *http.Request) {
	var req joinRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.NetworkID = strings.TrimSpace(req.NetworkID)
	req.WGPublicKey = strings.TrimSpace(req.WGPublicKey)
	req.Hostname = strings.TrimSpace(req.Hostname)
	req.OS = strings.TrimSpace(req.OS)
	req.Arch = strings.TrimSpace(req.Arch)

	if req.NetworkID == "" || req.WGPublicKey == "" {
		writeError(w, http.StatusBadRequest, "network_id and wg_public_key are required")
		return
	}
	if req.Hostname == "" {
		req.Hostname = "unknown-device"
	}
	if req.OS == "" {
		req.OS = runtime.GOOS
	}
	if req.Arch == "" {
		req.Arch = runtime.GOARCH
	}

	// Look up the network by public network_id (no auth required)
	network, err := h.networks.GetNetworkByPublicID(r.Context(), req.NetworkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		log.Error().Err(err).Str("network_id", req.NetworkID).Msg("join: failed to look up network")
		writeError(w, http.StatusInternalServerError, "failed to look up network")
		return
	}

	if !network.IsActive {
		writeError(w, http.StatusNotFound, "network not found")
		return
	}

	// Check if this device already joined (by WG public key in this network)
	existing, err := h.peers.GetPeerByPublicKey(r.Context(), network.ID, req.WGPublicKey)
	if err == nil && existing != nil {
		// Device is re-joining — return current status
		resp := joinResponse{
			MemberID:    existing.ID,
			MemberToken: existing.MemberToken,
			Status:      existing.Status,
			NetworkCIDR: network.CIDR,
			NetworkName: network.Name,
		}
		if existing.Status == "approved" {
			resp.VirtualIP = existing.VirtualIP
			resp.Message = "Already approved. Tunnel can be configured."
		} else if existing.Status == "pending" {
			resp.Message = "Waiting for network admin to approve your device."
		} else {
			resp.Message = "Your device was rejected by the network admin."
		}
		writeSuccess(w, http.StatusOK, resp)
		return
	}

	// Determine initial status based on network access control
	accessControl := network.AccessControl
	if accessControl == "" {
		accessControl = "approve"
	}

	// Use machine_id derived from public key for uniqueness
	machineID := "join_" + req.WGPublicKey[:12]

	if accessControl == "auto" {
		// Auto-approve: allocate IP and register immediately
		currentPeers, err := h.peers.ListNetworkPeers(r.Context(), network.ID)
		if err != nil {
			log.Error().Err(err).Msg("join: failed to list peers for IP allocation")
			writeError(w, http.StatusInternalServerError, "failed to allocate IP")
			return
		}

		usedIPs := make([]string, 0, len(currentPeers))
		for _, p := range currentPeers {
			if p.VirtualIP != "" && p.VirtualIP != "0.0.0.0" {
				usedIPs = append(usedIPs, p.VirtualIP)
			}
		}

		virtualIP, err := coordinator.AllocateIP(network.CIDR, usedIPs)
		if err != nil {
			writeError(w, http.StatusConflict, "no available IP addresses in network")
			return
		}

		registered, err := h.peers.RegisterPeer(r.Context(), &models.Peer{
			NetworkID:      network.ID,
			Name:           req.Hostname,
			MachineID:      machineID,
			PublicKey:      req.WGPublicKey,
			VirtualIP:      virtualIP,
			LocalEndpoints: []string{},
			OS:             req.OS,
			Version:        req.Arch,
			VNCPort:        5900,
			Status:         "approved",
		})
		if err != nil {
			log.Error().Err(err).Msg("join: failed to register peer (auto-approve)")
			writeError(w, http.StatusInternalServerError, "failed to register peer")
			return
		}

		writeSuccess(w, http.StatusCreated, joinResponse{
			MemberID:    registered.ID,
			MemberToken: registered.MemberToken,
			Status:      "approved",
			VirtualIP:   virtualIP,
			NetworkCIDR: network.CIDR,
			NetworkName: network.Name,
			Message:     "Approved! Tunnel is being configured.",
		})
		return
	}

	// Manual approval mode — register as pending
	pending, err := h.peers.RegisterPendingPeer(r.Context(), &models.Peer{
		NetworkID:      network.ID,
		Name:           req.Hostname,
		MachineID:      machineID,
		PublicKey:      req.WGPublicKey,
		VirtualIP:      "",
		LocalEndpoints: []string{},
		OS:             req.OS,
		Version:        req.Arch,
		VNCPort:        5900,
		Status:         "pending",
	})
	if err != nil {
		log.Error().Err(err).Msg("join: failed to register pending peer")
		writeError(w, http.StatusInternalServerError, "failed to register pending peer")
		return
	}

	writeSuccess(w, http.StatusCreated, joinResponse{
		MemberID:    pending.ID,
		MemberToken: pending.MemberToken,
		Status:      "pending",
		NetworkCIDR: network.CIDR,
		NetworkName: network.Name,
		Message:     "Your device is pending approval. Ask the network admin to approve you in the dashboard.",
	})
}

// GetServerURL returns the public server URL for install/join commands.
func GetServerURL() string {
	if url := os.Getenv("PUBLIC_SERVER_URL"); url != "" {
		return url
	}
	return ""
}
