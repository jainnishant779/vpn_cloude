package noise

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/crypto/curve25519"
)

const (
	ProtocolName = "Noise_IK_25519_ChaChaPoly_BLAKE2s"
)

// Role determines handshake behavior for message read/write operations.
type Role int

const (
	Initiator Role = iota
	Responder
)

// HandshakeState tracks IK handshake state for one side.
type HandshakeState struct {
	ss    SymmetricState
	role  Role
	s     [KeyLen]byte // local static private key
	e     [KeyLen]byte // local ephemeral private key
	rs    [KeyLen]byte // remote static public key (pre-shared in IK)
	re    [KeyLen]byte // remote ephemeral public key
	hasE  bool
	hasRE bool
	psk   []byte
}

// NewHandshakeState initializes an IK handshake state.
func NewHandshakeState(role Role, localStatic [KeyLen]byte, remoteStatic [KeyLen]byte, psk []byte) (*HandshakeState, error) {
	if role != Initiator && role != Responder {
		return nil, fmt.Errorf("new handshake state: invalid role %d", role)
	}
	if len(psk) > 0 && len(psk) != KeyLen {
		return nil, fmt.Errorf("new handshake state: optional psk must be %d bytes", KeyLen)
	}

	hs := &HandshakeState{
		role: role,
		s:    localStatic,
		rs:   remoteStatic,
		psk:  cloneBytes(psk),
	}

	if err := hs.ss.InitializeSymmetric([]byte(ProtocolName)); err != nil {
		return nil, fmt.Errorf("new handshake state: initialize symmetric state: %w", err)
	}

	localPub, err := publicFromPrivate(hs.s)
	if err != nil {
		return nil, fmt.Errorf("new handshake state: derive local static public key: %w", err)
	}

	if role == Initiator {
		hs.ss.MixHash(localPub[:])
		hs.ss.MixHash(hs.rs[:])
	} else {
		hs.ss.MixHash(hs.rs[:])
		hs.ss.MixHash(localPub[:])
	}

	return hs, nil
}

// SetEphemeral allows deterministic ephemeral key injection.
func (hs *HandshakeState) SetEphemeral(priv [KeyLen]byte) {
	hs.e = priv
	hs.hasE = true
}

// GenerateEphemeral creates a fresh local ephemeral private key.
func (hs *HandshakeState) GenerateEphemeral(source io.Reader) error {
	if source == nil {
		source = rand.Reader
	}

	var priv [KeyLen]byte
	if _, err := io.ReadFull(source, priv[:]); err != nil {
		return fmt.Errorf("generate ephemeral: read random bytes: %w", err)
	}
	clampScalar(priv[:])

	hs.e = priv
	hs.hasE = true
	return nil
}

// WriteMessageA writes the initiator IK first message: e, es, s, ss, payload.
func (hs *HandshakeState) WriteMessageA(payload []byte) ([]byte, error) {
	if hs.role != Initiator {
		return nil, fmt.Errorf("write message A: invalid role")
	}
	if !hs.hasE {
		if err := hs.GenerateEphemeral(nil); err != nil {
			return nil, fmt.Errorf("write message A: generate ephemeral: %w", err)
		}
	}

	ePub, err := publicFromPrivate(hs.e)
	if err != nil {
		return nil, fmt.Errorf("write message A: derive ephemeral public key: %w", err)
	}
	hs.ss.MixHash(ePub[:])
	if hs.hasPSK() {
		if err := hs.ss.MixKey(ePub[:]); err != nil {
			return nil, fmt.Errorf("write message A: mix key for psk mode: %w", err)
		}
	}

	dhES, err := dh(hs.e, hs.rs)
	if err != nil {
		return nil, fmt.Errorf("write message A: compute dh es: %w", err)
	}
	if err := hs.ss.MixKey(dhES[:]); err != nil {
		return nil, fmt.Errorf("write message A: mix key es: %w", err)
	}

	sPub, err := publicFromPrivate(hs.s)
	if err != nil {
		return nil, fmt.Errorf("write message A: derive static public key: %w", err)
	}
	encryptedStatic, err := hs.ss.EncryptAndHash(sPub[:])
	if err != nil {
		return nil, fmt.Errorf("write message A: encrypt static key: %w", err)
	}

	dhSS, err := dh(hs.s, hs.rs)
	if err != nil {
		return nil, fmt.Errorf("write message A: compute dh ss: %w", err)
	}
	if err := hs.ss.MixKey(dhSS[:]); err != nil {
		return nil, fmt.Errorf("write message A: mix key ss: %w", err)
	}

	encryptedPayload, err := hs.ss.EncryptAndHash(payload)
	if err != nil {
		return nil, fmt.Errorf("write message A: encrypt payload: %w", err)
	}

	message := make([]byte, 0, len(ePub)+len(encryptedStatic)+len(encryptedPayload))
	message = append(message, ePub[:]...)
	message = append(message, encryptedStatic...)
	message = append(message, encryptedPayload...)
	return message, nil
}

