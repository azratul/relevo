// Package secret generates identifiers and credentials with crypto/rand.
package secret

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
)

func randomB64(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// SessionID returns 128 random bits (22 base64url chars).
func SessionID() string { return randomB64(16) }

// ParticipantID returns "p_" + 96 random bits.
func ParticipantID() string { return "p_" + randomB64(12) }

// ConnectionID returns "c_" + 96 random bits.
func ConnectionID() string { return "c_" + randomB64(12) }

// Token returns an opaque bearer token with 256 random bits and a role prefix
// ("rlvh" host, "rlvp" participant). The prefix helps secret scanners.
func Token(prefix string) string { return prefix + "_" + randomB64(32) }

// UUID returns a random (v4) UUID.
func UUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// Crockford-like alphabet without ambiguous characters (0/o, 1/l/i, u).
const pwAlphabet = "abcdefghjkmnpqrstvwxyz23456789"

// Password returns a random password of 12 symbols from a 30-symbol alphabet
// (~58.9 bits), formatted as xxxx-xxxx-xxxx for dictation.
func Password() string {
	const n = 12
	var sb strings.Builder
	max := byte(256 - 256%len(pwAlphabet))
	buf := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			panic(err)
		}
		if buf[0] >= max { // rejection sampling: no modulo bias
			continue
		}
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(pwAlphabet[int(buf[0])%len(pwAlphabet)])
		i++
	}
	return sb.String()
}

// HashToken returns SHA-256 of a high-entropy token (fine for random tokens; NOT for passwords).
func HashToken(tok string) [32]byte { return sha256.Sum256([]byte(tok)) }

// Equal compares in constant time.
func Equal(a, b [32]byte) bool { return subtle.ConstantTimeCompare(a[:], b[:]) == 1 }
