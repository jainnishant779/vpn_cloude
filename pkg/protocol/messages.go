package protocol

import "time"

// PeerAnnounce is sent by an agent to advertise reachable endpoints.
type PeerAnnounce struct {
	PeerID    string   `json:"peer_id"`
	NetworkID string   `json:"network_id"`
	PublicKey string   `json:"public_key"`
	Endpoints []string `json:"endpoints"`
}

// PeerInfo is the minimal peer metadata needed for connection setup.
type PeerInfo struct {
	PeerID         string   `json:"peer_id"`
	Name           string   `json:"name"`
	VirtualIP      string   `json:"virtual_ip"`
	PublicKey      string   `json:"public_key"`
	PublicEndpoint string   `json:"public_endpoint,omitempty"`
	LocalEndpoints []string `json:"local_endpoints,omitempty"`
	IsOnline       bool     `json:"is_online"`
	VNCPort        int      `json:"vnc_port"`
	VNCAvailable   bool     `json:"vnc_available"`
}

// PeerList is returned by coordination to describe connectable peers.
type PeerList struct {
	NetworkID   string     `json:"network_id"`
	Peers       []PeerInfo `json:"peers"`
	GeneratedAt time.Time  `json:"generated_at"`
}

// RelayAssignment provides relay details when direct P2P is unavailable.
type RelayAssignment struct {
	RelayHost string    `json:"relay_host"`
	RelayPort int       `json:"relay_port"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TrafficStats provides coarse tunnel telemetry for heartbeat updates.
type TrafficStats struct {
	RXBytes uint64 `json:"rx_bytes"`
	TXBytes uint64 `json:"tx_bytes"`
}

// Heartbeat keeps peer liveness and transport stats fresh.
type Heartbeat struct {
	PeerID    string       `json:"peer_id"`
	Timestamp time.Time    `json:"timestamp"`
	Stats     TrafficStats `json:"stats"`
}

// HandshakeInit starts a peer handshake with replay protection via nonce.
type HandshakeInit struct {
	SessionID string    `json:"session_id"`
	FromPeer  string    `json:"from_peer"`
	ToPeer    string    `json:"to_peer"`
	Nonce     string    `json:"nonce"`
	SentAt    time.Time `json:"sent_at"`
}

// HandshakeResponse acknowledges handshake initiation.
type HandshakeResponse struct {
	SessionID string    `json:"session_id"`
	FromPeer  string    `json:"from_peer"`
	ToPeer    string    `json:"to_peer"`
	Accepted  bool      `json:"accepted"`
	Reason    string    `json:"reason,omitempty"`
	SentAt    time.Time `json:"sent_at"`
}
