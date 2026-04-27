package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"quicktunnel/server/internal/config"
)

type RelayAssignment struct {
	PeerID        string `json:"peer_id"`
	RelayID       string `json:"relay_id"`
	RelayHost     string `json:"relay_host"`
	RelayPort     int    `json:"relay_port"`
	Token         string `json:"token"`
	Region        string `json:"region"`
	RelayEndpoint string `json:"relay_endpoint"`
	SessionToken  string `json:"session_token"`
	ExpiresAt     int64  `json:"expires_at"`
	NetworkID     string `json:"network_id"`
}

// Handler for GET /api/v1/coord/relay/assign
func RelayAssignHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		networkID := strings.TrimSpace(r.URL.Query().Get("network_id"))
		peerID := strings.TrimSpace(r.URL.Query().Get("peer_id"))
		if peerID == "" {
			http.Error(w, "missing peer_id", http.StatusBadRequest)
			return
		}
		relayEndpoint := os.Getenv("RELAY_ENDPOINT")
		if relayEndpoint == "" {
			relayEndpoint = "relay:3478"
		}
		relayHost := relayEndpoint
		relayPort := 3478
		if host, portStr, err := net.SplitHostPort(relayEndpoint); err == nil {
			relayHost = host
			if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
				relayPort = p
			}
		}
		if networkID == "" {
			networkID = "default"
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
			PeerID:        peerID,
			RelayID:       "relay-default",
			RelayHost:     relayHost,
			RelayPort:     relayPort,
			Token:         token,
			Region:        "default",
			RelayEndpoint: relayEndpoint,
			SessionToken:  token,
			ExpiresAt:     expiresAt,
			NetworkID:     networkID,
		}
		writeSuccess(w, http.StatusOK, resp)
	}
}
