package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"quicktunnel/server/internal/auth"
	"quicktunnel/server/internal/coordinator"
	"quicktunnel/server/internal/database/queries"
	"quicktunnel/server/internal/models"
)

// memberNetworkReader reads network data for ownership checks.
type memberNetworkReader interface {
	GetNetwork(ctx context.Context, networkID string) (*models.Network, error)
}

// memberPeerManager manages peer records for approve/reject/kick.
type memberPeerManager interface {
	GetPeer(ctx context.Context, peerID string) (*models.Peer, error)
	GetPeerByMemberToken(ctx context.Context, token string) (*models.Peer, error)
	ListMembers(ctx context.Context, networkID string) ([]models.Peer, error)
	ListNetworkPeers(ctx context.Context, networkID string) ([]models.Peer, error)
	ApprovePeer(ctx context.Context, peerID, virtualIP string) (*models.Peer, error)
	RejectPeer(ctx context.Context, peerID string) error
	DeletePeer(ctx context.Context, peerID string) error
}

// MemberHandler serves member management endpoints for the dashboard.
type MemberHandler struct {
	networks memberNetworkReader
	peers    memberPeerManager
}

func NewMemberHandler(networks memberNetworkReader, peers memberPeerManager) *MemberHandler {
	return &MemberHandler{
		networks: networks,
		peers:    peers,
	}
}

// ListMembers returns all members (pending + approved) for a network.
// GET /api/v1/networks/{id}/members
func (h *MemberHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "not your network")
		return
	}

	members, err := h.peers.ListMembers(r.Context(), network.ID)
	if err != nil {
		log.Error().Err(err).Str("network_id", network.ID).Msg("member: list members failed")
		writeError(w, http.StatusInternalServerError, "failed to list members")
		return
	}

	// Sanitize: don't expose member_token to the dashboard
	for i := range members {
		members[i].MemberToken = ""
	}

	writeSuccess(w, http.StatusOK, members)
}

// ApproveMember approves a pending member and assigns a virtual IP.
// POST /api/v1/networks/{id}/members/{mid}/approve
func (h *MemberHandler) ApproveMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networkID := chi.URLParam(r, "id")
	memberID := chi.URLParam(r, "mid")

	network, err := h.networks.GetNetwork(r.Context(), networkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "not your network")
		return
	}

	member, err := h.peers.GetPeer(r.Context(), memberID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}
	if member.NetworkID != network.ID {
		writeError(w, http.StatusNotFound, "member not found in this network")
		return
	}

	if member.Status == "approved" {
		member.MemberToken = ""
		writeSuccess(w, http.StatusOK, member)
		return
	}

	// Allocate an IP
	currentPeers, err := h.peers.ListNetworkPeers(r.Context(), network.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list peers for IP allocation")
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

	approved, err := h.peers.ApprovePeer(r.Context(), memberID, virtualIP)
	if err != nil {
		log.Error().Err(err).Str("member_id", memberID).Msg("member: approve failed")
		writeError(w, http.StatusInternalServerError, "failed to approve member")
		return
	}

	approved.MemberToken = ""
	writeSuccess(w, http.StatusOK, approved)
}

// RejectMember rejects a pending member.
// POST /api/v1/networks/{id}/members/{mid}/reject
func (h *MemberHandler) RejectMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networkID := chi.URLParam(r, "id")
	memberID := chi.URLParam(r, "mid")

	network, err := h.networks.GetNetwork(r.Context(), networkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "not your network")
		return
	}

	if err := h.peers.RejectPeer(r.Context(), memberID); err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to reject member")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// KickMember removes an approved member from the network.
// DELETE /api/v1/networks/{id}/members/{mid}
func (h *MemberHandler) KickMember(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing authenticated user")
		return
	}

	networkID := chi.URLParam(r, "id")
	memberID := chi.URLParam(r, "mid")

	network, err := h.networks.GetNetwork(r.Context(), networkID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "network not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}
	if network.OwnerID != userID {
		writeError(w, http.StatusForbidden, "not your network")
		return
	}

	member, err := h.peers.GetPeer(r.Context(), memberID)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load member")
		return
	}
	if member.NetworkID != network.ID {
		writeError(w, http.StatusNotFound, "member not found in this network")
		return
	}

	if err := h.peers.DeletePeer(r.Context(), memberID); err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to remove member")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]string{"status": "removed"})
}

// MemberStatus returns the current status of a member.
// Called by the CLI client when polling for approval.
// GET /api/v1/members/{mid}/status
// Auth: member_token in Authorization header.
func (h *MemberHandler) MemberStatus(w http.ResponseWriter, r *http.Request) {
	memberID := chi.URLParam(r, "mid")

	// Authenticate via member_token
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	token := strings.TrimPrefix(authHeader, "Bearer ")
	token = strings.TrimSpace(token)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "member_token is required")
		return
	}

	// Verify the token belongs to this member
	peer, err := h.peers.GetPeerByMemberToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid member token")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to verify token")
		return
	}
	if peer.ID != memberID {
		writeError(w, http.StatusForbidden, "token does not match member")
		return
	}

	resp := map[string]string{
		"status": peer.Status,
	}
	if peer.Status == "approved" && peer.VirtualIP != "" && peer.VirtualIP != "0.0.0.0" {
		resp["virtual_ip"] = peer.VirtualIP
	}

	writeSuccess(w, http.StatusOK, resp)
}
