package fixture

import "testing"

func TestGetUser(t *testing.T) {
	if got := getUser(1); got != "user-1" {
		t.Fatalf("got %q", got)
	}
}
