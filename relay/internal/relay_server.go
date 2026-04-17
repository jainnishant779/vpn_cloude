package relay

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

const (
	PacketTypeConnect    byte = 0x01
	PacketTypeData       byte = 0x02
	PacketTypeDisconnect byte = 0x03
	PacketTypePing       byte = 0x04

	packetHeaderSize   = 17 // 1 byte type + 16 byte session id
	readBufferSize     = 64 * 1024
	sessionIdleTimeout = 5 * time.Minute
)

// Session tracks two peers paired for relay forwarding.
type Session struct {
	ID           [16]byte
	Token        string
	PeerAAddr    *net.UDPAddr
	PeerBAddr    *net.UDPAddr
	CreatedAt    time.Time
	LastActivity time.Time
}

// RelayServer forwards UDP traffic between paired peers.
type RelayServer struct {
	port int

	mu             sync.RWMutex
	sessions       map[string]*Session
	tokenToSession map[string]string

	conn    *net.UDPConn
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	metrics *Metrics
	logger  zerolog.Logger
}

type connectPayload struct {
	SessionToken string `json:"session_token"`
	PeerID       string `json:"peer_id"`
}

// NewRelayServer constructs a relay instance for a UDP port.
func NewRelayServer(port int) *RelayServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &RelayServer{
		port:           port,
		sessions:       make(map[string]*Session),
		tokenToSession: make(map[string]string),
		ctx:            ctx,
		cancel:         cancel,
		done:           make(chan struct{}),
		logger:         log.With().Str("component", "relay").Int("port", port).Logger(),
	}
}

// SetMetrics attaches metrics collection to relay traffic.
func (s *RelayServer) SetMetrics(metrics *Metrics) {
	s.metrics = metrics
}

// Start binds UDP socket and processes packets until stopped.
func (s *RelayServer) Start() error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", s.port))
	if err != nil {
		return fmt.Errorf("start relay: resolve address: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("start relay: listen udp: %w", err)
	}
	s.conn = conn
	s.logger.Info().Str("listen_addr", conn.LocalAddr().String()).Msg("relay udp listener started")

	go s.cleanupStaleSessions()

	defer func() {
		_ = conn.Close()
		close(s.done)
	}()

	buffer := make([]byte, readBufferSize)
	for {
		n, from, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || s.ctx.Err() != nil {
				s.logger.Info().Msg("relay udp listener stopped")
				return nil
			}
			return fmt.Errorf("start relay: read udp: %w", err)
		}

		packet := make([]byte, n)
		copy(packet, buffer[:n])

		if err := s.handlePacket(packet, from); err != nil {
			s.logger.Warn().Err(err).Str("from", from.String()).Msg("failed to handle packet")
		}
	}
}

// Stop requests graceful relay shutdown.
func (s *RelayServer) Stop() error {
	s.cancel()
	if s.conn != nil {
		if err := s.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("stop relay: close udp connection: %w", err)
		}
	}

	select {
	case <-s.done:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("stop relay: timeout waiting for shutdown")
	}
}

func (s *RelayServer) handlePacket(data []byte, from *net.UDPAddr) error {
	if len(data) < packetHeaderSize {
		return fmt.Errorf("handle packet: invalid packet length %d", len(data))
	}

	packetType := data[0]
	sessionID := data[1:17]
	payload := data[17:]

	switch packetType {
	case PacketTypeConnect:
		var req connectPayload
		if err := json.Unmarshal(payload, &req); err != nil {
			return fmt.Errorf("handle packet: parse connect payload: %w", err)
		}
		if req.SessionToken == "" {
			return fmt.Errorf("handle packet: empty session token")
		}

		session := s.createSession(req.SessionToken, from)
		status := []byte("waiting")
		if session.PeerAAddr != nil && session.PeerBAddr != nil {
			status = []byte("paired")
			s.broadcastControl(session, PacketTypePing, []byte("paired"))
		}

		if err := s.sendPacket(packetTypeForAck(status), session.ID, status, from); err != nil {
			return fmt.Errorf("handle packet: send connect ack: %w", err)
		}
		return nil

	case PacketTypeData:
		session := s.getSessionByID(sessionID)
		if session == nil {
			return fmt.Errorf("handle packet: data for unknown session")
		}
		return s.forwardPacket(session, data, from)

	case PacketTypePing:
		session := s.getSessionByID(sessionID)
		if session == nil {
			return fmt.Errorf("handle packet: ping for unknown session")
		}
		s.touchSession(session)
		return s.sendPacket(PacketTypePing, session.ID, []byte("pong"), from)

	case PacketTypeDisconnect:
		session := s.getSessionByID(sessionID)
		if session == nil {
			return nil
		}
		s.removeSession(session.ID)
		return nil

	default:
		return fmt.Errorf("handle packet: unknown packet type 0x%02x", packetType)
	}
}

