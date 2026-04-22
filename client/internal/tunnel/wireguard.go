package tunnel

import (
	"encoding/base64"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const defaultMTU = 1420

type TunnelStats struct {
	RXBytes       uint64    `json:"rx_bytes"`
	TXBytes       uint64    `json:"tx_bytes"`
	LastHandshake time.Time `json:"last_handshake"`
}

type PeerConfig struct {
	PublicKey     string
	Endpoint      string
	AllowedIP     string
	AddedAt       time.Time
	LastHandshake time.Time
}

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

	// ── Real WireGuard setup ──────────────────────────────────────────────────
	// Set private key + listen port via wg command
	privKeyCmd := fmt.Sprintf("echo '%s' | wg set %s listen-port %d private-key /dev/stdin",
		w.privateKey, w.deviceName, w.listenPort)
	if out, err := exec.Command("sh", "-c", privKeyCmd).CombinedOutput(); err != nil {
		// wg not available — fall back to raw TUN (ping works, TCP limited)
		_ = out
	}

	// Enable IP forwarding + add route for VPN subnet
	_ = exec.Command("sh", "-c", "sysctl -w net.ipv4.ip_forward=1").Run()
	maskBits, _ := maskBitsFromCIDR(w.cidr)
	routeCmd := fmt.Sprintf("ip route replace %s/%d dev %s 2>/dev/null || true",
		networkAddr(w.virtualIP, maskBits), maskBits, w.deviceName)
	_ = exec.Command("sh", "-c", routeCmd).Run()

	w.started = true
	w.stats.LastHandshake = time.Now().UTC()
	return nil
}

// networkAddr returns the network address for a given IP and mask bits.
func networkAddr(ip string, maskBits int) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return ip
	}
	mask := net.CIDRMask(maskBits, 32)
	return parsed.Mask(mask).String()
}

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

	// ── Real wg peer config ───────────────────────────────────────────────────
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}
	cmd := fmt.Sprintf(
		"wg set %s peer %s endpoint %s allowed-ips %s persistent-keepalive 25",
		w.deviceName, publicKey, endpoint, allowedIP,
	)
	_ = exec.Command("sh", "-c", cmd).Run()

	// Add host route so OS sends packets through the tunnel
	peerIP := strings.Split(allowedIP, "/")[0]
	routeCmd := fmt.Sprintf("ip route replace %s dev %s 2>/dev/null || true", peerIP, w.deviceName)
	_ = exec.Command("sh", "-c", routeCmd).Run()

	return nil
}

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

	// Remove from OS WireGuard
	cmd := fmt.Sprintf("wg set %s peer %s remove", w.deviceName, publicKey)
	_ = exec.Command("sh", "-c", cmd).Run()

	delete(w.peers, publicKey)
	return nil
}

func (w *WGTunnel) UpdatePeerEndpoint(publicKey, newEndpoint string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		return fmt.Errorf("update peer endpoint: tunnel is not started")
	}
	peer, exists := w.peers[publicKey]
	if !exists {
		return fmt.Errorf("update peer endpoint: peer not found")
	}
	peer.Endpoint = newEndpoint
	peer.LastHandshake = time.Now().UTC()
	w.stats.LastHandshake = peer.LastHandshake

	// Update in OS
	cmd := fmt.Sprintf("wg set %s peer %s endpoint %s", w.deviceName, publicKey, newEndpoint)
	_ = exec.Command("sh", "-c", cmd).Run()
	return nil
}

func (w *WGTunnel) GetStats() TunnelStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.stats
}

func (w *WGTunnel) RecordRX(bytes uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.RXBytes += bytes
}

func (w *WGTunnel) RecordTX(bytes uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stats.TXBytes += bytes
}

func (w *WGTunnel) Stop() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		return nil
	}
	// Remove all peers from OS
	for pubKey := range w.peers {
		cmd := fmt.Sprintf("wg set %s peer %s remove", w.deviceName, pubKey)
		_ = exec.Command("sh", "-c", cmd).Run()
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

func (w *WGTunnel) ReadPacket(buf []byte) (int, error) {
	w.mu.RLock()
	dev := w.device
	w.mu.RUnlock()
	if dev == nil {
		return 0, fmt.Errorf("read packet: tunnel not started")
	}
	return dev.Read(buf)
}

func (w *WGTunnel) WritePacket(buf []byte) (int, error) {
	w.mu.RLock()
	dev := w.device
	w.mu.RUnlock()
	if dev == nil {
		return 0, fmt.Errorf("write packet: tunnel not started")
	}
	return dev.Write(buf)
}

func (w *WGTunnel) FindEndpointByVirtualIP(destIP string) (string, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	parsed := net.ParseIP(destIP)
	if parsed == nil {
		return "", false
	}
	for _, p := range w.peers {
		_, network, err := net.ParseCIDR(p.AllowedIP)
		if err == nil && network.Contains(parsed) {
			return p.Endpoint, true
		}
	}
	return "", false
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
