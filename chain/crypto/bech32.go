// Package crypto implements cryptographic primitives for Alpha Network.
// Includes bech32 address encoding/decoding from scratch (no external deps)
// and ZK Proof of Intelligence using gnark.
package crypto

import (
	"crypto/sha256"
	"errors"
	"strings"

	"golang.org/x/crypto/ripemd160" //nolint:gosec // RIPEMD-160 used for address derivation like Bitcoin
)

// bech32 charset and reverse lookup
const charset = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"

var charsetRev [256]int8

func init() {
	for i := range charsetRev {
		charsetRev[i] = -1
	}
	for i, c := range charset {
		charsetRev[c] = int8(i)
	}
}

// generator polynomial coefficients for bech32 checksum
var generator = [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}

func polymod(values []byte) uint32 {
	chk := uint32(1)
	for _, v := range values {
		top := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (top>>uint(i))&1 == 1 {
				chk ^= generator[i]
			}
		}
	}
	return chk
}

func hrpExpand(hrp string) []byte {
	out := make([]byte, len(hrp)*2+1)
	for i := 0; i < len(hrp); i++ {
		out[i] = hrp[i] >> 5
	}
	out[len(hrp)] = 0
	for i := 0; i < len(hrp); i++ {
		out[len(hrp)+1+i] = hrp[i] & 31
	}
	return out
}

func verifyChecksum(hrp string, data []byte) bool {
	combined := append(hrpExpand(hrp), data...)
	return polymod(combined) == 1
}

func createChecksum(hrp string, data []byte) []byte {
	combined := append(hrpExpand(hrp), data...)
	combined = append(combined, 0, 0, 0, 0, 0, 0)
	mod := polymod(combined) ^ 1
	ret := make([]byte, 6)
	for i := range ret {
		ret[i] = byte(mod>>uint(5*(5-i))) & 31
	}
	return ret
}

// convertBits converts a byte slice from one bit-grouping to another.
// fromBits and toBits must be in [1,8]. If pad is true, pad the output.
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := 0
	bits := uint(0)
	var result []byte
	maxv := (1 << toBits) - 1
	maxAcc := (1 << (fromBits + toBits - 1)) - 1

	for _, b := range data {
		if int(b) < 0 || (int(b)>>fromBits) != 0 {
			return nil, errors.New("invalid data range")
		}
		acc = ((acc << fromBits) | int(b)) & maxAcc
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			result = append(result, byte((acc>>bits)&maxv))
		}
	}

	if pad {
		if bits > 0 {
			result = append(result, byte((acc<<(toBits-bits))&maxv))
		}
	} else if bits >= fromBits || ((acc<<(toBits-bits))&maxv) != 0 {
		return nil, errors.New("invalid padding")
	}
	return result, nil
}

// Encode encodes data bytes with the given human-readable prefix into a bech32 string.
// data should be raw bytes; they are internally converted to 5-bit groups.
func Encode(prefix string, data []byte) (string, error) {
	if len(prefix) == 0 {
		return "", errors.New("prefix cannot be empty")
	}
	conv, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", err
	}
	checksum := createChecksum(prefix, conv)
	combined := append(conv, checksum...)

	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteByte('1')
	for _, b := range combined {
		sb.WriteByte(charset[b])
	}
	return sb.String(), nil
}

// Decode decodes a bech32 string and returns the human-readable prefix and decoded data bytes.
// Returns an error if the encoding is invalid.
func Decode(bech string) (string, []byte, error) {
	if len(bech) > 90 {
		return "", nil, errors.New("bech32 string too long")
	}

	// Only lowercase (or normalize to lowercase)
	lower := strings.ToLower(bech)
	if strings.ContainsAny(bech, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") &&
		strings.ContainsAny(bech, "abcdefghijklmnopqrstuvwxyz") {
		return "", nil, errors.New("mixed case in bech32 string")
	}
	bech = lower

	// Find the separator
	sepIdx := strings.LastIndexByte(bech, '1')
	if sepIdx < 1 || sepIdx+7 > len(bech) {
		return "", nil, errors.New("invalid bech32 separator position")
	}

	hrp := bech[:sepIdx]
	data := bech[sepIdx+1:]

	decoded := make([]byte, len(data))
	for i, c := range data {
		v := charsetRev[c]
		if v < 0 {
			return "", nil, errors.New("invalid character in bech32 string")
		}
		decoded[i] = byte(v)
	}

	if !verifyChecksum(hrp, decoded) {
		return "", nil, errors.New("invalid bech32 checksum")
	}

	// Strip 6-byte checksum and convert back to 8-bit groups
	payload, err := convertBits(decoded[:len(decoded)-6], 5, 8, false)
	if err != nil {
		return "", nil, err
	}

	return hrp, payload, nil
}

// AddressFromPubKey derives an Alpha Network bech32 address from a raw public key.
// Uses SHA256 + RIPEMD-160 (like Bitcoin P2PKH) then encodes with "alpha" prefix.
func AddressFromPubKey(pubKey []byte) (string, error) {
	if len(pubKey) == 0 {
		return "", errors.New("public key cannot be empty")
	}

	// SHA256 first
	sha := sha256.Sum256(pubKey)

	// Then RIPEMD-160
	h := ripemd160.New()
	h.Write(sha[:])
	keyHash := h.Sum(nil) // 20 bytes

	return Encode("alpha", keyHash)
}

// ValidateAddress checks that addr is a valid bech32-encoded Alpha Network address
// (prefix "alpha", valid checksum, correct data length).
func ValidateAddress(addr string) bool {
	hrp, payload, err := Decode(addr)
	if err != nil {
		return false
	}
	if hrp != "alpha" {
		return false
	}
	// RIPEMD-160 hashes are 20 bytes
	if len(payload) != 20 {
		return false
	}
	return true
}
