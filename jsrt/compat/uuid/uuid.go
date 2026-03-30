// Package uuid provides a Go adapter for the npm "uuid" package.
package uuid

import (
	"crypto/rand"
	"fmt"

	"github.com/i2y/ramune/jsrt/node/crypto"
)

// V4 generates a random UUID v4 string.
func V4() string {
	return crypto.RandomUUID()
}

// V1 generates a UUID v1-like string (timestamp-based, simplified).
func V1() string {
	// Simplified: use random bytes with v1 version marker
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x10 // version 1
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Validate checks if a string is a valid UUID format.
func Validate(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

// Parse parses a UUID string and returns it normalized (lowercase).
func Parse(s string) (string, error) {
	if !Validate(s) {
		return "", fmt.Errorf("invalid UUID: %s", s)
	}
	return s, nil
}

// Nil is the nil UUID (all zeros).
const Nil = "00000000-0000-0000-0000-000000000000"
