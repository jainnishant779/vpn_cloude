package peer

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"quicktunnel/client/internal/api_client"
	"quicktunnel/client/internal/nat"
	"quicktunnel/client/internal/tunnel"
)

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
	memberID    string // ZeroTier mode

	handshakeSecret []byte

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	}
}

// SetMemberID enables ZeroTier-style peer fetching.
func (m *PeerManager) SetMemberID(memberID string) {
	m.memberID = memberID
}

func (m *PeerManager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_ = m.syncPeersOnce()

		ticker := time.NewTicker(15 * time.Second)
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

// fetchPeers uses member token if available, else API key.
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
			_ = m.connectToPeer(peerInfo)
			continue
		}

		// Update endpoint if changed
		newEndpoint := preferIPv4(peerInfo.PublicEndpoint, peerInfo.LocalEndpoints)
		if newEndpoint != "" && newEndpoint != existing.Endpoint {
			_ = m.tunnel.UpdatePeerEndpoint(existing.PublicKey, newEndpoint)
			m.mu.Lock()
			if conn, ok := m.peers[peerInfo.ID]; ok {
				conn.Endpoint = newEndpoint
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

	// Remove peers that went offline
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

// preferIPv4 returns IPv4 endpoint if available, otherwise returns original.
func preferIPv4(publicEndpoint string, localEndpoints []string) string {
	// Public IPv4 (not starting with [)
	if publicEndpoint != "" && !strings.HasPrefix(publicEndpoint, "[") {
		return publicEndpoint
	}
	// Try local endpoints for IPv4
	for _, ep := range localEndpoints {
		if !strings.HasPrefix(ep, "[") && strings.Contains(ep, ".") {
			if !strings.Contains(ep, ":") {
				return ep + ":51820"
			}
			return ep
		}
	}
	// Fall back to IPv6 public endpoint
	return publicEndpoint
}

func (m *PeerManager) connectToPeer(peer api_client.PeerInfo) error {
	if m.tunnel == nil {
		return fmt.Errorf("connect to peer: tunnel is nil")
	}

	// Get best endpoint — prefer IPv4
	endpoint := preferIPv4(peer.PublicEndpoint, peer.LocalEndpoints)
	connectedVia := "direct"

	// If no endpoint at all, try relay
	if endpoint == "" {
		if m.apiClient != nil {
			relayInfo, err := m.apiClient.GetNearestRelay(peer.ID)
			if err == nil {
				endpoint = net.JoinHostPort(relayInfo.RelayHost, strconv.Itoa(relayInfo.RelayPort))
				connectedVia = "relay"
			}
		}
	}

	// Still no endpoint — skip for now, will retry on next sync
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

	if err := m.tunnel.AddPeer(peer.PublicKey, endpoint, allowedIP); err != nil {
		return fmt.Errorf("connect to peer: add tunnel peer: %w", err)
	}

	m.mu.Lock()
	m.peers[peer.ID] = &PeerConnection{
		PeerID:       peer.ID,
		PeerName:     peer.Name,
		PublicKey:    peer.PublicKey,
		VirtualIP:    peer.VirtualIP,
		Endpoint:     endpoint,
		ConnectedVia: connectedVia,
		LastSeen:     time.Now().UTC(),
	}
	m.mu.Unlock()

	return nil
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
	relayInfo, err := m.apiClient.GetNearestRelay(peerID)
	if err != nil {
		return fmt.Errorf("force relay: assign relay: %w", err)
	}
	endpoint := net.JoinHostPort(relayInfo.RelayHost, strconv.Itoa(relayInfo.RelayPort))
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
	endpoint := preferIPv4(target.PublicEndpoint, target.LocalEndpoints)
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
