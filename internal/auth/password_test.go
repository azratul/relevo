package auth

import (
	"context"
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	h := NewHasher(Params{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, MaxConcurrent: 1})
	ctx := context.Background()
	hash, err := h.Hash(ctx, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.Verify(ctx, &hash, "correct horse"); !ok {
		t.Fatal("should verify")
	}
	if ok, _ := h.Verify(ctx, &hash, "wrong"); ok {
		t.Fatal("should not verify")
	}
	if ok, _ := h.Verify(ctx, nil, "dummy-password-for-timing"); ok {
		t.Fatal("nil hash must never verify")
	}
}

func TestBusy(t *testing.T) {
	h := NewHasher(Params{MemoryKiB: 1024, Iterations: 1, Parallelism: 1, MaxConcurrent: 1})
	h.sem <- struct{}{} // saturate
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := h.Verify(ctx, nil, "x"); err != ErrBusy {
		t.Fatalf("want ErrBusy, got %v", err)
	}
}
