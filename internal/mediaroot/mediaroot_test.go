package mediaroot

import "testing"

func TestResolveExplicitPath(t *testing.T) {
	if got := Resolve("/srv/seinfeld"); got != "/srv/seinfeld" {
		t.Fatalf("Resolve explicit path = %q", got)
	}
}

func TestResolveAutoHasStableFallback(t *testing.T) {
	got := Resolve(Auto)
	if got == "" {
		t.Fatal("auto resolution returned an empty path")
	}
}
