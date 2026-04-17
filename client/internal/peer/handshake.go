package peer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"
)

const handshakeMagic = "QTHS1"

type handshakeMessage struct {
	Magic     string `json:"magic"`
	PeerID    string `json:"peer_id"`
	Timestamp int64  `json:"timestamp"`
	HMAC      string `json:"hmac"`
}

// BuildHandshakePacket creates a signed UDP handshake payload.
func BuildHandshakePacket(peerID string, secret []byte) ([]byte, error) {
	if peerID == "" {
		return nil, fmt.Errorf("build handshake packet: peer id is required")
	}
	if len(secret) == 0 {
		return nil, fmt.Errorf("build handshake packet: secret is required")
	}

	ts := time.Now().UTC().Unix()
	mac := computeHMAC(peerID, ts, secret)

	payload, err := json.Marshal(handshakeMessage{
		Magic:     handshakeMagic,
		PeerID:    peerID,
		Timestamp: ts,
		HMAC:      hex.EncodeToString(mac),
	})
	if err != nil {
		return nil, fmt.Errorf("build handshake packet: marshal message: %w", err)
	}
	return payload, nil
}

// VerifyHandshakePacket validates signature and freshness of handshake payload.
func VerifyHandshakePacket(packet []byte, secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("verify handshake packet: secret is required")
	}

	var msg handshakeMessage
	if err := json.Unmarshal(packet, &msg); err != nil {
		return "", fmt.Errorf("verify handshake packet: unmarshal packet: %w", err)
	}

	if msg.Magic != handshakeMagic {
		return "", fmt.Errorf("verify handshake packet: invalid magic")
	}
	if msg.PeerID == "" {
		return "", fmt.Errorf("verify handshake packet: missing peer id")
	}
	if time.Since(time.Unix(msg.Timestamp, 0).UTC()) > 30*time.Second {
		return "", fmt.Errorf("verify handshake packet: stale timestamp")
	}

	signature, err := hex.DecodeString(msg.HMAC)
	if err != nil {
		return "", fmt.Errorf("verify handshake packet: decode hmac: %w", err)
	}

	expected := computeHMAC(msg.PeerID, msg.Timestamp, secret)
	if !hmac.Equal(signature, expected) {
		return "", fmt.Errorf("verify handshake packet: invalid hmac")
	}

	return msg.PeerID, nil
}

// ExchangeHandshake sends a handshake packet and waits for a valid response.
func ExchangeHandshake(conn *net.UDPConn, peerAddr *net.UDPAddr, peerID string, secret []byte) error {
	if conn == nil {
		return fmt.Errorf("exchange handshake: connection is nil")
	}
	if peerAddr == nil {
		return fmt.Errorf("exchange handshake: peer address is nil")
	}

	packet, err := BuildHandshakePacket(peerID, secret)
	if err != nil {
		return fmt.Errorf("exchange handshake: build handshake packet: %w", err)
	}

	if _, err := conn.WriteToUDP(packet, peerAddr); err != nil {
		return fmt.Errorf("exchange handshake: send packet: %w", err)
	}

	buffer := make([]byte, 2048)
	if err := conn.SetReadDeadline(time.Now().Add(1200 * time.Millisecond)); err != nil {
		return fmt.Errorf("exchange handshake: set read deadline: %w", err)
	}

	n, from, err := conn.ReadFromUDP(buffer)
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil
		}
		return fmt.Errorf("exchange handshake: read response: %w", err)
	}

	if !from.IP.Equal(peerAddr.IP) || from.Port != peerAddr.Port {
		return fmt.Errorf("exchange handshake: response from unexpected peer")
	}

	if _, err := VerifyHandshakePacket(buffer[:n], secret); err != nil {
		return fmt.Errorf("exchange handshake: verify response: %w", err)
	}
	return nil
}

func computeHMAC(peerID string, timestamp int64, secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(handshakeMagic))
	mac.Write([]byte("|"))
	mac.Write([]byte(peerID))
	mac.Write([]byte("|"))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	return mac.Sum(nil)
}
