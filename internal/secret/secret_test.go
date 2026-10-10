package secret

import (
	"regexp"
	"testing"
)

func TestFormats(t *testing.T) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(SessionID()) {
		t.Fatal("session id")
	}
	if !regexp.MustCompile(`^p_[A-Za-z0-9_-]{16}$`).MatchString(ParticipantID()) {
		t.Fatal("participant id")
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(UUID()) {
		t.Fatal("uuid")
	}
	re := regexp.MustCompile(`^[a-z2-9]{4}-[a-z2-9]{4}-[a-z2-9]{4}$`)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		p := Password()
		if !re.MatchString(p) || seen[p] {
			t.Fatalf("bad or repeated password %q", p)
		}
		seen[p] = true
	}
}
