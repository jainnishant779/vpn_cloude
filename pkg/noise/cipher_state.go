package noise

import (
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	KeyLen = 32
)

// CipherState tracks AEAD key/nonce state for one traffic direction.
type CipherState struct {
	key    [KeyLen]byte
	nonce  uint64
	hasKey bool
	aead   cipher.AEAD
}

// InitializeKey sets the cipher key. A zero-length key puts the state in pass-through mode.
func (cs *CipherState) InitializeKey(key []byte) error {
	if len(key) == 0 {
		cs.key = [KeyLen]byte{}
		cs.nonce = 0
		cs.hasKey = false
		cs.aead = nil
		return nil
	}

	if len(key) != KeyLen {
		return fmt.Errorf("initialize key: expected %d-byte key, got %d", KeyLen, len(key))
	}

	copy(cs.key[:], key)
	aead, err := chacha20poly1305.New(cs.key[:])
	if err != nil {
		return fmt.Errorf("initialize key: create chacha20poly1305: %w", err)
	}

	cs.aead = aead
	cs.hasKey = true
	cs.nonce = 0
	return nil
}

// HasKey returns true when an AEAD key is configured.
func (cs *CipherState) HasKey() bool {
	return cs.hasKey
}

// Nonce returns the current nonce counter.
func (cs *CipherState) Nonce() uint64 {
	return cs.nonce
}

// SetNonce sets the current nonce counter.
func (cs *CipherState) SetNonce(nonce uint64) {
	cs.nonce = nonce
}

// EncryptWithAd encrypts plaintext with associated data.
func (cs *CipherState) EncryptWithAd(ad, plaintext []byte) ([]byte, error) {
	if !cs.hasKey {
		return cloneBytes(plaintext), nil
	}
	if cs.nonce == math.MaxUint64 {
		return nil, fmt.Errorf("encrypt with ad: nonce exhausted")
	}

	nonce := formatNonce(cs.nonce)
	ciphertext := cs.aead.Seal(nil, nonce[:], plaintext, ad)
	cs.nonce++
	return ciphertext, nil
}

// DecryptWithAd decrypts ciphertext with associated data.
func (cs *CipherState) DecryptWithAd(ad, ciphertext []byte) ([]byte, error) {
	if !cs.hasKey {
		return cloneBytes(ciphertext), nil
	}
	if cs.nonce == math.MaxUint64 {
		return nil, fmt.Errorf("decrypt with ad: nonce exhausted")
	}

	nonce := formatNonce(cs.nonce)
	plaintext, err := cs.aead.Open(nil, nonce[:], ciphertext, ad)
	if err != nil {
		return nil, fmt.Errorf("decrypt with ad: open ciphertext: %w", err)
	}
	cs.nonce++
	return plaintext, nil
}

// Rekey derives a fresh key from the current key using Noise rekey construction.
func (cs *CipherState) Rekey() error {
	if !cs.hasKey {
		return fmt.Errorf("rekey: cipher is not keyed")
	}

	maxNonce := formatNonce(math.MaxUint64)
	zeros := make([]byte, KeyLen)
	material := cs.aead.Seal(nil, maxNonce[:], zeros, nil)
	if len(material) < KeyLen {
		return fmt.Errorf("rekey: insufficient key material %d", len(material))
	}

	return cs.InitializeKey(material[:KeyLen])
}

func formatNonce(counter uint64) [chacha20poly1305.NonceSize]byte {
	var nonce [chacha20poly1305.NonceSize]byte
	binary.LittleEndian.PutUint64(nonce[4:], counter)
	return nonce
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
