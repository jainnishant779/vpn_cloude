package api

import (
	"net"
	"net/http"
	"strings"
)

// QuickConnectBootstrap stores reusable simple-mode credentials for launchers.
type QuickConnectBootstrap struct {
	Enabled     bool   `json:"enabled"`
	OwnerEmail  string `json:"owner_email"`
	NetworkID   string `json:"network_id"`
	NetworkName string `json:"network_name"`
	APIKey      string `json:"api_key"`
	CIDR        string `json:"cidr"`
	MaxPeers    int    `json:"max_peers"`
}

// QuickConnectHandler exposes local quick-connect bootstrap details.
type QuickConnectHandler struct {
	bootstrap *QuickConnectBootstrap
}

func NewQuickConnectHandler(bootstrap *QuickConnectBootstrap) *QuickConnectHandler {
	return &QuickConnectHandler{bootstrap: bootstrap}
}

func (h *QuickConnectHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeError(w, http.StatusForbidden, "quick connect endpoint is available only from localhost")
		return
	}

	if h.bootstrap == nil || !h.bootstrap.Enabled {
		writeError(w, http.StatusServiceUnavailable, "quick connect bootstrap is disabled")
		return
	}

	writeSuccess(w, http.StatusOK, h.bootstrap)
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	return ip.IsLoopback()
}
