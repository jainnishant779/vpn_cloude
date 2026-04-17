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

// PeerConnection tracks active connection state to a remote peer.
type PeerConnection struct {
	PeerID       string
	PeerName     string
	PublicKey    string
	VirtualIP    string
	Endpoint     string
	ConnectedVia string
	LastSeen     time.Time
}

// PeerManager maintains connectivity to all peers in a network.
type PeerManager struct {
	tunnel      *tunnel.WGTunnel
	apiClient   *api_client.Client
	holePuncher *nat.HolePuncher
	peers       map[string]*PeerConnection
	networkID   string
	localPeerID string

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

// Start launches periodic synchronization with coordination service.
func (m *PeerManager) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		_ = m.syncPeersOnce()

		ticker := time.NewTicker(30 * time.Second)
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

// Stop terminates background loops and disconnects tracked peers.
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
		if err := m.disconnectPeer(peerID); err != nil {
			return fmt.Errorf("peer manager stop: disconnect peer %s: %w", peerID, err)
		}
	}

	return nil
}

func (m *PeerManager) syncPeersOnce() error {
	if m.apiClient == nil {
		return fmt.Errorf("sync peers: api client is nil")
	}

	peerList, err := m.apiClient.GetPeers(m.networkID)
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
		_, alreadyConnected := m.peers[peerInfo.ID]
		m.mu.RUnlock()

		if !alreadyConnected {
			_ = m.connectToPeer(peerInfo)
			continue
		}

		m.mu.Lock()
		if conn, ok := m.peers[peerInfo.ID]; ok {
			conn.LastSeen = time.Now().UTC()
		}
		m.mu.Unlock()
	}

	m.mu.RLock()
	existingIDs := make([]string, 0, len(m.peers))
	for peerID := range m.peers {
		existingIDs = append(existingIDs, peerID)
	}
	m.mu.RUnlock()

	for _, peerID := range existingIDs {
		if _, ok := observed[peerID]; ok {
			continue
		}
		_ = m.disconnectPeer(peerID)
	}

	return nil
}

func (m *PeerManager) connectToPeer(peer api_client.PeerInfo) error {
	if m.tunnel == nil {
		return fmt.Errorf("connect to peer: tunnel is nil")
	}

	endpoint := strings.TrimSpace(peer.PublicEndpoint)
	connectedVia := "relay"

	if endpoint != "" && m.holePuncher != nil {
		host, port, err := splitHostPort(endpoint)
		if err == nil {
			success, punchErr := m.holePuncher.Punch(host, port)
			if punchErr == nil && success {
				if conn := m.holePuncher.Conn(); conn != nil {
					peerAddr, resolveErr := net.ResolveUDPAddr("udp", endpoint)
					if resolveErr == nil {
						_ = ExchangeHandshake(conn, peerAddr, peer.ID, m.handshakeSecret)
					}
				}
				connectedVia = "p2p"
			}
		}
	}

	if connectedVia != "p2p" {
		if m.apiClient == nil {
			return fmt.Errorf("connect to peer: api client is nil")
		}
		relayInfo, err := m.apiClient.GetNearestRelay(peer.ID)
		if err != nil {
			return fmt.Errorf("connect to peer: assign relay: %w", err)
		}
		endpoint = net.JoinHostPort(relayInfo.RelayHost, strconv.Itoa(relayInfo.RelayPort))
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
		if err := m.tunnel.RemovePeer(conn.PublicKey); err != nil {
			return fmt.Errorf("disconnect peer: remove tunnel peer: %w", err)
		}
	}

	m.mu.Lock()
	delete(m.peers, peerID)
	m.mu.Unlock()
	return nil
}

// ListConnections returns a snapshot of current peer connections.
func (m *PeerManager) ListConnections() []PeerConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]PeerConnection, 0, len(m.peers))
	for _, conn := range m.peers {
		result = append(result, *conn)
	}
	return result
}

// ForceRelay updates a peer route to relay endpoint when direct path quality degrades.
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

// AttemptDirect retries P2P hole punching and switches endpoint back to direct path on success.
func (m *PeerManager) AttemptDirect(peerID string) error {
	if m.apiClient == nil || m.holePuncher == nil {
		return fmt.Errorf("attempt direct: manager missing api client or hole puncher")
	}

	peers, err := m.apiClient.GetPeers(m.networkID)
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
		return fmt.Errorf("attempt direct: peer not found in coordination list")
	}
	if target.PublicEndpoint == "" {
		return fmt.Errorf("attempt direct: peer has no direct endpoint")
	}

	host, port, err := splitHostPort(target.PublicEndpoint)
	if err != nil {
		return fmt.Errorf("attempt direct: parse endpoint: %w", err)
	}

	ok, err := m.holePuncher.Punch(host, port)
	if err != nil {
		return fmt.Errorf("attempt direct: hole punch failed: %w", err)
	}
	if !ok {
		return fmt.Errorf("attempt direct: hole punch timed out")
	}

	m.mu.RLock()
	conn, exists := m.peers[peerID]
	m.mu.RUnlock()
	if !exists {
		return fmt.Errorf("attempt direct: peer not connected")
	}

	if err := m.tunnel.UpdatePeerEndpoint(conn.PublicKey, target.PublicEndpoint); err != nil {
		return fmt.Errorf("attempt direct: update tunnel endpoint: %w", err)
	}

	m.mu.Lock()
	if current, ok := m.peers[peerID]; ok {
		current.Endpoint = target.PublicEndpoint
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
