// Package auth hashes and verifies session passwords with Argon2id under a
// global concurrency limit, so login floods cannot exhaust CPU/memory.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"

	"golang.org/x/crypto/argon2"
)

// Params are Argon2id parameters. Defaults follow the OWASP Password Storage
// Cheat Sheet minimum (m=19 MiB, t=2, p=1).
type Params struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	// MaxConcurrent bounds simultaneous hash computations; peak memory is
	// MaxConcurrent*MemoryKiB.
	MaxConcurrent int
}

func DefaultParams() Params {
	return Params{MemoryKiB: 19 * 1024, Iterations: 2, Parallelism: 1, MaxConcurrent: 4}
}

// ErrBusy means the hasher is saturated; callers should answer RATE_LIMITED.
var ErrBusy = errors.New("password hasher busy")

type Hash struct {
	salt [16]byte
	key  [32]byte
	p    Params
}

type Hasher struct {
	p   Params
	sem chan struct{}
	// dummy is used to spend the same work when the session does not exist.
	dummy Hash
}

func NewHasher(p Params) *Hasher {
	if p.MaxConcurrent <= 0 {
		p.MaxConcurrent = 1
	}
	h := &Hasher{p: p, sem: make(chan struct{}, p.MaxConcurrent)}
	h.dummy = h.compute("dummy-password-for-timing")
	return h
}

func (h *Hasher) compute(pw string) Hash {
	var out Hash
	if _, err := rand.Read(out.salt[:]); err != nil {
		panic(err)
	}
	out.p = h.p
	copy(out.key[:], argon2.IDKey([]byte(pw), out.salt[:], h.p.Iterations, h.p.MemoryKiB, h.p.Parallelism, 32))
	return out
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ErrBusy
	}
}

// Hash derives a new salted hash. The plaintext is not retained.
func (h *Hasher) Hash(ctx context.Context, pw string) (Hash, error) {
	if err := h.acquire(ctx); err != nil {
		return Hash{}, err
	}
	defer func() { <-h.sem }()
	return h.compute(pw), nil
}

// Verify checks pw against hash. A nil hash verifies against a dummy (always false),
// spending the same work to avoid revealing whether a session exists.
func (h *Hasher) Verify(ctx context.Context, hash *Hash, pw string) (bool, error) {
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer func() { <-h.sem }()
	target := hash
	if target == nil {
		target = &h.dummy
	}
	k := argon2.IDKey([]byte(pw), target.salt[:], target.p.Iterations, target.p.MemoryKiB, target.p.Parallelism, 32)
	ok := subtle.ConstantTimeCompare(k, target.key[:]) == 1
	return ok && hash != nil, nil
}

// Equal reports whether two hashes are the same stored value (same salt and key).
func (a Hash) Equal(b Hash) bool { return a.salt == b.salt && a.key == b.key }
