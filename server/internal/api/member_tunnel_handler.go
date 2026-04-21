package api

// member_tunnel_handler.go
//
// Member-token-authenticated tunnel endpoints for ZeroTier-style peers.
// Called by quicktunnel client after join, using member_token (no API key needed).
//
// Routes (added in router.go):
//   PUT  /api/v1/members/{mid}/heartbeat
//   GET  /api/v1/members/{mid}/peers
//   POST /api/v1/members/{mid}/announce

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"quicktunnel/server/internal/database/queries"
	"quicktunnel/server/internal/models"
)

type memberTunnelPeerStore interface {
	GetPeerByMemberToken(ctx context.Context, token string) (*models.Peer, error)
	UpdatePeerStatus(ctx context.Context, peerID string, status queries.PeerStatusUpdate) error
	GetOnlinePeers(ctx context.Context, networkID string) ([]models.Peer, error)
}

// MemberTunnelHandler serves member-token-authenticated tunnel endpoints.
type MemberTunnelHandler struct {
	peers memberTunnelPeerStore
	redis *redis.Client
}

func NewMemberTunnelHandler(peers memberTunnelPeerStore, redisClient *redis.Client) *MemberTunnelHandler {
	return &MemberTunnelHandler{peers: peers, redis: redisClient}
}

// authMember validates the Bearer member_token and returns the peer.
func (h *MemberTunnelHandler) authMember(w http.ResponseWriter, r *http.Request) *models.Peer {
	memberID := strings.TrimSpace(chi.URLParam(r, "mid"))
	token := strings.TrimSpace(strings.TrimPrefix(
		strings.TrimSpace(r.Header.Get("Authorization")), "Bearer "))
	if token == "" {
		writeError(w, http.StatusUnauthorized, "member_token required")
		return nil
	}
	peer, err := h.peers.GetPeerByMemberToken(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid member token")
		return nil
	}
	if memberID != "" && peer.ID != memberID {
		writeError(w, http.StatusForbidden, "token does not match member")
		return nil
	}
	return peer
}

// Heartbeat — PUT /api/v1/members/{mid}/heartbeat
func (h *MemberTunnelHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	peer := h.authMember(w, r)
	if peer == nil {
		return
	}
	var req struct {
		PublicEndpoint string   `json:"public_endpoint"`
		LocalEndpoints []string `json:"local_endpoints"`
		VNCAvailable   bool     `json:"vnc_available"`
		RXBytes        int64    `json:"rx_bytes"`
		TXBytes        int64    `json:"tx_bytes"`
	}
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
	}); err != nil {
		log.Error().Err(err).Str("peer_id", peer.ID).Msg("member heartbeat failed")
		writeError(w, http.StatusInternalServerError, "failed to update status")
		return
	}
	writeSuccess(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Peers — GET /api/v1/members/{mid}/peers
// Returns all other online+approved peers in same network.
func (h *MemberTunnelHandler) Peers(w http.ResponseWriter, r *http.Request) {
	peer := h.authMember(w, r)
	if peer == nil {
		return
	}
	peers, err := h.peers.GetOnlinePeers(r.Context(), peer.NetworkID)
	if err != nil {
		log.Error().Err(err).Str("peer_id", peer.ID).Msg("member peers list failed")
		writeError(w, http.StatusInternalServerError, "failed to list peers")
		return
	}
	result := make([]models.Peer, 0, len(peers))
	for _, p := range peers {
		if p.ID == peer.ID {
			continue // exclude self
		}
		p.MemberToken = "" // never expose other peers' tokens
		result = append(result, p)
	}
	writeSuccess(w, http.StatusOK, result)
}

// Announce — POST /api/v1/members/{mid}/announce
// Publishes WireGuard endpoint to Redis (same format as CoordHandler).
func (h *MemberTunnelHandler) Announce(w http.ResponseWriter, r *http.Request) {
	peer := h.authMember(w, r)
	if peer == nil {
		return
	}
	var req struct {
		PublicEndpoint string   `json:"public_endpoint"`
		LocalEndpoints []string `json:"local_endpoints"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if h.redis != nil && strings.TrimSpace(req.PublicEndpoint) != "" {
		type coordEntry struct {
			PeerID         string    `json:"peer_id"`
			NetworkID      string    `json:"network_id"`
			PublicEndpoint string    `json:"public_endpoint"`
			LocalEndpoints []string  `json:"local_endpoints"`
			AnnouncedAt    time.Time `json:"announced_at"`
		}
		entry := coordEntry{
			PeerID:         peer.ID,
			NetworkID:      peer.NetworkID,
			PublicEndpoint: req.PublicEndpoint,
			LocalEndpoints: req.LocalEndpoints,
			AnnouncedAt:    time.Now().UTC(),
		}
		payload, _ := json.Marshal(entry)
		entryKey := fmt.Sprintf("coord:announce:%s:%s", peer.NetworkID, peer.ID)
		indexKey := fmt.Sprintf("coord:network:%s:peers", peer.NetworkID)
		pipe := h.redis.TxPipeline()
		pipe.Set(r.Context(), entryKey, payload, 60*time.Second)
		pipe.SAdd(r.Context(), indexKey, peer.ID)
		pipe.Expire(r.Context(), indexKey, 70*time.Second)
		if _, err := pipe.Exec(r.Context()); err != nil {
			log.Warn().Err(err).Str("peer_id", peer.ID).Msg("member announce redis failed")
		}
	}
	writeSuccess(w, http.StatusOK, map[string]string{"status": "announced"})
}