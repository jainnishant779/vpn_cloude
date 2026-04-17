package tunnel

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultMTU = 1420

// TunnelStats reports aggregate I/O and handshake activity.
type TunnelStats struct {
	RXBytes       uint64    `json:"rx_bytes"`
	TXBytes       uint64    `json:"tx_bytes"`
	LastHandshake time.Time `json:"last_handshake"`
}

// PeerConfig tracks peer attributes managed by the tunnel.
type PeerConfig struct {
	PublicKey     string
	Endpoint      string
	AllowedIP     string
	AddedAt       time.Time
	LastHandshake time.Time
}

// WGTunnel manages userspace tunnel lifecycle and peer config state.
type WGTunnel struct {
	privateKey string
	virtualIP  string
	cidr       string
	listenPort int
	deviceName string
	mtu        int

	mu      sync.RWMutex
	started bool
	device  TUNDevice
	peers   map[string]*PeerConfig
	stats   TunnelStats
}

// NewWGTunnel prepares a tunnel instance with runtime settings.
func NewWGTunnel(privateKey, virtualIP, cidr string, listenPort int) (*WGTunnel, error) {
	if strings.TrimSpace(privateKey) == "" {
		return nil, fmt.Errorf("new wg tunnel: private key is required")
	}
	if strings.TrimSpace(virtualIP) == "" {
		return nil, fmt.Errorf("new wg tunnel: virtual ip is required")
	}
	if strings.TrimSpace(cidr) == "" {
		return nil, fmt.Errorf("new wg tunnel: cidr is required")
	}
	if listenPort <= 0 || listenPort > 65535 {
		return nil, fmt.Errorf("new wg tunnel: listen port out of range")
	}

	if err := validateWireGuardKey(privateKey); err != nil {
		return nil, fmt.Errorf("new wg tunnel: %w", err)
	}

	return &WGTunnel{
		privateKey: privateKey,
		virtualIP:  virtualIP,
		cidr:       cidr,
		listenPort: listenPort,
		deviceName: "qtun0",
		mtu:        defaultMTU,
		peers:      make(map[string]*PeerConfig),
	}, nil
}

// Start creates and configures the underlying TUN interface.
func (w *WGTunnel) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.started {
		return nil
	}

	device, err := CreateTUN(w.deviceName, w.mtu)
	if err != nil {
		return fmt.Errorf("wg tunnel start: create tun: %w", err)
	}

	if err := device.Configure(w.virtualIP, w.cidr); err != nil {
		_ = device.Close()
		return fmt.Errorf("wg tunnel start: configure tun: %w", err)
	}

	w.device = device
	w.deviceName = device.Name()
	w.started = true
	w.stats.LastHandshake = time.Now().UTC()
	return nil
}

// AddPeer adds or updates a tunnel peer definition.
func (w *WGTunnel) AddPeer(publicKey, endpoint, allowedIP string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return fmt.Errorf("add peer: tunnel is not started")
	}
	if strings.TrimSpace(publicKey) == "" {
		return fmt.Errorf("add peer: public key is required")
	}
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("add peer: endpoint is required")
	}
	if strings.TrimSpace(allowedIP) == "" {
		return fmt.Errorf("add peer: allowed ip is required")
	}

	now := time.Now().UTC()
	w.peers[publicKey] = &PeerConfig{
		PublicKey:     publicKey,
		Endpoint:      endpoint,
		AllowedIP:     allowedIP,
		AddedAt:       now,
		LastHandshake: now,
	}
	w.stats.LastHandshake = now
	return nil
}

// RemovePeer removes peer state from tunnel configuration.
func (w *WGTunnel) RemovePeer(publicKey string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return fmt.Errorf("remove peer: tunnel is not started")
	}
	if strings.TrimSpace(publicKey) == "" {
		return fmt.Errorf("remove peer: public key is required")
	}

	if _, exists := w.peers[publicKey]; !exists {
		return fmt.Errorf("remove peer: peer not found")
	}
	delete(w.peers, publicKey)
	return nil
}

// UpdatePeerEndpoint updates a peer endpoint address.
func (w *WGTunnel) UpdatePeerEndpoint(publicKey, newEndpoint string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return fmt.Errorf("update peer endpoint: tunnel is not started")
	}
	if strings.TrimSpace(publicKey) == "" {
		return fmt.Errorf("update peer endpoint: public key is required")
	}
	if strings.TrimSpace(newEndpoint) == "" {
		return fmt.Errorf("update peer endpoint: new endpoint is required")
	}

	peer, exists := w.peers[publicKey]
	if !exists {
		return fmt.Errorf("update peer endpoint: peer not found")
	}
	peer.Endpoint = newEndpoint
	peer.LastHandshake = time.Now().UTC()
	w.stats.LastHandshake = peer.LastHandshake
	return nil
}

// GetStats returns an immutable snapshot of tunnel counters.
func (w *WGTunnel) GetStats() TunnelStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.stats
}

// RecordRX increments received-byte counter.
func (w *WGTunnel) RecordRX(bytes uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.RXBytes += bytes
}

// RecordTX increments transmitted-byte counter.
func (w *WGTunnel) RecordTX(bytes uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.TXBytes += bytes
}

// Stop tears down tunnel resources.
func (w *WGTunnel) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.started {
		return nil
	}

	if w.device != nil {
		if err := w.device.Close(); err != nil {
			return fmt.Errorf("wg tunnel stop: close tun device: %w", err)
		}
	}

	w.device = nil
	w.peers = make(map[string]*PeerConfig)
	w.started = false
	return nil
}

func validateWireGuardKey(privateKey string) error {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
	if err != nil {
		return fmt.Errorf("validate key: decode base64: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("validate key: expected 32-byte key material")
	}
	return nil
}
