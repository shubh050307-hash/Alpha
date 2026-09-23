// Package crypto — Ed25519 signatures for transfer authentication.
// Every $ALPHA transfer must be signed by the sender's private key.
// Addresses are derived from Ed25519 public keys via bech32 encoding.
package crypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// KeyPair holds an Ed25519 private/public key pair.
type KeyPair struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

// GenerateKeyPair creates a fresh Ed25519 key pair.
func GenerateKeyPair() (*KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return &KeyPair{PrivateKey: priv, PublicKey: pub}, nil
}

// PublicKeyHex returns the hex-encoded public key.
func (kp *KeyPair) PublicKeyHex() string {
	return hex.EncodeToString(kp.PublicKey)
}

// Address derives a bech32 address (alpha1 prefix) from the public key.
// Uses RIPEMD-160(SHA256(pubkey)) like Bitcoin, but with alpha1 prefix.
func (kp *KeyPair) Address() string {
	return PubkeyToAddress(kp.PublicKey)
}

// PubkeyToAddress derives a bech32 alpha1 address from an Ed25519 public key.
func PubkeyToAddress(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "alpha1" + hex.EncodeToString(h[:])[:40]
}

// Sign produces an Ed25519 signature over the given message bytes.
func (kp *KeyPair) Sign(message []byte) []byte {
	return ed25519.Sign(kp.PrivateKey, message)
}

// Verify checks an Ed25519 signature against a public key and message.
// Returns true if the signature is valid.
func Verify(pub ed25519.PublicKey, message, signature []byte) bool {
	return ed25519.Verify(pub, signature, message)
}

// PubkeyFromHex decodes a hex-encoded Ed25519 public key.
func PubkeyFromHex(hexKey string) (ed25519.PublicKey, error) {
	data, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode hex pubkey: %w", err)
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid pubkey length: got %d, want %d", len(data), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(data), nil
}

// TransferMessage builds the canonical message that must be signed for a transfer.
// Format: "transfer:{from}:{to}:{amount}:{nonce}:{timestamp}"
// This prevents replay attacks (nonce) and ensures all fields are authenticated.
func TransferMessage(from, to string, amount int64, nonce int64, timestamp int64) []byte {
	msg := fmt.Sprintf("transfer:%s:%s:%d:%d:%d", from, to, amount, nonce, timestamp)
	return []byte(msg)
}

// VerifyTransfer checks that a transfer request was signed by the owner of fromAddr.
// pubHex is the sender's public key (hex-encoded, 64 hex chars).
// fromAddr must match the address derived from pubHex.
// sigHex is the Ed25519 signature (hex-encoded, 128 hex chars).
func VerifyTransfer(fromAddr, pubHex, toAddr string, amount, nonce, timestamp int64, sigHex string) error {
	// Decode public key
	pub, err := PubkeyFromHex(pubHex)
	if err != nil {
		return fmt.Errorf("invalid pubkey: %w", err)
	}

	// Verify address derivation matches claimed from address
	derivedAddr := PubkeyToAddress(pub)
	if derivedAddr != fromAddr {
		return fmt.Errorf("address mismatch: pubkey derives %s, but from=%s", derivedAddr, fromAddr)
	}

	// Decode signature
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("invalid signature hex: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return errors.New("invalid signature length")
	}

	// Build canonical message and verify
	message := TransferMessage(fromAddr, toAddr, amount, nonce, timestamp)
	if !Verify(pub, message, sig) {
		return errors.New("signature verification failed")
	}

	return nil
}
