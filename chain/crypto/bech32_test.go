package crypto

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		data   []byte
	}{
		{"zero bytes", "alpha", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{"all ones", "alpha", []byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255}},
		{"sequential", "alpha", []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}},
		{"test prefix", "test", []byte{0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := Encode(tc.prefix, tc.data)
			if err != nil {
				t.Fatalf("Encode failed: %v", err)
			}

			hrp, decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}

			if hrp != tc.prefix {
				t.Errorf("hrp mismatch: got %q want %q", hrp, tc.prefix)
			}

			if hex.EncodeToString(decoded) != hex.EncodeToString(tc.data) {
				t.Errorf("data mismatch: got %x want %x", decoded, tc.data)
			}
		})
	}
}

func TestDecodeInvalidInputs(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"no separator", "alphaabcdef"},
		{"bad checksum", "alpha1qpzry9x8gf2tvdw0s3jn54khce6muaX"},
		{"mixed case", "Alpha1qpzry9x8gf2tvdw0s3jn54khce6mua7l"},
		{"invalid char", "alpha1!invalidchar"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Decode(tc.input)
			if err == nil {
				t.Errorf("expected error for input %q, got nil", tc.input)
			}
		})
	}
}

func TestAddressFromPubKey(t *testing.T) {
	// Use a well-known test pubkey (compressed secp256k1)
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	pubKey, _ := hex.DecodeString(pubKeyHex)

	addr, err := AddressFromPubKey(pubKey)
	if err != nil {
		t.Fatalf("AddressFromPubKey failed: %v", err)
	}

	if !strings.HasPrefix(addr, "alpha1") {
		t.Errorf("address should start with 'alpha1', got %q", addr)
	}

	// Decode and verify length
	hrp, payload, err := Decode(addr)
	if err != nil {
		t.Fatalf("address is not valid bech32: %v", err)
	}
	if hrp != "alpha" {
		t.Errorf("expected hrp 'alpha', got %q", hrp)
	}
	if len(payload) != 20 {
		t.Errorf("expected 20 byte payload (RIPEMD-160), got %d", len(payload))
	}

	t.Logf("Generated address: %s", addr)
}

func TestAddressFromPubKeyDeterministic(t *testing.T) {
	pubKey := []byte("test public key material 32bytes!")
	addr1, err := AddressFromPubKey(pubKey)
	if err != nil {
		t.Fatal(err)
	}
	addr2, err := AddressFromPubKey(pubKey)
	if err != nil {
		t.Fatal(err)
	}
	if addr1 != addr2 {
		t.Errorf("AddressFromPubKey is non-deterministic: %q vs %q", addr1, addr2)
	}
}

func TestValidateAddress(t *testing.T) {
	pubKey := []byte("some agent public key material 1!")
	addr, _ := AddressFromPubKey(pubKey)

	if !ValidateAddress(addr) {
		t.Errorf("expected valid address, got invalid for %q", addr)
	}

	// Bad cases
	cases := []string{
		"",
		"alpha1badchecksum",
		"notalphaprefix1qpzry9x8gf2tvdw0s3jn54khce6mua7l",
		"random string",
	}
	for _, c := range cases {
		if ValidateAddress(c) {
			t.Errorf("expected invalid address for %q", c)
		}
	}
}

func TestEncodeDecodeLowercase(t *testing.T) {
	data := []byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	encoded, err := Encode("alpha", data)
	if err != nil {
		t.Fatal(err)
	}
	// bech32 must be all lowercase
	if encoded != strings.ToLower(encoded) {
		t.Errorf("encoded bech32 is not lowercase: %q", encoded)
	}
}
