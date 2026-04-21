package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type MemberTunnelHandler struct {
	peers memberTunnelPeerStore
	redis *redis.Client
}

func NewMemberTunnelHandler(peers memberTunnelPeerStore, redisClient *redis.Client) *MemberTunnelHandler {
	return &MemberTunnelHandler{peers: peers, redis: redisClient}
}

// decodeJSONBodyLoose decodes JSON without rejecting unknown fields.
// Used for member endpoints where the client may send extra fields.
func decodeJSONBodyLoose(r *http.Request, dest any) error {
	if r.Body == nil {
		return fmt.Errorf("request body is required")
	}
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dest)
}

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
	if err := decodeJSONBodyLoose(r, &req); err != nil {
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
			continue
		}
		p.MemberToken = ""
		result = append(result, p)
	}
	writeSuccess(w, http.StatusOK, result)
}

// Announce — POST /api/v1/members/{mid}/announce
func (h *MemberTunnelHandler) Announce(w http.ResponseWriter, r *http.Request) {
	peer := h.authMember(w, r)
	if peer == nil {
		return
	}
	var req struct {
		PublicEndpoint string   `json:"public_endpoint"`
		LocalEndpoints []string `json:"local_endpoints"`
	}
	// Use loose decoder — ignore unknown fields from client
	if err := decodeJSONBodyLoose(r, &req); err != nil {
		// Even if decode fails, don't hard-fail — just skip Redis update
		log.Warn().Err(err).Str("peer_id", peer.ID).Msg("announce: decode failed, skipping")
		writeSuccess(w, http.StatusOK, map[string]string{"status": "skipped"})
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
			log.Warn().Err(err).Str("peer_id", peer.ID).Msg("announce: redis write failed")
		}
	}
	writeSuccess(w, http.StatusOK, map[string]string{"status": "announced"})
}