package fixture

import "testing"

func TestFetchUser(t *testing.T) {
	if got := fetchUser(1); got != "user-1" {
		t.Fatalf("got %q", got)
	}
}
