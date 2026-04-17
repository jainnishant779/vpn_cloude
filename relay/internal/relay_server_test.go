package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSessionPairsPeers(t *testing.T) {
	srv := NewRelayServer(3478)

	peerA := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 10001}
	peerB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 10002}

	session1 := srv.createSession("token-a", peerA)
	require.NotNil(t, session1)
	require.NotNil(t, session1.PeerAAddr)
	assert.Nil(t, session1.PeerBAddr)

	session2 := srv.createSession("token-a", peerB)
	require.NotNil(t, session2)
	assert.Equal(t, sessionKey(session1.ID[:]), sessionKey(session2.ID[:]))
	require.NotNil(t, session2.PeerAAddr)
	require.NotNil(t, session2.PeerBAddr)
	assert.True(t, sameAddr(peerA, session2.PeerAAddr) || sameAddr(peerA, session2.PeerBAddr))
	assert.True(t, sameAddr(peerB, session2.PeerAAddr) || sameAddr(peerB, session2.PeerBAddr))
}

func TestPacketForwardingBetweenTwoUDPClients(t *testing.T) {
	port := reserveUDPPort(t)
	srv := NewRelayServer(port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	metrics := NewMetrics("test-relay", zerolog.Nop())
	metrics.Start(ctx)
	srv.SetMetrics(metrics)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start()
	}()
	defer func() {
		require.NoError(t, srv.Stop())
		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(1 * time.Second):
			t.Fatalf("relay server did not stop in time")
		}
	}()

	serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", port))
	require.NoError(t, err)

	clientA, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer clientA.Close()

	clientB, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer clientB.Close()

	token := "relay-forward-token"
	require.NoError(t, sendConnect(clientA, serverAddr, token, "peer-a"))
	require.NoError(t, sendConnect(clientB, serverAddr, token, "peer-b"))

	sessionID, err := readSessionIDFromControl(clientA, 2*time.Second)
	if err != nil {
		sessionID, err = readSessionIDFromControl(clientB, 2*time.Second)
	}
	require.NoError(t, err)

	payload := []byte("hello-through-relay")
	dataPacket := buildPacket(PacketTypeData, sessionID, payload)
	_, err = clientA.WriteToUDP(dataPacket, serverAddr)
	require.NoError(t, err)

	receivedType, receivedSessionID, receivedPayload, err := readPacket(clientB, 2*time.Second)
	require.NoError(t, err)
	assert.Equal(t, PacketTypeData, receivedType)
	assert.Equal(t, sessionKey(sessionID[:]), sessionKey(receivedSessionID[:]))
	assert.Equal(t, payload, receivedPayload)
}

func TestStaleSessionCleanup(t *testing.T) {
	srv := NewRelayServer(3478)
	peer := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 10001}

	session := srv.createSession("stale-token", peer)
	require.NotNil(t, session)

	srv.mu.Lock()
	session.LastActivity = time.Now().Add(-6 * time.Minute)
	srv.mu.Unlock()

	removed := srv.removeStaleSessions(time.Now())
	assert.Equal(t, 1, removed)

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	assert.Len(t, srv.sessions, 0)
}

func TestConcurrentSessions(t *testing.T) {
	srv := NewRelayServer(3478)

	const count = 100
	var wg sync.WaitGroup
	wg.Add(count)

	for i := 0; i < count; i++ {
		i := i
		go func() {
			defer wg.Done()
			token := fmt.Sprintf("token-%d", i)
			peerA := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 11000 + i}
			peerB := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12000 + i}
			srv.createSession(token, peerA)
			srv.createSession(token, peerB)
		}()
	}

	wg.Wait()

	srv.mu.RLock()
	defer srv.mu.RUnlock()
	assert.Len(t, srv.sessions, count)
	for _, session := range srv.sessions {
		require.NotNil(t, session.PeerAAddr)
		require.NotNil(t, session.PeerBAddr)
	}
}

func reserveUDPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)
	defer listener.Close()
	return listener.LocalAddr().(*net.UDPAddr).Port
}

func sendConnect(conn *net.UDPConn, server *net.UDPAddr, token, peerID string) error {
	payload, err := json.Marshal(connectPayload{SessionToken: token, PeerID: peerID})
	if err != nil {
		return fmt.Errorf("send connect: marshal payload: %w", err)
	}
	packet := buildPacket(PacketTypeConnect, [16]byte{}, payload)
	if _, err := conn.WriteToUDP(packet, server); err != nil {
		return fmt.Errorf("send connect: write packet: %w", err)
	}
	return nil
}

func readSessionIDFromControl(conn *net.UDPConn, timeout time.Duration) ([16]byte, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		packetType, sessionID, payload, err := readPacket(conn, deadline.Sub(time.Now()))
		if err != nil {
			return [16]byte{}, err
		}
		if packetType == PacketTypePing && (string(payload) == "paired" || string(payload) == "waiting") {
			return sessionID, nil
		}
	}
	return [16]byte{}, fmt.Errorf("read session id: timeout")
}

func readPacket(conn *net.UDPConn, timeout time.Duration) (byte, [16]byte, []byte, error) {
	if timeout <= 0 {
		timeout = 1 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, [16]byte{}, nil, fmt.Errorf("read packet: set deadline: %w", err)
	}

	buf := make([]byte, 2048)
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		return 0, [16]byte{}, nil, fmt.Errorf("read packet: read udp: %w", err)
	}
	if n < packetHeaderSize {
		return 0, [16]byte{}, nil, fmt.Errorf("read packet: packet too small")
	}

	var sessionID [16]byte
	copy(sessionID[:], buf[1:17])
	payload := make([]byte, n-packetHeaderSize)
	copy(payload, buf[packetHeaderSize:n])
	return buf[0], sessionID, payload, nil
}

func buildPacket(packetType byte, sessionID [16]byte, payload []byte) []byte {
	packet := make([]byte, packetHeaderSize+len(payload))
	packet[0] = packetType
	copy(packet[1:17], sessionID[:])
	copy(packet[17:], payload)
	return packet
}
