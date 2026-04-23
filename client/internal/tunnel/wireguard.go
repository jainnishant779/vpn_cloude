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
	Endpoint     string
	AllowedIP    string
	AddedAt      time.Time
	LastHandshake time.Time
}

type WGTunnel struct {
	privateKey string
	virtualIP  string
	cidr       string
	listenPort int
	deviceName string
	mtu        int
	wgReady    bool

	mu      sync.RWMutex
	started bool
	device  TUNDevice
	peers   map[string]*PeerConfig
	stats   TunnelStats
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

// runWGCmd runs a shell command and logs warnings on failure.
func runWGCmd(description, command string) error {
	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		fmt.Printf("[WARN] %s failed: %s — %v\n", description, strings.TrimSpace(string(out)), err)
		return err
	}
	return nil
}

// checkWGAvailable checks if wireguard-tools is installed.
func checkWGAvailable() bool {
	_, err := exec.LookPath("wg")
	return err == nil
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

	// Real WireGuard kernel setup — safe stdin pipe (no shell injection)
	if checkWGAvailable() {
		cmd := exec.Command("wg", "set", w.deviceName,
			"listen-port", fmt.Sprintf("%d", w.listenPort),
			"private-key", "/dev/stdin")
		cmd.Stdin = strings.NewReader(w.privateKey)
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("[WARN] wg set interface failed: %s — %v\n", strings.TrimSpace(string(out)), err)
		} else {
			w.wgReady = true
			fmt.Printf("[INFO] WireGuard kernel module active on %s\n", w.deviceName)
		}
	} else {
		fmt.Println("[WARN] wireguard-tools not installed — raw TUN mode (ICMP only)")
		fmt.Println("[WARN] Install: sudo apt install wireguard-tools")
	}

	// Enable IP forwarding + subnet route
	_ = exec.Command("sh", "-c", "sysctl -w net.ipv4.ip_forward=1").Run()
	maskBits, _ := maskBitsFromCIDR(w.cidr)
	routeCmd := fmt.Sprintf("ip route replace %s/%d dev %s 2>/dev/null || true",
		networkAddr(w.virtualIP, maskBits), maskBits, w.deviceName)
	_ = runWGCmd("add subnet route", routeCmd)

	w.started = true
	w.stats.LastHandshake = time.Now().UTC()
	return nil
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

	// Normalize BEFORE storing
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}

	now := time.Now().UTC()
	w.peers[publicKey] = &PeerConfig{
		PublicKey:     publicKey,
		Endpoint:     endpoint,
		AllowedIP:    allowedIP,
		AddedAt:      now,
		LastHandshake: now,
	}
	w.stats.LastHandshake = now

	// Inject peer into kernel WireGuard
	if w.wgReady {
		cmd := fmt.Sprintf(
			"wg set %s peer %s endpoint %s allowed-ips %s persistent-keepalive 25",
			w.deviceName, publicKey, endpoint, allowedIP,
		)
		_ = runWGCmd("add wg peer", cmd)
	}

	// Host route for peer
	peerIP := strings.Split(allowedIP, "/")[0]
	routeCmd := fmt.Sprintf("ip route replace %s dev %s 2>/dev/null || true", peerIP, w.deviceName)
	_ = runWGCmd("add peer route", routeCmd)

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
	peer, exists := w.peers[publicKey]
	if !exists {
		return fmt.Errorf("remove peer: peer not found")
	}

	// Remove from kernel
	if w.wgReady {
		cmd := fmt.Sprintf("wg set %s peer %s remove", w.deviceName, publicKey)
		_ = runWGCmd("remove wg peer", cmd)
	}

	// Clean up route
	peerIP := strings.Split(peer.AllowedIP, "/")[0]
	routeCmd := fmt.Sprintf("ip route del %s dev %s 2>/dev/null || true", peerIP, w.deviceName)
	_ = runWGCmd("remove peer route", routeCmd)

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

	if w.wgReady {
		cmd := fmt.Sprintf("wg set %s peer %s endpoint %s", w.deviceName, publicKey, newEndpoint)
		_ = runWGCmd("update peer endpoint", cmd)
	}
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

	// Clean up all peers
	for pubKey, peer := range w.peers {
		if w.wgReady {
			cmd := fmt.Sprintf("wg set %s peer %s remove", w.deviceName, pubKey)
			_ = runWGCmd("stop: remove wg peer", cmd)
		}
		peerIP := strings.Split(peer.AllowedIP, "/")[0]
		routeCmd := fmt.Sprintf("ip route del %s dev %s 2>/dev/null || true", peerIP, w.deviceName)
		_ = runWGCmd("stop: remove peer route", routeCmd)
	}

	if w.device != nil {
		if err := w.device.Close(); err != nil {
			return fmt.Errorf("wg tunnel stop: close tun device: %w", err)
		}
	}
	w.device = nil
	w.peers = make(map[string]*PeerConfig)
	w.started = false
	w.wgReady = false
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
