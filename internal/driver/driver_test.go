package driver

import (
	"regexp"
	"testing"
)

func TestNewUUID(t *testing.T) {
	v4 := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 100 {
		u := NewUUID()
		if !v4.MatchString(u) || seen[u] {
			t.Fatalf("bad or repeated uuid %q", u)
		}
		seen[u] = true
	}
}