// ReadMessageA reads the responder IK first message and returns payload.
func (hs *HandshakeState) ReadMessageA(message []byte) ([]byte, error) {
	if hs.role != Responder {
		return nil, fmt.Errorf("read message A: invalid role")
	}
	if len(message) < KeyLen {
		return nil, fmt.Errorf("read message A: message too short")
	}

	index := 0
	copy(hs.re[:], message[index:index+KeyLen])
	hs.hasRE = true
	index += KeyLen
	hs.ss.MixHash(hs.re[:])
	if hs.hasPSK() {
		if err := hs.ss.MixKey(hs.re[:]); err != nil {
			return nil, fmt.Errorf("read message A: mix key for psk mode: %w", err)
		}
	}

	dhES, err := dh(hs.s, hs.re)
	if err != nil {
		return nil, fmt.Errorf("read message A: compute dh es: %w", err)
	}
	if err := hs.ss.MixKey(dhES[:]); err != nil {
		return nil, fmt.Errorf("read message A: mix key es: %w", err)
	}

	encryptedStaticLen := encryptedLen(KeyLen, hs.ss.CipherState().HasKey())
	if len(message[index:]) < encryptedStaticLen {
		return nil, fmt.Errorf("read message A: missing encrypted static key")
	}
	encryptedStatic := message[index : index+encryptedStaticLen]
	index += encryptedStaticLen

	remoteStatic, err := hs.ss.DecryptAndHash(encryptedStatic)
	if err != nil {
		return nil, fmt.Errorf("read message A: decrypt remote static key: %w", err)
	}
	if len(remoteStatic) != KeyLen {
		return nil, fmt.Errorf("read message A: decrypted static key length %d", len(remoteStatic))
	}
	if !bytes.Equal(remoteStatic, hs.rs[:]) {
		return nil, fmt.Errorf("read message A: remote static key mismatch")
	}

	dhSS, err := dh(hs.s, hs.rs)
	if err != nil {
		return nil, fmt.Errorf("read message A: compute dh ss: %w", err)
	}
	if err := hs.ss.MixKey(dhSS[:]); err != nil {
		return nil, fmt.Errorf("read message A: mix key ss: %w", err)
	}

	payload, err := hs.ss.DecryptAndHash(message[index:])
	if err != nil {
		return nil, fmt.Errorf("read message A: decrypt payload: %w", err)
	}
	return payload, nil
}

// WriteMessageB writes the responder IK second message: e, ee, se, [psk], payload.
func (hs *HandshakeState) WriteMessageB(payload []byte) ([]byte, error) {
	if hs.role != Responder {
		return nil, fmt.Errorf("write message B: invalid role")
	}
	if !hs.hasRE {
		return nil, fmt.Errorf("write message B: message A has not been read")
	}
	if !hs.hasE {
		if err := hs.GenerateEphemeral(nil); err != nil {
			return nil, fmt.Errorf("write message B: generate ephemeral: %w", err)
		}
	}

	ePub, err := publicFromPrivate(hs.e)
	if err != nil {
		return nil, fmt.Errorf("write message B: derive ephemeral public key: %w", err)
	}
	hs.ss.MixHash(ePub[:])
	if hs.hasPSK() {
		if err := hs.ss.MixKey(ePub[:]); err != nil {
			return nil, fmt.Errorf("write message B: mix key for psk mode: %w", err)
		}
	}

	dhEE, err := dh(hs.e, hs.re)
	if err != nil {
		return nil, fmt.Errorf("write message B: compute dh ee: %w", err)
	}
	if err := hs.ss.MixKey(dhEE[:]); err != nil {
		return nil, fmt.Errorf("write message B: mix key ee: %w", err)
	}

	dhSE, err := dh(hs.e, hs.rs)
	if err != nil {
		return nil, fmt.Errorf("write message B: compute dh se: %w", err)
	}
	if err := hs.ss.MixKey(dhSE[:]); err != nil {
		return nil, fmt.Errorf("write message B: mix key se: %w", err)
	}

	if err := hs.mixPSK(); err != nil {
		return nil, fmt.Errorf("write message B: mix psk: %w", err)
	}

	encryptedPayload, err := hs.ss.EncryptAndHash(payload)
	if err != nil {
		return nil, fmt.Errorf("write message B: encrypt payload: %w", err)
	}

	message := make([]byte, 0, len(ePub)+len(encryptedPayload))
	message = append(message, ePub[:]...)
	message = append(message, encryptedPayload...)
	return message, nil
}

