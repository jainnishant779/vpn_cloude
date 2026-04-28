package peer

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"quicktunnel/client/internal/api_client"
	"quicktunnel/client/internal/nat"
	"quicktunnel/client/internal/tunnel"
)

const defaultPeerSyncInterval = 15 * time.Second

type PeerConnection struct {
	PeerID       string
	PeerName     string
	PublicKey    string
	VirtualIP    string
	Endpoint     string
	ConnectedVia string
	LastSeen     time.Time
}

type PeerManager struct {
	tunnel      *tunnel.WGTunnel
	apiClient   *api_client.Client
	holePuncher *nat.HolePuncher
	peers       map[string]*PeerConnection
	networkID   string
	localPeerID string
	memberID    string

	handshakeSecret []byte

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	syncInterval time.Duration
}

func NewPeerManager(
	tun *tunnel.WGTunnel,
	apiClient *api_client.Client,
	holePuncher *nat.HolePuncher,
	networkID string,
	localPeerID string,
) *PeerManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &PeerManager{
		tunnel:          tun,
		apiClient:       apiClient,
		holePuncher:     holePuncher,
		peers:           make(map[string]*PeerConnection),
		networkID:       strings.TrimSpace(networkID),
		localPeerID:     strings.TrimSpace(localPeerID),
		handshakeSecret: []byte("quicktunnel-handshake-secret"),
		ctx:             ctx,
		cancel:          cancel,
		syncInterval:    defaultPeerSyncInterval,
	}
}

func (m *PeerManager) SetMemberID(memberID string) {
	m.memberID = memberID
}

func (m *PeerManager) SetSyncInterval(d time.Duration) {
	if d < 10*time.Second {
		d = 10 * time.Second
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncInterval = d
}

func (m *PeerManager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_ = m.syncPeersOnce()

		m.mu.RLock()
		interval := m.syncInterval
		m.mu.RUnlock()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				_ = m.syncPeersOnce()
			}
		}
	}()
}

func (m *PeerManager) Stop() error {
	m.cancel()
	m.wg.Wait()

	m.mu.RLock()
	peerIDs := make([]string, 0, len(m.peers))
	for peerID := range m.peers {
		peerIDs = append(peerIDs, peerID)
	}
	m.mu.RUnlock()

	for _, peerID := range peerIDs {
		_ = m.disconnectPeer(peerID)
	}
	return nil
}

func (m *PeerManager) fetchPeers() ([]api_client.PeerInfo, error) {
	if m.memberID != "" {
		return m.apiClient.MemberGetPeers(m.memberID)
	}
	return m.apiClient.GetPeers(m.networkID)
}

func (m *PeerManager) syncPeersOnce() error {
	if m.apiClient == nil {
		return fmt.Errorf("sync peers: api client is nil")
	}

	peerList, err := m.fetchPeers()
	if err != nil {
		return fmt.Errorf("sync peers: fetch peers: %w", err)
	}

	observed := make(map[string]api_client.PeerInfo)
	for _, peerInfo := range peerList {
		if peerInfo.ID == "" || peerInfo.ID == m.localPeerID {
			continue
		}
		observed[peerInfo.ID] = peerInfo

		m.mu.RLock()
		existing, alreadyConnected := m.peers[peerInfo.ID]
		m.mu.RUnlock()

		if !alreadyConnected {
			if err := m.connectToPeer(peerInfo); err != nil {
				fmt.Printf("[PEER] connect failed peer=%s (%s): %v\n", peerInfo.Name, peerInfo.ID, err)
			}
			continue
		}

		newEndpoint := preferIPv4(peerInfo.PublicEndpoint, peerInfo.LocalEndpoints, m.tunnel.CIDR())
		newConnectedVia := "p2p"
		if newEndpoint == "" {
			relayEndpoint, err := m.resolveRelayEndpoint(peerInfo.ID)
			if err == nil && relayEndpoint != "" {
				newEndpoint = relayEndpoint
				newConnectedVia = "relay"
			}
		}

		if newEndpoint != "" && newEndpoint != existing.Endpoint {
			if err := m.tunnel.UpdatePeerEndpoint(existing.PublicKey, newEndpoint); err != nil {
				fmt.Printf("[PEER] endpoint update failed peer=%s (%s): %v\n", peerInfo.Name, peerInfo.ID, err)
			}
			m.mu.Lock()
			if conn, ok := m.peers[peerInfo.ID]; ok {
				conn.Endpoint = newEndpoint
				conn.ConnectedVia = newConnectedVia
				conn.LastSeen = time.Now().UTC()
			}
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			if conn, ok := m.peers[peerInfo.ID]; ok {
				conn.LastSeen = time.Now().UTC()
			}
			m.mu.Unlock()
		}
	}

	m.mu.RLock()
	existingIDs := make([]string, 0, len(m.peers))
	for peerID := range m.peers {
		existingIDs = append(existingIDs, peerID)
	}
	m.mu.RUnlock()

	for _, peerID := range existingIDs {
		if _, ok := observed[peerID]; !ok {
			_ = m.disconnectPeer(peerID)
		}
	}

	return nil
}

