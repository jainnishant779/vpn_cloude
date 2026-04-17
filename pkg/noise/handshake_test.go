package noise

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestCipherStatePassThrough(t *testing.T) {
	t.Parallel()

	var cs CipherState
	if err := cs.InitializeKey(nil); err != nil {
		t.Fatalf("initialize key: %v", err)
	}

	ad := []byte("ad")
	plaintext := []byte("hello")
	ciphertext, err := cs.EncryptWithAd(ad, plaintext)
	if err != nil {
		t.Fatalf("encrypt with ad: %v", err)
	}
	if !bytes.Equal(ciphertext, plaintext) {
		t.Fatalf("pass-through encrypt mismatch")
	}

	decoded, err := cs.DecryptWithAd(ad, ciphertext)
	if err != nil {
		t.Fatalf("decrypt with ad: %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("pass-through decrypt mismatch")
	}
}

func TestCipherStateKeyedRoundTripTamperAndRekey(t *testing.T) {
	t.Parallel()

	key := make([]byte, KeyLen)
	for i := range key {
		key[i] = byte(i + 1)
	}

	var sender CipherState
	if err := sender.InitializeKey(key); err != nil {
		t.Fatalf("sender initialize key: %v", err)
	}
	var receiver CipherState
	if err := receiver.InitializeKey(key); err != nil {
		t.Fatalf("receiver initialize key: %v", err)
	}

	ad := []byte("context")
	plaintext := []byte("payload-before-rekey")
	ciphertext, err := sender.EncryptWithAd(ad, plaintext)
	if err != nil {
		t.Fatalf("encrypt with ad: %v", err)
	}

	decoded, err := receiver.DecryptWithAd(ad, ciphertext)
	if err != nil {
		t.Fatalf("decrypt with ad: %v", err)
	}
	if !bytes.Equal(decoded, plaintext) {
		t.Fatalf("decoded payload mismatch")
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := receiver.DecryptWithAd(ad, tampered); err == nil {
		t.Fatalf("expected tampered ciphertext to fail")
	}

	if err := sender.Rekey(); err != nil {
		t.Fatalf("sender rekey: %v", err)
	}
	if err := receiver.Rekey(); err != nil {
		t.Fatalf("receiver rekey: %v", err)
	}

	afterRekey := []byte("payload-after-rekey")
	ctAfter, err := sender.EncryptWithAd(ad, afterRekey)
	if err != nil {
		t.Fatalf("encrypt after rekey: %v", err)
	}
	ptAfter, err := receiver.DecryptWithAd(ad, ctAfter)
	if err != nil {
		t.Fatalf("decrypt after rekey: %v", err)
	}
	if !bytes.Equal(ptAfter, afterRekey) {
		t.Fatalf("decoded payload after rekey mismatch")
	}
}

func TestCipherStateNonceExhaustion(t *testing.T) {
	t.Parallel()

	key := make([]byte, KeyLen)
	for i := range key {
		key[i] = byte(255 - i)
	}

	var cs CipherState
	if err := cs.InitializeKey(key); err != nil {
		t.Fatalf("initialize key: %v", err)
	}

	cs.SetNonce(math.MaxUint64)
	if _, err := cs.EncryptWithAd(nil, []byte("x")); err == nil {
		t.Fatalf("expected encrypt nonce exhaustion error")
	}
	if _, err := cs.DecryptWithAd(nil, []byte("x")); err == nil {
		t.Fatalf("expected decrypt nonce exhaustion error")
	}
}

func TestSymmetricStateSplitDeterminism(t *testing.T) {
	t.Parallel()

	var a SymmetricState
	if err := a.InitializeSymmetric([]byte(ProtocolName)); err != nil {
		t.Fatalf("initialize symmetric A: %v", err)
	}
	a.MixHash([]byte("mix-hash"))
	if err := a.MixKey([]byte("mix-key")); err != nil {
		t.Fatalf("mix key A: %v", err)
	}
	if err := a.MixKeyAndHash([]byte("mix-key-and-hash")); err != nil {
		t.Fatalf("mix key and hash A: %v", err)
	}

	var b SymmetricState
	if err := b.InitializeSymmetric([]byte(ProtocolName)); err != nil {
		t.Fatalf("initialize symmetric B: %v", err)
	}
	b.MixHash([]byte("mix-hash"))
	if err := b.MixKey([]byte("mix-key")); err != nil {
		t.Fatalf("mix key B: %v", err)
	}
	if err := b.MixKeyAndHash([]byte("mix-key-and-hash")); err != nil {
		t.Fatalf("mix key and hash B: %v", err)
	}

	if !bytes.Equal(a.HandshakeHash(), b.HandshakeHash()) {
		t.Fatalf("handshake hash mismatch")
	}
	if !bytes.Equal(a.ChainingKey(), b.ChainingKey()) {
		t.Fatalf("chaining key mismatch")
	}

	a1, a2, err := a.Split()
	if err != nil {
		t.Fatalf("split A: %v", err)
	}
	b1, b2, err := b.Split()
	if err != nil {
		t.Fatalf("split B: %v", err)
	}

	pt1 := []byte("channel-one")
	ct1, err := a1.EncryptWithAd(nil, pt1)
	if err != nil {
		t.Fatalf("encrypt channel one: %v", err)
	}
	dec1, err := b1.DecryptWithAd(nil, ct1)
	if err != nil {
		t.Fatalf("decrypt channel one: %v", err)
	}
	if !bytes.Equal(dec1, pt1) {
		t.Fatalf("channel one payload mismatch")
	}

	pt2 := []byte("channel-two")
	ct2, err := a2.EncryptWithAd(nil, pt2)
	if err != nil {
		t.Fatalf("encrypt channel two: %v", err)
	}
	dec2, err := b2.DecryptWithAd(nil, ct2)
	if err != nil {
		t.Fatalf("decrypt channel two: %v", err)
	}
	if !bytes.Equal(dec2, pt2) {
		t.Fatalf("channel two payload mismatch")
	}
}

func TestHandshakeIKRoundTrip(t *testing.T) {
	t.Parallel()

	initStatic := fixedPrivate(0x11)
	respStatic := fixedPrivate(0x33)
	initPub, err := publicFromPrivate(initStatic)
	if err != nil {
		t.Fatalf("derive initiator static public key: %v", err)
	}
	respPub, err := publicFromPrivate(respStatic)
	if err != nil {
		t.Fatalf("derive responder static public key: %v", err)
	}

	initiator, err := NewHandshakeState(Initiator, initStatic, respPub, nil)
	if err != nil {
		t.Fatalf("new initiator handshake state: %v", err)
	}
	responder, err := NewHandshakeState(Responder, respStatic, initPub, nil)
	if err != nil {
		t.Fatalf("new responder handshake state: %v", err)
	}

	initiator.SetEphemeral(fixedPrivate(0x55))
	responder.SetEphemeral(fixedPrivate(0x77))

	payloadA := []byte("payload-from-initiator")
	msgA, err := initiator.WriteMessageA(payloadA)
	if err != nil {
		t.Fatalf("write message A: %v", err)
	}
	gotA, err := responder.ReadMessageA(msgA)
	if err != nil {
		t.Fatalf("read message A: %v", err)
	}
	if !bytes.Equal(gotA, payloadA) {
		t.Fatalf("message A payload mismatch")
	}

	payloadB := []byte("payload-from-responder")
	msgB, err := responder.WriteMessageB(payloadB)
	if err != nil {
		t.Fatalf("write message B: %v", err)
	}
	gotB, err := initiator.ReadMessageB(msgB)
	if err != nil {
		t.Fatalf("read message B: %v", err)
	}
	if !bytes.Equal(gotB, payloadB) {
		t.Fatalf("message B payload mismatch")
	}

	if !bytes.Equal(initiator.HandshakeHash(), responder.HandshakeHash()) {
		t.Fatalf("transcript hash mismatch")
	}

	initSend, initRecv, err := initiator.Transport()
	if err != nil {
		t.Fatalf("initiator transport split: %v", err)
	}
	respSend, respRecv, err := responder.Transport()
	if err != nil {
		t.Fatalf("responder transport split: %v", err)
	}

	pt1 := []byte("transport-initiator-to-responder")
	ct1, err := initSend.EncryptWithAd(nil, pt1)
	if err != nil {
		t.Fatalf("encrypt i->r: %v", err)
	}
	dec1, err := respRecv.DecryptWithAd(nil, ct1)
	if err != nil {
		t.Fatalf("decrypt i->r: %v", err)
	}
	if !bytes.Equal(dec1, pt1) {
		t.Fatalf("transport i->r mismatch")
	}

	pt2 := []byte("transport-responder-to-initiator")
	ct2, err := respSend.EncryptWithAd(nil, pt2)
	if err != nil {
		t.Fatalf("encrypt r->i: %v", err)
	}
	dec2, err := initRecv.DecryptWithAd(nil, ct2)
	if err != nil {
		t.Fatalf("decrypt r->i: %v", err)
	}
	if !bytes.Equal(dec2, pt2) {
		t.Fatalf("transport r->i mismatch")
	}
}

func TestHandshakeIKWithOptionalPSK(t *testing.T) {
	t.Parallel()

	initStatic := fixedPrivate(0x21)
	respStatic := fixedPrivate(0x43)
	initPub, err := publicFromPrivate(initStatic)
	if err != nil {
		t.Fatalf("derive initiator static public key: %v", err)
	}
	respPub, err := publicFromPrivate(respStatic)
	if err != nil {
		t.Fatalf("derive responder static public key: %v", err)
	}

	psk := make([]byte, KeyLen)
	for i := range psk {
		psk[i] = byte(i)
	}

	initiator, err := NewHandshakeState(Initiator, initStatic, respPub, psk)
	if err != nil {
		t.Fatalf("new initiator handshake state: %v", err)
	}
	responder, err := NewHandshakeState(Responder, respStatic, initPub, psk)
	if err != nil {
		t.Fatalf("new responder handshake state: %v", err)
	}

	initiator.SetEphemeral(fixedPrivate(0x65))
	responder.SetEphemeral(fixedPrivate(0x87))

	msgA, err := initiator.WriteMessageA([]byte("hello"))
	if err != nil {
		t.Fatalf("write message A: %v", err)
	}
	if _, err := responder.ReadMessageA(msgA); err != nil {
		t.Fatalf("read message A: %v", err)
	}

	msgB, err := responder.WriteMessageB([]byte("world"))
	if err != nil {
		t.Fatalf("write message B: %v", err)
	}
	if _, err := initiator.ReadMessageB(msgB); err != nil {
		t.Fatalf("read message B: %v", err)
	}

	if !bytes.Equal(initiator.HandshakeHash(), responder.HandshakeHash()) {
		t.Fatalf("transcript hash mismatch with psk")
	}
}

func TestReplayWindowBehavior(t *testing.T) {
	t.Parallel()

	rw := NewReplayWindow()
	if !rw.Accept(0) {
		t.Fatalf("expected first counter to be accepted")
	}
	if rw.Accept(0) {
		t.Fatalf("expected duplicate counter to be rejected")
	}
	if !rw.Accept(1) {
		t.Fatalf("expected next counter to be accepted")
	}

	if !rw.Accept(3000) {
		t.Fatalf("expected far-ahead counter to be accepted")
	}
	if rw.Accept(900) {
		t.Fatalf("expected stale counter outside window to be rejected")
	}

	if !rw.Accept(2999) {
		t.Fatalf("expected near-latest unseen counter to be accepted")
	}
	if rw.Accept(2999) {
		t.Fatalf("expected duplicate near-latest counter to be rejected")
	}
}

func TestHandshakeTransportAndReplayHighVolume(t *testing.T) {
	t.Parallel()

	initStatic := fixedPrivate(0x19)
	respStatic := fixedPrivate(0x39)
	initPub, err := publicFromPrivate(initStatic)
	if err != nil {
		t.Fatalf("derive initiator static public key: %v", err)
	}
	respPub, err := publicFromPrivate(respStatic)
	if err != nil {
		t.Fatalf("derive responder static public key: %v", err)
	}

	initiator, err := NewHandshakeState(Initiator, initStatic, respPub, nil)
	if err != nil {
		t.Fatalf("new initiator handshake state: %v", err)
	}
	responder, err := NewHandshakeState(Responder, respStatic, initPub, nil)
	if err != nil {
		t.Fatalf("new responder handshake state: %v", err)
	}

	initiator.SetEphemeral(fixedPrivate(0x59))
	responder.SetEphemeral(fixedPrivate(0x79))

	msgA, err := initiator.WriteMessageA([]byte("A"))
	if err != nil {
		t.Fatalf("write message A: %v", err)
	}
	if _, err := responder.ReadMessageA(msgA); err != nil {
		t.Fatalf("read message A: %v", err)
	}

	msgB, err := responder.WriteMessageB([]byte("B"))
	if err != nil {
		t.Fatalf("write message B: %v", err)
	}
	if _, err := initiator.ReadMessageB(msgB); err != nil {
		t.Fatalf("read message B: %v", err)
	}

	send, _, err := initiator.Transport()
	if err != nil {
		t.Fatalf("initiator transport split: %v", err)
	}
	_, recv, err := responder.Transport()
	if err != nil {
		t.Fatalf("responder transport split: %v", err)
	}

	replay := NewReplayWindow()
	for i := uint64(0); i < 1500; i++ {
		payload := make([]byte, 12)
		binary.LittleEndian.PutUint64(payload[:8], i)
		copy(payload[8:], []byte("pkt"))

		ciphertext, err := send.EncryptWithAd(nil, payload)
		if err != nil {
			t.Fatalf("encrypt packet %d: %v", i, err)
		}
		decoded, err := recv.DecryptWithAd(nil, ciphertext)
		if err != nil {
			t.Fatalf("decrypt packet %d: %v", i, err)
		}
		if !bytes.Equal(decoded, payload) {
			t.Fatalf("payload mismatch at counter %d", i)
		}

		if !replay.Accept(i) {
			t.Fatalf("replay window rejected fresh counter %d", i)
		}
	}

	for i := uint64(1499); i > 1450; i-- {
		if replay.Accept(i) {
			t.Fatalf("replay window accepted duplicate counter %d", i)
		}
	}
}

func fixedPrivate(seed byte) [KeyLen]byte {
	var out [KeyLen]byte
	for i := range out {
		out[i] = seed + byte(i)
	}
	clampScalar(out[:])
	return out
}
