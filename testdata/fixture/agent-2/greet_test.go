package fixture

import "testing"

func TestGreet(t *testing.T) {
	if got := greet("bob"); got != "hello, bob" {
		t.Fatalf("got %q", got)
	}
}
