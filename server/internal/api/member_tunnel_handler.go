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
// Merges DB peer data with fresh Redis announce data so that clients
// receive up-to-date public_endpoint and local_endpoints for each peer.
// Without this merge, same-NAT LAN detection cannot work because the
// DB only stores the last heartbeat endpoint, not the latest announce.
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

	// Build map for Redis merge
	peerByID := make(map[string]models.Peer, len(peers))
	for _, p := range peers {
		peerByID[p.ID] = p
	}

	// Merge Redis announce data (freshest local_endpoints + public_endpoint)
	if h.redis != nil {
		indexKey := fmt.Sprintf("coord:network:%s:peers", peer.NetworkID)
		peerIDs, redisErr := h.redis.SMembers(r.Context(), indexKey).Result()
		if redisErr == nil {
			for _, peerID := range peerIDs {
				entryKey := fmt.Sprintf("coord:announce:%s:%s", peer.NetworkID, peerID)
				raw, getErr := h.redis.Get(r.Context(), entryKey).Result()
				if getErr != nil {
					continue
				}
				var entry struct {
					PeerID         string   `json:"peer_id"`
					PublicEndpoint string   `json:"public_endpoint"`
					LocalEndpoints []string `json:"local_endpoints"`
				}
				if json.Unmarshal([]byte(raw), &entry) != nil {
					continue
				}
				if p, exists := peerByID[entry.PeerID]; exists {
					if entry.PublicEndpoint != "" {
						p.PublicEndpoint = entry.PublicEndpoint
					}
					if len(entry.LocalEndpoints) > 0 {
						p.LocalEndpoints = entry.LocalEndpoints
					}
					peerByID[entry.PeerID] = p
				}
			}
		}
	}

	result := make([]models.Peer, 0, len(peerByID))
	for _, p := range peerByID {
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

// Offline — POST /api/v1/members/{mid}/offline
// Called by the client agent on graceful shutdown to immediately remove peer.
func (h *MemberTunnelHandler) Offline(w http.ResponseWriter, r *http.Request) {
	peer := h.authMember(w, r)
	if peer == nil {
		return
	}

	// Mark peer offline in database
	if err := h.peers.UpdatePeerStatus(r.Context(), peer.ID, queries.PeerStatusUpdate{
		PublicEndpoint: "",
		LocalEndpoints: []string{},
	}); err != nil {
		log.Warn().Err(err).Str("peer_id", peer.ID).Msg("offline: update status failed")
	}

	// Also mark is_online = false explicitly
	if marker, ok := h.peers.(interface {
		MarkPeerOffline(ctx context.Context, peerID string) error
	}); ok {
		_ = marker.MarkPeerOffline(r.Context(), peer.ID)
	}

	// Clean up Redis coordination entry
	if h.redis != nil {
		entryKey := fmt.Sprintf("coord:announce:%s:%s", peer.NetworkID, peer.ID)
		indexKey := fmt.Sprintf("coord:network:%s:peers", peer.NetworkID)
		pipe := h.redis.TxPipeline()
		pipe.Del(r.Context(), entryKey)
		pipe.SRem(r.Context(), indexKey, peer.ID)
		if _, err := pipe.Exec(r.Context()); err != nil {
			log.Warn().Err(err).Str("peer_id", peer.ID).Msg("offline: redis cleanup failed")
		}
	}

	log.Info().Str("peer_id", peer.ID).Str("name", peer.Name).Msg("peer went offline gracefully")
	writeSuccess(w, http.StatusOK, map[string]string{"status": "offline"})
}