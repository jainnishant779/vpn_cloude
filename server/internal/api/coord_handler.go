package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"quicktunnel/server/internal/models"
)

type coordPeerStore interface {
	GetOnlinePeers(ctx context.Context, networkID string) ([]models.Peer, error)
}

type coordRelayStore interface {
	GetNearestRelay(ctx context.Context, latitude, longitude float64) (*models.RelayServer, error)
}

// CoordHandler serves fast endpoint discovery and relay assignment.
type CoordHandler struct {
	redis  *redis.Client
	peers  coordPeerStore
	relays coordRelayStore
}

func NewCoordHandler(redisClient *redis.Client, peers coordPeerStore, relays coordRelayStore) *CoordHandler {
	return &CoordHandler{
		redis:  redisClient,
		peers:  peers,
		relays: relays,
	}
}

type announceRequest struct {
	PeerID         string   `json:"peer_id"`
	NetworkID      string   `json:"network_id"`
	PublicEndpoint string   `json:"public_endpoint"`
	LocalEndpoints []string `json:"local_endpoints"`
}

type coordEntry struct {
	PeerID         string    `json:"peer_id"`
	NetworkID      string    `json:"network_id"`
	PublicEndpoint string    `json:"public_endpoint"`
	LocalEndpoints []string  `json:"local_endpoints"`
	AnnouncedAt    time.Time `json:"announced_at"`
}

// Announce stores latest peer endpoints in Redis with short TTL.
func (h *CoordHandler) Announce(w http.ResponseWriter, r *http.Request) {
	if h.redis == nil {
		writeError(w, http.StatusServiceUnavailable, "coordination cache unavailable")
		return
	}

	var req announceRequest
	if err := decodeJSONBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.PeerID = strings.TrimSpace(req.PeerID)
	req.NetworkID = strings.TrimSpace(req.NetworkID)
	req.PublicEndpoint = strings.TrimSpace(req.PublicEndpoint)
	if req.PeerID == "" || req.NetworkID == "" || req.PublicEndpoint == "" {
		writeError(w, http.StatusBadRequest, "peer_id, network_id, and public_endpoint are required")
		return
	}

	entry := coordEntry{
		PeerID:         req.PeerID,
		NetworkID:      req.NetworkID,
		PublicEndpoint: req.PublicEndpoint,
		LocalEndpoints: req.LocalEndpoints,
		AnnouncedAt:    time.Now().UTC(),
	}

	payload, err := json.Marshal(entry)
	if err != nil {
		log.Error().Err(err).
			Str("handler", "coord").
			Str("operation", "announce.marshal").
			Str("network_id", req.NetworkID).
			Str("peer_id", req.PeerID).
			Msg("coord handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to marshal endpoint")
		return
	}

	entryKey := fmt.Sprintf("coord:announce:%s:%s", req.NetworkID, req.PeerID)
	indexKey := fmt.Sprintf("coord:network:%s:peers", req.NetworkID)

	pipe := h.redis.TxPipeline()
	pipe.Set(r.Context(), entryKey, payload, 60*time.Second)
	pipe.SAdd(r.Context(), indexKey, req.PeerID)
	pipe.Expire(r.Context(), indexKey, 70*time.Second)
	if _, err := pipe.Exec(r.Context()); err != nil {
		log.Error().Err(err).
			Str("handler", "coord").
			Str("operation", "announce.store").
			Str("network_id", req.NetworkID).
			Str("peer_id", req.PeerID).
			Msg("coord handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to store endpoint")
		return
	}

	writeSuccess(w, http.StatusOK, map[string]string{"status": "announced"})
}

// ListPeers returns online peers with freshest endpoint data from Redis.
func (h *CoordHandler) ListPeers(w http.ResponseWriter, r *http.Request) {
	if h.redis == nil {
		writeError(w, http.StatusServiceUnavailable, "coordination cache unavailable")
		return
	}

	networkID := strings.TrimSpace(chi.URLParam(r, "networkId"))
	if networkID == "" {
		writeError(w, http.StatusBadRequest, "network id is required")
		return
	}

	onlinePeers, err := h.peers.GetOnlinePeers(r.Context(), networkID)
	if err != nil {
		log.Error().Err(err).
			Str("handler", "coord").
			Str("operation", "list.load_online_peers").
			Str("network_id", networkID).
			Msg("coord handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to load peers")
		return
	}

	peerByID := make(map[string]models.Peer, len(onlinePeers))
	for _, peer := range onlinePeers {
		peerByID[peer.ID] = peer
	}

	indexKey := fmt.Sprintf("coord:network:%s:peers", networkID)
	peerIDs, err := h.redis.SMembers(r.Context(), indexKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		log.Error().Err(err).
			Str("handler", "coord").
			Str("operation", "list.read_index").
			Str("network_id", networkID).
			Msg("coord handler operation failed")
		writeError(w, http.StatusInternalServerError, "failed to read endpoint index")
		return
	}

	for _, peerID := range peerIDs {
		entryKey := fmt.Sprintf("coord:announce:%s:%s", networkID, peerID)
		raw, err := h.redis.Get(r.Context(), entryKey).Result()
		if errors.Is(err, redis.Nil) {
			_ = h.redis.SRem(r.Context(), indexKey, peerID).Err()
			continue
		}
		if err != nil {
			continue
		}

		var entry coordEntry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			continue
		}

		peer, exists := peerByID[entry.PeerID]
		if !exists {
			continue
		}
		peer.PublicEndpoint = entry.PublicEndpoint
		peer.LocalEndpoints = entry.LocalEndpoints
		peerByID[entry.PeerID] = peer
	}

	excludePeerID := strings.TrimSpace(r.URL.Query().Get("peer_id"))
	items := make([]models.Peer, 0, len(peerByID))
	for _, peer := range peerByID {
		if excludePeerID != "" && peer.ID == excludePeerID {
			continue
		}
		items = append(items, peer)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	writeSuccess(w, http.StatusOK, items)
}

// AssignRelay returns a healthy relay candidate and short-lived session token.
func (h *CoordHandler) AssignRelay(w http.ResponseWriter, r *http.Request) {
	       // Deprecated: use RelayAssignHandler
	       writeError(w, http.StatusNotImplemented, "use new relay assign handler")
}

func generateRelayToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate relay token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}