// ReadMessageB reads the initiator IK second message and returns payload.
func (hs *HandshakeState) ReadMessageB(message []byte) ([]byte, error) {
	if hs.role != Initiator {
		return nil, fmt.Errorf("read message B: invalid role")
	}
	if !hs.hasE {
		return nil, fmt.Errorf("read message B: message A has not been written")
	}
	if len(message) < KeyLen {
		return nil, fmt.Errorf("read message B: message too short")
	}

	index := 0
	copy(hs.re[:], message[index:index+KeyLen])
	hs.hasRE = true
	index += KeyLen
	hs.ss.MixHash(hs.re[:])
	if hs.hasPSK() {
		if err := hs.ss.MixKey(hs.re[:]); err != nil {
			return nil, fmt.Errorf("read message B: mix key for psk mode: %w", err)
		}
	}

	dhEE, err := dh(hs.e, hs.re)
	if err != nil {
		return nil, fmt.Errorf("read message B: compute dh ee: %w", err)
	}
	if err := hs.ss.MixKey(dhEE[:]); err != nil {
		return nil, fmt.Errorf("read message B: mix key ee: %w", err)
	}

	dhSE, err := dh(hs.s, hs.re)
	if err != nil {
		return nil, fmt.Errorf("read message B: compute dh se: %w", err)
	}
	if err := hs.ss.MixKey(dhSE[:]); err != nil {
		return nil, fmt.Errorf("read message B: mix key se: %w", err)
	}

	if err := hs.mixPSK(); err != nil {
		return nil, fmt.Errorf("read message B: mix psk: %w", err)
	}

	payload, err := hs.ss.DecryptAndHash(message[index:])
	if err != nil {
		return nil, fmt.Errorf("read message B: decrypt payload: %w", err)
	}
	return payload, nil
}

// Transport returns send and receive ciphers after handshake completion.
func (hs *HandshakeState) Transport() (CipherState, CipherState, error) {
	c1, c2, err := hs.ss.Split()
	if err != nil {
		return CipherState{}, CipherState{}, fmt.Errorf("transport: split symmetric state: %w", err)
	}

	if hs.role == Initiator {
		return c1, c2, nil
	}
	return c2, c1, nil
}

// HandshakeHash returns the transcript hash for diagnostics and binding checks.
func (hs *HandshakeState) HandshakeHash() []byte {
	return hs.ss.HandshakeHash()
}

func (hs *HandshakeState) mixPSK() error {
	if len(hs.psk) == 0 {
		return nil
	}
	return hs.ss.MixKeyAndHash(hs.psk)
}

func (hs *HandshakeState) hasPSK() bool {
	return len(hs.psk) > 0
}

func publicFromPrivate(priv [KeyLen]byte) ([KeyLen]byte, error) {
	var pub [KeyLen]byte
	clamped := priv
	clampScalar(clamped[:])

	raw, err := curve25519.X25519(clamped[:], curve25519.Basepoint)
	if err != nil {
		return pub, fmt.Errorf("public from private: x25519: %w", err)
	}
	copy(pub[:], raw)
	return pub, nil
}

func dh(localPrivate [KeyLen]byte, remotePublic [KeyLen]byte) ([KeyLen]byte, error) {
	var out [KeyLen]byte
	clamped := localPrivate
	clampScalar(clamped[:])

	raw, err := curve25519.X25519(clamped[:], remotePublic[:])
	if err != nil {
		return out, fmt.Errorf("dh: x25519: %w", err)
	}
	copy(out[:], raw)
	return out, nil
}

func clampScalar(s []byte) {
	s[0] &= 248
	s[31] &= 127
	s[31] |= 64
}
