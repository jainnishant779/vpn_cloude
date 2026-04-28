package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
		relayEndpoint, err := derivePublicRelayEndpoint(r)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
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
			secret = "quicktunnel-default-relay-secret"
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

func derivePublicRelayEndpoint(r *http.Request) (string, error) {
	// 1) Explicit relay endpoint has highest priority.
	if endpoint, ok := normalizeRelayEndpointCandidate(os.Getenv("RELAY_ENDPOINT"), 3478, true); ok {
		return endpoint, nil
	}

	// 2) Public server URL should always map to host:3478.
	if host, ok := normalizePublicHostCandidate(os.Getenv("PUBLIC_SERVER_URL")); ok {
		return net.JoinHostPort(host, "3478"), nil
	}

	// 3) Proxy/request host headers as fallback.
	candidates := []string{
		r.Header.Get("X-Forwarded-Host"),
		r.Header.Get("X-Original-Host"),
		r.Host,
	}
	for _, candidate := range candidates {
		if host, ok := normalizePublicHostCandidate(candidate); ok {
			return net.JoinHostPort(host, "3478"), nil
		}
	}

	return "", fmt.Errorf("relay endpoint unavailable: set PUBLIC_SERVER_URL or RELAY_ENDPOINT to a public host")
}

func normalizeRelayEndpointCandidate(raw string, defaultPort int, keepExplicitPort bool) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	// In case malformed env accidentally concatenates another key.
	if i := strings.Index(raw, "RELAY_ENDPOINT="); i > 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	if i := strings.Index(raw, "PUBLIC_SERVER_URL="); i > 0 {
		raw = strings.TrimSpace(raw[:i])
	}

	// Strip comma-separated forwarded hosts.
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}

	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", false
	}

	// Allow URL-form input.
	if strings.Contains(raw, "://") {
		if parsedHost := hostPortFromURL(raw); parsedHost != "" {
			raw = parsedHost
		} else {
			return "", false
		}
	}

	host, port := splitHostPortWithDefault(raw, defaultPort)
	host = strings.Trim(host, "[]")
	if isInternalRelayHost(host) {
		return "", false
	}
	if !keepExplicitPort {
		port = defaultPort
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), true
}

func normalizePublicHostCandidate(raw string) (string, bool) {
	endpoint, ok := normalizeRelayEndpointCandidate(raw, 3478, false)
	if !ok {
		return "", false
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(host), true
}

func hostPortFromURL(raw string) string {
	u, err := neturlParse(raw)
	if err != nil || u == nil {
		return ""
	}
	return strings.TrimSpace(u.Host)
}

func splitHostPortWithDefault(raw string, defaultPort int) (string, int) {
	host := strings.TrimSpace(raw)
	port := defaultPort

	if h, p, err := net.SplitHostPort(raw); err == nil {
		host = strings.TrimSpace(h)
		if parsed, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
		return host, port
	}

	// Handle simple host:port (non-IPv6) without brackets.
	if strings.Count(raw, ":") == 1 && !strings.HasPrefix(raw, "[") {
		parts := strings.SplitN(raw, ":", 2)
		host = strings.TrimSpace(parts[0])
		if parsed, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
	}
	return host, port
}

func isInternalRelayHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return true
	}
	switch h {
	case "relay", "server", "localhost":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return !strings.Contains(h, ".")
}

// net/url kept behind wrapper to keep helper signatures small and testable.
func neturlParse(raw string) (*url.URL, error) {
	return url.Parse(raw)
}

