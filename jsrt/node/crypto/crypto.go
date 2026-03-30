// Package crypto provides Node.js crypto module equivalents for transpiled TypeScript code.
package crypto

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"math/big"
)

// RandomBytes generates n random bytes.
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	return b, err
}

// RandomUUID generates a v4 UUID string.
func RandomUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RandomInt generates a random integer in [min, max).
func RandomInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min)))
	return int(n.Int64()) + min
}

// Hash represents a streaming hash computation.
type Hash struct {
	h         hash.Hash
	algorithm string
}

// CreateHash creates a new Hash with the given algorithm.
func CreateHash(algorithm string) *Hash {
	var h hash.Hash
	switch algorithm {
	case "md5":
		h = md5.New()
	case "sha1":
		h = sha1.New()
	case "sha256":
		h = sha256.New()
	case "sha512":
		h = sha512.New()
	default:
		h = sha256.New()
	}
	return &Hash{h: h, algorithm: algorithm}
}

// Update adds data to the hash.
func (h *Hash) Update(data string) *Hash {
	h.h.Write([]byte(data))
	return h
}

// Digest finalizes and returns the hash as a hex string.
func (h *Hash) Digest(encoding ...string) string {
	enc := "hex"
	if len(encoding) > 0 {
		enc = encoding[0]
	}
	sum := h.h.Sum(nil)
	switch enc {
	case "hex":
		return hex.EncodeToString(sum)
	default:
		return hex.EncodeToString(sum)
	}
}

// CreateHmac creates a new HMAC hash.
func CreateHmac(algorithm string, key []byte) *Hash {
	var h hash.Hash
	switch algorithm {
	case "md5":
		h = hmac.New(md5.New, key)
	case "sha1":
		h = hmac.New(sha1.New, key)
	case "sha256":
		h = hmac.New(sha256.New, key)
	case "sha512":
		h = hmac.New(sha512.New, key)
	default:
		h = hmac.New(sha256.New, key)
	}
	return &Hash{h: h, algorithm: algorithm}
}
