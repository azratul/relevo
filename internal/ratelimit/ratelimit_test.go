package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, 2, 10)
	l.now = func() time.Time { return now }
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal()
	}
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal()
	}
	if ok, wait := l.Allow("a"); ok || wait <= 0 {
		t.Fatal("third should be denied")
	}
	now = now.Add(time.Second)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("refilled")
	}
}

func TestBackoff(t *testing.T) {
	now := time.Unix(0, 0)
	b := NewBackoff(2, time.Second, 8*time.Second, 10)
	b.now = func() time.Time { return now }
	b.Fail("k")
	b.Fail("k")
	if b.Check("k") != 0 {
		t.Fatal("free failures")
	}
	b.Fail("k")
	if b.Check("k") != time.Second {
		t.Fatal("first lockout 1s")
	}
	b.Fail("k")
	if b.Check("k") != 2*time.Second {
		t.Fatal("second lockout 2s")
	}
	for i := 0; i < 10; i++ {
		b.Fail("k")
	}
	if b.Check("k") != 8*time.Second {
		t.Fatal("capped")
	}
	b.Success("k")
	if b.Check("k") != 0 {
		t.Fatal("cleared")
	}
}
