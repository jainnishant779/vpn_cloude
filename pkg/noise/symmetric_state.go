package noise

import (
	"fmt"
	"hash"
	"io"

	"golang.org/x/crypto/blake2s"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	HashLen = 32
)

// SymmetricState wraps handshake hash/chaining-key evolution plus a CipherState.
type SymmetricState struct {
	cs CipherState
	ck [HashLen]byte
	h  [HashLen]byte
}

// InitializeSymmetric initializes hash/chaining-key from protocol name.
func (ss *SymmetricState) InitializeSymmetric(protocolName []byte) error {
	if len(protocolName) > HashLen {
		h := blake2s.Sum256(protocolName)
		ss.h = h
	} else {
		ss.h = [HashLen]byte{}
		copy(ss.h[:], protocolName)
	}

	ss.ck = ss.h
	if err := ss.cs.InitializeKey(nil); err != nil {
		return fmt.Errorf("initialize symmetric: initialize cipher state: %w", err)
	}
	return nil
}

// MixHash updates the running handshake hash as H(h || data).
func (ss *SymmetricState) MixHash(data []byte) {
	h := newHash()
	h.Write(ss.h[:])
	h.Write(data)
	copy(ss.h[:], h.Sum(nil))
}

// MixKey updates chaining key and active cipher key from input key material.
func (ss *SymmetricState) MixKey(input []byte) error {
	material, err := hkdfMaterial(ss.ck[:], input, 2)
	if err != nil {
		return fmt.Errorf("mix key: hkdf material: %w", err)
	}

	copy(ss.ck[:], material[0])
	if err := ss.cs.InitializeKey(material[1]); err != nil {
		return fmt.Errorf("mix key: initialize cipher key: %w", err)
	}
	return nil
}

// MixKeyAndHash mixes input key material into chaining key, handshake hash, and cipher key.
func (ss *SymmetricState) MixKeyAndHash(input []byte) error {
	material, err := hkdfMaterial(ss.ck[:], input, 3)
	if err != nil {
		return fmt.Errorf("mix key and hash: hkdf material: %w", err)
	}

	copy(ss.ck[:], material[0])
	ss.MixHash(material[1])
	if err := ss.cs.InitializeKey(material[2]); err != nil {
		return fmt.Errorf("mix key and hash: initialize cipher key: %w", err)
	}
	return nil
}

// EncryptAndHash encrypts plaintext using current handshake hash as AD, then mixes ciphertext into hash.
func (ss *SymmetricState) EncryptAndHash(plaintext []byte) ([]byte, error) {
	ciphertext, err := ss.cs.EncryptWithAd(ss.h[:], plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt and hash: encrypt with ad: %w", err)
	}
	ss.MixHash(ciphertext)
	return ciphertext, nil
}

// DecryptAndHash decrypts ciphertext using current handshake hash as AD, then mixes ciphertext into hash.
func (ss *SymmetricState) DecryptAndHash(ciphertext []byte) ([]byte, error) {
	plaintext, err := ss.cs.DecryptWithAd(ss.h[:], ciphertext)
	if err != nil {
		return nil, fmt.Errorf("decrypt and hash: decrypt with ad: %w", err)
	}
	ss.MixHash(ciphertext)
	return plaintext, nil
}

// Split derives two transport CipherStates.
func (ss *SymmetricState) Split() (CipherState, CipherState, error) {
	material, err := hkdfMaterial(ss.ck[:], nil, 2)
	if err != nil {
		return CipherState{}, CipherState{}, fmt.Errorf("split: hkdf material: %w", err)
	}

	var c1 CipherState
	if err := c1.InitializeKey(material[0]); err != nil {
		return CipherState{}, CipherState{}, fmt.Errorf("split: initialize first cipher: %w", err)
	}

	var c2 CipherState
	if err := c2.InitializeKey(material[1]); err != nil {
		return CipherState{}, CipherState{}, fmt.Errorf("split: initialize second cipher: %w", err)
	}

	return c1, c2, nil
}

// HandshakeHash returns a copy of the current handshake hash.
func (ss *SymmetricState) HandshakeHash() []byte {
	out := make([]byte, HashLen)
	copy(out, ss.h[:])
	return out
}

// ChainingKey returns a copy of the current chaining key.
func (ss *SymmetricState) ChainingKey() []byte {
	out := make([]byte, HashLen)
	copy(out, ss.ck[:])
	return out
}

// CipherState returns the active handshake CipherState.
func (ss *SymmetricState) CipherState() *CipherState {
	return &ss.cs
}

func newHash() hash.Hash {
	h, _ := blake2s.New256(nil)
	return h
}

func hkdfMaterial(chainingKey, input []byte, parts int) ([][]byte, error) {
	if parts <= 0 {
		return nil, fmt.Errorf("hkdf material: parts must be positive")
	}

	reader := hkdf.New(newHash, input, chainingKey, nil)
	result := make([][]byte, 0, parts)
	for i := 0; i < parts; i++ {
		block := make([]byte, HashLen)
		if _, err := io.ReadFull(reader, block); err != nil {
			return nil, fmt.Errorf("hkdf material: read block %d: %w", i, err)
		}
		result = append(result, block)
	}
	return result, nil
}

func encryptedLen(plainLen int, keyed bool) int {
	if keyed {
		return plainLen + chacha20poly1305.Overhead
	}
	return plainLen
}
