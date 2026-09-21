package fixture

import "testing"

func TestProfileTitle(t *testing.T) {
	if got := profileTitle(2); got != "Profile: user-2" {
		t.Fatalf("got %q", got)
	}
}
