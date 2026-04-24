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

// preferIPv4 selects the best endpoint for a peer.
// Priority:
//  1. Local LAN IP (same /24 subnet) — avoids NAT hairpin issues
//  2. Private RFC1918 IP (same router, different subnet)
//  3. Public IPv4 endpoint
func preferIPv4(publicEndpoint string, localEndpoints []string) string {
	myAddrs, _ := net.InterfaceAddrs()

	// ── Priority 1: Same LAN (same /24) ──────────────────────────────────────
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
		for _, addr := range myAddrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
				continue
			}
			// Exact subnet match
			if ipnet.Contains(peerIP) {
				if peerPort == "" {
					peerPort = "51820"
				}
				return net.JoinHostPort(peerHost, peerPort)
			}
		}
	}

	// ── Priority 2: Any private RFC1918 local endpoint ───────────────────────
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
		if isPrivateIP(peerIP) && !strings.HasPrefix(peerHost, "169.254.") {
			if peerPort == "" {
				peerPort = "51820"
			}
			return net.JoinHostPort(peerHost, peerPort)
		}
	}

	// ── Priority 3: Public IPv4 endpoint ────────────────────────────────────
	if publicEndpoint != "" && !strings.HasPrefix(publicEndpoint, "[") {
		host, port, err := net.SplitHostPort(publicEndpoint)
		if err == nil {
			if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
				// Always use port 51820 for WireGuard
				_ = port
				return net.JoinHostPort(host, "51820")
			}
		}
	}

	return ""
}

// isPrivateIP returns true for RFC1918 private addresses.
func isPrivateIP(ip net.IP) bool {
	private := []string{"10.", "192.168.", "172.16.", "172.17.", "172.18.",
		"172.19.", "172.20.", "172.21.", "172.22.", "172.23.", "172.24.",
		"172.25.", "172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31."}
	s := ip.String()
	for _, p := range private {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
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
