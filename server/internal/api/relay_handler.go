package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"quicktunnel/server/internal/config"
)

type RelayAssignment struct {
	RelayEndpoint string `json:"relay_endpoint"`
	SessionToken  string `json:"session_token"`
	ExpiresAt     int64  `json:"expires_at"`
	NetworkID     string `json:"network_id"`
}

// Handler for GET /api/v1/coord/relay/assign
func RelayAssignHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		networkID := r.URL.Query().Get("network_id")
		peerID := r.URL.Query().Get("peer_id")
		if networkID == "" || peerID == "" {
			http.Error(w, "missing network_id or peer_id", http.StatusBadRequest)
			return
		}
		relayEndpoint := os.Getenv("RELAY_ENDPOINT")
		if relayEndpoint == "" {
			relayEndpoint = "relay:3478" // fallback
		}
		expiresAt := time.Now().Add(5 * time.Minute).Unix()
		secret := cfg.RelaySessionSecret
		if secret == "" {
			http.Error(w, "relay session secret not set", http.StatusInternalServerError)
			return
		}
		msg := networkID + ":" + peerID + ":" + strconv.FormatInt(expiresAt, 10)
		h := hmac.New(sha256.New, []byte(secret))
		h.Write([]byte(msg))
		token := hex.EncodeToString(h.Sum(nil))
		resp := RelayAssignment{
			RelayEndpoint: relayEndpoint,
			SessionToken:  token,
			ExpiresAt:     expiresAt,
			NetworkID:     networkID,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}