// preferIPv4 returns the best IPv4 endpoint, never falling back to unreachable IPv6.
func preferIPv4(publicEndpoint string, localEndpoints []string, tunCIDR string) string {
	if endpoint := normalizePublicEndpoint(publicEndpoint); endpoint != "" {
		return endpoint
	}

	// Optional LAN fallback. Disabled by default because many networks reuse the
	// same private subnets (for example 192.168.x.x), which can cause false
	// "same-lan" matches and break WireGuard handshake.
	if !lanFallbackEnabled() {
		return ""
	}

	return preferLANEndpoint(localEndpoints, tunCIDR)
}

func normalizePublicEndpoint(publicEndpoint string) string {
	publicEndpoint = strings.TrimSpace(publicEndpoint)
	if publicEndpoint == "" {
		return ""
	}

	host, port, err := net.SplitHostPort(publicEndpoint)
	if err == nil {
		host = strings.TrimSpace(host)
		port = strings.TrimSpace(port)
		if port == "" {
			port = "51820"
		}
		if ip := net.ParseIP(host); ip != nil {
			if !isRoutableIPv4(ip) {
				return ""
			}
			return net.JoinHostPort(ip.String(), port)
		}
		if isInternalHostName(host) {
			return ""
		}
		// Hostname endpoint. Keep announced port as-is.
		return net.JoinHostPort(host, port)
	}

	if ip := net.ParseIP(publicEndpoint); ip != nil {
		if !isRoutableIPv4(ip) {
			return ""
		}
		return net.JoinHostPort(ip.String(), "51820")
	}
	return ""
}

func preferLANEndpoint(localEndpoints []string, tunCIDR string) string {
	addrs, _ := net.InterfaceAddrs()
	_, tunNet, _ := net.ParseCIDR(tunCIDR)
	for _, ep := range localEndpoints {
		peerHost, peerPort, err := net.SplitHostPort(ep)
		if err != nil {
			peerHost = ep
			peerPort = "51820"
		}
		peerIP := net.ParseIP(peerHost)
		if peerIP == nil || peerIP.To4() == nil {
			continue
		}
		if strings.HasPrefix(peerHost, "169.254.") || peerHost == "127.0.0.1" {
			continue
		}
		if tunNet != nil && tunNet.Contains(peerIP) {
			continue
		}
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
				if ipnet.Contains(peerIP) {
					return net.JoinHostPort(peerHost, peerPort)
				}
			}
		}
	}

	return ""
}

func lanFallbackEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("QT_ENABLE_LAN_ENDPOINT_FALLBACK")))
	return v == "1" || v == "true" || v == "yes"
}

func isInternalHostName(host string) bool {
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
	// Single-label hostnames are usually internal service names.
	return !strings.Contains(h, ".")
}

func isRoutableIPv4(ip net.IP) bool {
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.IsPrivate() {
		return false
	}
	// Link-local 169.254.0.0/16
	if v4[0] == 169 && v4[1] == 254 {
		return false
	}
	// Carrier-grade NAT 100.64.0.0/10
	if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
		return false
	}
	return true
}

func (m *PeerManager) connectToPeer(peer api_client.PeerInfo) error {
	if m.tunnel == nil {
		return fmt.Errorf("connect to peer: tunnel is nil")
	}

	endpoint := preferIPv4(peer.PublicEndpoint, peer.LocalEndpoints, m.tunnel.CIDR())
	connectedVia := "p2p"

	if endpoint == "" {
		relayEndpoint, err := m.resolveRelayEndpoint(peer.ID)
		if err != nil {
			return fmt.Errorf("connect to peer: resolve relay endpoint: %w", err)
		}
		if relayEndpoint != "" {
			endpoint = relayEndpoint
			connectedVia = "relay"
		}
	}

	if endpoint == "" {
		return fmt.Errorf("connect to peer: no endpoint available for %s", peer.Name)
	}

	allowedIP := strings.TrimSpace(peer.VirtualIP)
	if allowedIP == "" {
		return fmt.Errorf("connect to peer: peer virtual ip is required")
	}
	if !strings.Contains(allowedIP, "/") {
		allowedIP += "/32"
	}

	finalEndpoint, finalVia, err := m.addPeerWithFallback(peer, endpoint, connectedVia, allowedIP)
	if err != nil {
		return fmt.Errorf("connect to peer: add tunnel peer: %w", err)
	}

	m.mu.Lock()
	m.peers[peer.ID] = &PeerConnection{
		PeerID:       peer.ID,
		PeerName:     peer.Name,
		PublicKey:    peer.PublicKey,
		VirtualIP:    peer.VirtualIP,
		Endpoint:     finalEndpoint,
		ConnectedVia: finalVia,
		LastSeen:     time.Now().UTC(),
	}
	m.mu.Unlock()

	return nil
}

