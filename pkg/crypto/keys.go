package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/crypto/curve25519"
)

// GenerateKeyPair returns WireGuard-compatible Curve25519 keys encoded in base64.
func GenerateKeyPair() (privateKey string, publicKey string, err error) {
	var private [32]byte
	if _, err := rand.Read(private[:]); err != nil {
		return "", "", fmt.Errorf("generate key pair: read random bytes: %w", err)
	}

	clampPrivateKey(private[:])

	public, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		return "", "", fmt.Errorf("generate key pair: derive public key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(private[:]), base64.StdEncoding.EncodeToString(public), nil
}

// MachineFingerprint creates a stable identifier without requiring privileged calls.
func MachineFingerprint() string {
	parts := []string{runtime.GOOS, runtime.GOARCH}

	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		parts = append(parts, strings.ToLower(hostname))
	}

	interfaces, err := net.Interfaces()
	if err == nil {
		macs := make([]string, 0, len(interfaces))
		for _, iface := range interfaces {
			if iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			if len(iface.HardwareAddr) == 0 {
				continue
			}
			macs = append(macs, strings.ToLower(iface.HardwareAddr.String()))
		}
		sort.Strings(macs)
		parts = append(parts, macs...)
	}

	seed := strings.Join(parts, "|")
	if seed == "" {
		seed = "quicktunnel-fallback"
	}

	sum := sha256.Sum256([]byte(seed))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

func clampPrivateKey(private []byte) {
	private[0] &= 248
	private[31] &= 127
	private[31] |= 64
}
