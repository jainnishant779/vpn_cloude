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
	GetOnlinePeers(ctx context.Context, networkID string) ([]models.Peer, error)
	ApprovePeer(ctx context.Context, peerID, virtualIP string) (*models.Peer, error)
	RejectPeer(ctx context.Context, peerID string) error
	DeletePeer(ctx context.Context, peerID string) error
	UpdatePeerStatus(ctx context.Context, peerID string, status queries.PeerStatusUpdate) error
}

// MemberHandler serves member management endpoints for the dashboard.
type MemberHandler struct {
	networks memberNetworkReader
	peers    memberPeerManager
}

type memberHeartbeatRequest struct {
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
	VNCAvailable   bool     `json:"vnc_available"`
	RXBytes        int64    `json:"rx_bytes"`
	TXBytes        int64    `json:"tx_bytes"`
	RelayID        string   `json:"relay_id"`
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
	token := readBearerToken(r)
	peer, err := h.authenticateMember(r.Context(), memberID, token)
	if err != nil {
		h.writeMemberAuthError(w, err)
		return
	}

	network, err := h.networks.GetNetwork(r.Context(), peer.NetworkID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load network")
		return
	}

	resp := map[string]string{
		"status":       peer.Status,
		"member_id":    peer.ID,
		"network_id":   network.NetworkID,
		"network_name": network.Name,
		"network_cidr": network.CIDR,
	}
	if peer.Status == "approved" && peer.VirtualIP != "" && peer.VirtualIP != "0.0.0.0" {
		resp["virtual_ip"] = peer.VirtualIP
	}

	writeSuccess(w, http.StatusOK, resp)
}

// MemberHeartbeat updates device heartbeat using member-token auth.
// PUT /api/v1/members/{mid}/heartbeat
func (h *MemberHandler) MemberHeartbeat(w http.ResponseWriter, r *http.Request) {
	memberID := chi.URLParam(r, "mid")
	token := readBearerToken(r)
	peer, err := h.authenticateMember(r.Context(), memberID, token)
	if err != nil {
		h.writeMemberAuthError(w, err)
		return
	}
	if peer.Status != "approved" {
		writeError(w, http.StatusConflict, "member is not approved")
		return
	}

	var req memberHeartbeatRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.peers.UpdatePeerStatus(r.Context(), peer.ID, queries.PeerStatusUpdate{
		PublicEndpoint: strings.TrimSpace(req.PublicEndpoint),
		LocalEndpoints: req.LocalEndpoints,
		VNCAvailable:   req.VNCAvailable,
		RXBytes:        req.RXBytes,
		TXBytes:        req.TXBytes,
		RelayID:        strings.TrimSpace(req.RelayID),
	}); err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update member status")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]string{"status": "updated"})
}

// MemberAnnounce updates device endpoints using member-token auth.
// POST /api/v1/members/{mid}/announce
func (h *MemberHandler) MemberAnnounce(w http.ResponseWriter, r *http.Request) {
	memberID := chi.URLParam(r, "mid")
	token := readBearerToken(r)
	peer, err := h.authenticateMember(r.Context(), memberID, token)
	if err != nil {
		h.writeMemberAuthError(w, err)
		return
	}
	if peer.Status != "approved" {
		writeError(w, http.StatusConflict, "member is not approved")
		return
	}

	var req memberHeartbeatRequest // Uses the same endpoint structure
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.peers.UpdatePeerStatus(r.Context(), peer.ID, queries.PeerStatusUpdate{
		PublicEndpoint: strings.TrimSpace(req.PublicEndpoint),
		LocalEndpoints: req.LocalEndpoints,
	}); err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			writeError(w, http.StatusNotFound, "member not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to update member endpoints")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]string{"status": "announced"})
}

// MemberPeers returns online approved peers for the same network.
// GET /api/v1/members/{mid}/peers
func (h *MemberHandler) MemberPeers(w http.ResponseWriter, r *http.Request) {
	memberID := chi.URLParam(r, "mid")
	token := readBearerToken(r)
	peer, err := h.authenticateMember(r.Context(), memberID, token)
	if err != nil {
		h.writeMemberAuthError(w, err)
		return
	}
	if peer.Status != "approved" {
		writeError(w, http.StatusConflict, "member is not approved")
		return
	}

	peers, err := h.peers.GetOnlinePeers(r.Context(), peer.NetworkID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list peers")
		return
	}

	filtered := make([]models.Peer, 0, len(peers))
	for _, candidate := range peers {
		if candidate.ID == peer.ID {
			continue
		}
		if candidate.Status != "approved" {
			continue
		}
		candidate.MemberToken = ""
		filtered = append(filtered, candidate)
	}

	writeSuccess(w, http.StatusOK, filtered)
}

var (
	errMissingMemberToken = errors.New("missing member token")
	errInvalidMemberToken = errors.New("invalid member token")
	errMemberMismatch     = errors.New("token does not match member")
)

func readBearerToken(r *http.Request) string {
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	return token
}

func (h *MemberHandler) authenticateMember(ctx context.Context, memberID, token string) (*models.Peer, error) {
	memberID = strings.TrimSpace(memberID)
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errMissingMemberToken
	}

	peer, err := h.peers.GetPeerByMemberToken(ctx, token)
	if err != nil {
		if errors.Is(err, queries.ErrNotFound) {
			return nil, errInvalidMemberToken
		}
		return nil, err
	}
	if peer.ID != memberID {
		return nil, errMemberMismatch
	}
	return peer, nil
}

func (h *MemberHandler) writeMemberAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMissingMemberToken):
		writeError(w, http.StatusUnauthorized, "member_token is required")
	case errors.Is(err, errInvalidMemberToken):
		writeError(w, http.StatusUnauthorized, "invalid member token")
	case errors.Is(err, errMemberMismatch):
		writeError(w, http.StatusForbidden, "token does not match member")
	default:
		writeError(w, http.StatusInternalServerError, "failed to verify token")
	}
}