func (m *PeerManager) addPeerWithFallback(peer api_client.PeerInfo, primaryEndpoint, primaryVia, allowedIP string) (string, string, error) {
	type candidate struct {
		endpoint string
		via      string
	}

	candidates := make([]candidate, 0, 3)
	seen := map[string]struct{}{}
	addCandidate := func(endpoint, via string) {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			return
		}
		if _, ok := seen[endpoint]; ok {
			return
		}
		seen[endpoint] = struct{}{}
		candidates = append(candidates, candidate{endpoint: endpoint, via: via})
	}

	addCandidate(primaryEndpoint, primaryVia)

	// If relay fails, try direct public endpoint and then LAN endpoint.
	addCandidate(normalizePublicEndpoint(peer.PublicEndpoint), "p2p")
	addCandidate(preferLANEndpoint(peer.LocalEndpoints, m.tunnel.CIDR()), "lan")

	var lastErr error
	for _, c := range candidates {
		if err := m.tunnel.AddPeer(peer.PublicKey, c.endpoint, allowedIP); err != nil {
			lastErr = err
			continue
		}
		return c.endpoint, c.via, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoint candidates available")
	}
	return "", "", lastErr
}

func (m *PeerManager) resolveRelayEndpoint(peerID string) (string, error) {
	if m.apiClient == nil {
		return "", fmt.Errorf("api client is nil")
	}
	var relayInfo *api_client.RelayInfo
	var err error
	if m.memberID != "" {
		relayInfo, err = m.apiClient.MemberGetNearestRelay(m.memberID, m.networkID, peerID)
	} else {
		relayInfo, err = m.apiClient.GetNearestRelay(m.networkID, peerID)
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "missing api key") && strings.TrimSpace(m.localPeerID) != "" {
			relayInfo, err = m.apiClient.MemberGetNearestRelay(m.localPeerID, m.networkID, peerID)
		}
	}
	if err != nil {
		return "", err
	}

	host := strings.TrimSpace(relayInfo.RelayHost)
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
			host = h
		}
	}
	if isInternalHostName(host) {
		if h, _, splitErr := net.SplitHostPort(strings.TrimSpace(relayInfo.RelayEndpoint)); splitErr == nil && !isInternalHostName(h) {
			host = h
		}
	}
	if isInternalHostName(host) {
		host = m.apiClient.PublicHost()
	}

	port := relayInfo.RelayPort
	if port <= 0 || port > 65535 {
		port = 3478
	}
	if isInternalHostName(host) {
		return "", fmt.Errorf("invalid relay endpoint")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func (m *PeerManager) disconnectPeer(peerID string) error {
	m.mu.RLock()
	conn, exists := m.peers[peerID]
	m.mu.RUnlock()
	if !exists {
		return nil
	}
	if conn.PublicKey != "" {
		_ = m.tunnel.RemovePeer(conn.PublicKey)
	}
	m.mu.Lock()
	delete(m.peers, peerID)
	m.mu.Unlock()
	return nil
}

func (m *PeerManager) ListConnections() []PeerConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]PeerConnection, 0, len(m.peers))
	for _, conn := range m.peers {
		result = append(result, *conn)
	}
	return result
}

func (m *PeerManager) ForceRelay(peerID string) error {
	m.mu.RLock()
	conn, exists := m.peers[peerID]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("force relay: peer not connected")
	}
	endpoint, err := m.resolveRelayEndpoint(peerID)
	if err != nil {
		return fmt.Errorf("force relay: assign relay: %w", err)
	}
	if err := m.tunnel.UpdatePeerEndpoint(conn.PublicKey, endpoint); err != nil {
		return fmt.Errorf("force relay: update tunnel endpoint: %w", err)
	}
	m.mu.Lock()
	if current, ok := m.peers[peerID]; ok {
		current.Endpoint = endpoint
		current.ConnectedVia = "relay"
		current.LastSeen = time.Now().UTC()
	}
	m.mu.Unlock()
	return nil
}

func (m *PeerManager) AttemptDirect(peerID string) error {
	peers, err := m.fetchPeers()
	if err != nil {
		return fmt.Errorf("attempt direct: fetch peers: %w", err)
	}
	var target *api_client.PeerInfo
	for i := range peers {
		if peers[i].ID == peerID {
			target = &peers[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("attempt direct: peer not found")
	}
	endpoint := preferIPv4(target.PublicEndpoint, target.LocalEndpoints, m.tunnel.CIDR())
	if endpoint == "" {
		return fmt.Errorf("attempt direct: peer has no direct endpoint")
	}
	m.mu.RLock()
	conn, exists := m.peers[peerID]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("attempt direct: peer not connected")
	}
	if err := m.tunnel.UpdatePeerEndpoint(conn.PublicKey, endpoint); err != nil {
		return fmt.Errorf("attempt direct: update tunnel endpoint: %w", err)
	}
	m.mu.Lock()
	if current, ok := m.peers[peerID]; ok {
		current.Endpoint = endpoint
		current.ConnectedVia = "p2p"
		current.LastSeen = time.Now().UTC()
	}
	m.mu.Unlock()
	return nil
}

func splitHostPort(endpoint string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(endpoint))
	if err != nil {
		return "", 0, fmt.Errorf("split host port: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("split host port: invalid port: %w", err)
	}
	if port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("split host port: port out of range")
	}
	return host, port, nil
}