func (s *RelayServer) createSession(token string, peer *net.UDPAddr) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	if key, ok := s.tokenToSession[token]; ok {
		if existing, exists := s.sessions[key]; exists {
			now := time.Now().UTC()
			existing.LastActivity = now

			if existing.PeerAAddr == nil {
				existing.PeerAAddr = cloneUDPAddr(peer)
			} else if !sameAddr(existing.PeerAAddr, peer) && existing.PeerBAddr == nil {
				existing.PeerBAddr = cloneUDPAddr(peer)
			}

			s.updateActiveSessionGaugeLocked()
			return existing
		}
	}

	id, err := randomSessionID()
	if err != nil {
		// Fallback keeps relay usable in improbable RNG failure.
		now := time.Now().UTC().UnixNano()
		copy(id[:], []byte(fmt.Sprintf("%016x", now)))
	}

	now := time.Now().UTC()
	session := &Session{
		ID:           id,
		Token:        token,
		PeerAAddr:    cloneUDPAddr(peer),
		CreatedAt:    now,
		LastActivity: now,
	}
	key := sessionKey(id[:])
	s.sessions[key] = session
	s.tokenToSession[token] = key
	s.updateActiveSessionGaugeLocked()
	return session
}

func (s *RelayServer) forwardPacket(session *Session, data []byte, from *net.UDPAddr) error {
	s.mu.Lock()
	session.LastActivity = time.Now().UTC()
	to := destinationAddr(session, from)
	s.mu.Unlock()

	if to == nil {
		return fmt.Errorf("forward packet: sender is not part of session")
	}
	if s.conn == nil {
		return fmt.Errorf("forward packet: udp connection is not initialized")
	}

	if _, err := s.conn.WriteToUDP(data, to); err != nil {
		return fmt.Errorf("forward packet: write to destination: %w", err)
	}
	if s.metrics != nil {
		s.metrics.RecordPacket(len(data))
	}
	return nil
}

// cleanupStaleSessions removes stale session state in the background.
func (s *RelayServer) cleanupStaleSessions() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			removed := s.removeStaleSessions(time.Now().UTC())
			if removed > 0 {
				s.logger.Info().Int("removed_sessions", removed).Msg("cleaned stale relay sessions")
			}
		}
	}
}

func (s *RelayServer) removeStaleSessions(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0
	for key, session := range s.sessions {
		if now.Sub(session.LastActivity) <= sessionIdleTimeout {
			continue
		}
		delete(s.sessions, key)
		delete(s.tokenToSession, session.Token)
		removed++
	}
	if removed > 0 {
		s.updateActiveSessionGaugeLocked()
	}
	return removed
}

func (s *RelayServer) removeSession(id [16]byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := sessionKey(id[:])
	session, exists := s.sessions[key]
	if !exists {
		return
	}

	delete(s.sessions, key)
	delete(s.tokenToSession, session.Token)
	s.updateActiveSessionGaugeLocked()
}

func (s *RelayServer) getSessionByID(id []byte) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionKey(id)]
	if !ok {
		return nil
	}
	return session
}

func (s *RelayServer) touchSession(session *Session) {
	s.mu.Lock()
	session.LastActivity = time.Now().UTC()
	s.mu.Unlock()
}

func (s *RelayServer) broadcastControl(session *Session, packetType byte, payload []byte) {
	if session.PeerAAddr != nil {
		_ = s.sendPacket(packetType, session.ID, payload, session.PeerAAddr)
	}
	if session.PeerBAddr != nil {
		_ = s.sendPacket(packetType, session.ID, payload, session.PeerBAddr)
	}
}

func (s *RelayServer) sendPacket(packetType byte, sessionID [16]byte, payload []byte, to *net.UDPAddr) error {
	if s.conn == nil {
		return fmt.Errorf("send packet: udp connection is not initialized")
	}
	packet := make([]byte, packetHeaderSize+len(payload))
	packet[0] = packetType
	copy(packet[1:17], sessionID[:])
	copy(packet[17:], payload)

	if _, err := s.conn.WriteToUDP(packet, to); err != nil {
		return fmt.Errorf("send packet: write udp: %w", err)
	}
	if s.metrics != nil {
		s.metrics.RecordPacket(len(packet))
	}
	return nil
}

func (s *RelayServer) updateActiveSessionGaugeLocked() {
	if s.metrics != nil {
		s.metrics.SetActiveSessions(len(s.sessions))
	}
}

func packetTypeForAck(status []byte) byte {
	// Ping responses are used as lightweight control acknowledgements.
	_ = status
	return PacketTypePing
}

func destinationAddr(session *Session, from *net.UDPAddr) *net.UDPAddr {
	switch {
	case session.PeerAAddr != nil && sameAddr(session.PeerAAddr, from):
		return cloneUDPAddr(session.PeerBAddr)
	case session.PeerBAddr != nil && sameAddr(session.PeerBAddr, from):
		return cloneUDPAddr(session.PeerAAddr)
	default:
		return nil
	}
}

func sameAddr(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

func cloneUDPAddr(in *net.UDPAddr) *net.UDPAddr {
	if in == nil {
		return nil
	}
	ip := make(net.IP, len(in.IP))
	copy(ip, in.IP)
	return &net.UDPAddr{IP: ip, Port: in.Port, Zone: in.Zone}
}

func sessionKey(raw []byte) string {
	return string(raw)
}

func randomSessionID() ([16]byte, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return id, fmt.Errorf("random session id: %w", err)
	}
	return id, nil
}
