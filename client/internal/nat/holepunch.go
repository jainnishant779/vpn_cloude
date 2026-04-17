package nat

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

var punchMagic = []byte{0x51, 0x54, 0x50, 0x4e, 0x43, 0x48} // "QTPNCH"

// HolePuncher performs UDP hole punching against peer public endpoints.
type HolePuncher struct {
	conn    *net.UDPConn
	timeout time.Duration
}

// NewHolePuncher creates a UDP socket bound to localPort (0 for ephemeral).
func NewHolePuncher(localPort int) (*HolePuncher, error) {
	if localPort < 0 || localPort > 65535 {
		return nil, fmt.Errorf("new hole puncher: local port out of range")
	}

	addr := &net.UDPAddr{IP: net.IPv4zero, Port: localPort}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("new hole puncher: listen udp: %w", err)
	}

	return &HolePuncher{
		conn:    conn,
		timeout: 10 * time.Second,
	}, nil
}

// Punch sends punch probes and waits for peer packet confirmation.
func (h *HolePuncher) Punch(peerPublicIP string, peerPublicPort int) (bool, error) {
	if h == nil || h.conn == nil {
		return false, fmt.Errorf("punch: hole puncher is not initialized")
	}
	if peerPublicIP == "" || peerPublicPort <= 0 || peerPublicPort > 65535 {
		return false, fmt.Errorf("punch: invalid peer endpoint")
	}

	peerAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(peerPublicIP, fmt.Sprintf("%d", peerPublicPort)))
	if err != nil {
		return false, fmt.Errorf("punch: resolve peer address: %w", err)
	}

	done := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		defer close(done)
		if err := h.sendPunchBursts(peerAddr); err != nil {
			errCh <- err
		}
	}()

	deadline := time.Now().Add(h.timeout)
	buffer := make([]byte, 2048)
	for time.Now().Before(deadline) {
		if err := h.conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond)); err != nil {
			return false, fmt.Errorf("punch: set read deadline: %w", err)
		}

		n, from, err := h.conn.ReadFromUDP(buffer)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case sendErr := <-errCh:
					return false, fmt.Errorf("punch: send bursts failed: %w", sendErr)
				default:
				}
				continue
			}
			return false, fmt.Errorf("punch: read udp packet: %w", err)
		}

		if !sameUDPAddr(from, peerAddr) {
			continue
		}

		if n >= len(punchMagic) && bytes.Equal(buffer[:len(punchMagic)], punchMagic) {
			return true, nil
		}
		if n > 0 {
			return true, nil
		}
	}

	select {
	case sendErr := <-errCh:
		return false, fmt.Errorf("punch: send bursts failed: %w", sendErr)
	default:
	}

	<-done
	return false, nil
}

// Conn returns the underlying UDP socket for tunnel handoff.
func (h *HolePuncher) Conn() *net.UDPConn {
	if h == nil {
		return nil
	}
	return h.conn
}

// LocalPort returns the currently bound local UDP port.
func (h *HolePuncher) LocalPort() int {
	if h == nil || h.conn == nil {
		return 0
	}
	if addr, ok := h.conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.Port
	}
	return 0
}

// Close releases the underlying UDP socket.
func (h *HolePuncher) Close() error {
	if h == nil || h.conn == nil {
		return nil
	}
	if err := h.conn.Close(); err != nil {
		return fmt.Errorf("close hole puncher: %w", err)
	}
	return nil
}

func (h *HolePuncher) sendPunchBursts(peerAddr *net.UDPAddr) error {
	for i := 0; i < 20; i++ {
		payload, err := buildPunchPayload()
		if err != nil {
			return fmt.Errorf("send punch bursts: build payload: %w", err)
		}

		if _, err := h.conn.WriteToUDP(payload, peerAddr); err != nil {
			return fmt.Errorf("send punch bursts: write udp packet: %w", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

func buildPunchPayload() ([]byte, error) {
	rnd := make([]byte, 8)
	if _, err := rand.Read(rnd); err != nil {
		return nil, fmt.Errorf("build punch payload: random bytes: %w", err)
	}

	payload := make([]byte, len(punchMagic)+8+8)
	copy(payload, punchMagic)
	binary.BigEndian.PutUint64(payload[len(punchMagic):], uint64(time.Now().UTC().UnixNano()))
	copy(payload[len(punchMagic)+8:], rnd)
	return payload, nil
}

func sameUDPAddr(a, b *net.UDPAddr) bool {
	if a == nil || b == nil {
		return false
	}
	return a.Port == b.Port && a.IP.Equal(b.IP)
}
