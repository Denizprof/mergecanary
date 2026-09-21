package bisect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// failsIf builds a Tester that fails when every one of the given branches is in the set.
func failsIf(groups ...[]string) Tester {
	return func(_ context.Context, set []string) (Verdict, string, error) {
		in := map[string]bool{}
		for _, s := range set {
			in[s] = true
		}
		for _, g := range groups {
			all := true
			for _, n := range g {
				all = all && in[n]
			}
			if all {
				return Fail, "fail: " + strings.Join(g, "+"), nil
			}
		}
		return Pass, "", nil
	}
}

func sets(r Result) [][]string {
	var out [][]string
	for _, f := range r.Findings {
		out = append(out, f.Set)
	}
	return out
}

func TestFindPair(t *testing.T) {
	names := []string{"agent-1", "agent-2", "agent-3"}
	r, err := Find(context.Background(), names, failsIf([]string{"agent-1", "agent-3"}))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"agent-1", "agent-3"}}
	if !reflect.DeepEqual(sets(r), want) {
		t.Fatalf("got %v want %v", sets(r), want)
	}
	if r.Findings[0].Output != "fail: agent-1+agent-3" {
		t.Fatalf("output not carried: %q", r.Findings[0].Output)
	}
	if r.Checks != 6 { // 3 singles + 3 pairs
		t.Fatalf("checks = %d", r.Checks)
	}
}

func TestFindSingleAndSkipsPairsContainingIt(t *testing.T) {
	names := []string{"a", "b", "c"}
	// b is broken alone; that must not also be reported as {a,b} and {b,c}.
	r, err := Find(context.Background(), names, failsIf([]string{"b"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"b"}}; !reflect.DeepEqual(sets(r), want) {
		t.Fatalf("got %v want %v", sets(r), want)
	}
}

func TestFindIndependentSingleAndPair(t *testing.T) {
	names := []string{"a", "b", "c", "d"}
	r, err := Find(context.Background(), names, failsIf([]string{"a"}, []string{"c", "d"}))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a"}, {"c", "d"}}
	if !reflect.DeepEqual(sets(r), want) {
		t.Fatalf("got %v want %v", sets(r), want)
	}
}

func TestFindTripleFallsBackToMinimize(t *testing.T) {
	names := []string{"a", "b", "c", "d", "e"}
	r, err := Find(context.Background(), names, failsIf([]string{"b", "d", "e"}))
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"b", "d", "e"}}
	if !reflect.DeepEqual(sets(r), want) {
		t.Fatalf("got %v want %v", sets(r), want)
	}
}

func TestFindSkipIsNotFailure(t *testing.T) {
	test := func(_ context.Context, set []string) (Verdict, string, error) {
		if len(set) == 2 {
			return Skip, "", nil // e.g. the pair cannot be merged
		}
		return Pass, "", nil
	}
	// Nothing fails in any tested subset, so the fallback reports the full set.
	r, err := Find(context.Background(), []string{"a", "b", "c"}, test)
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"a", "b", "c"}}; !reflect.DeepEqual(sets(r), want) {
		t.Fatalf("got %v want %v", sets(r), want)
	}
}

func TestFindMemoizes(t *testing.T) {
	calls := 0
	inner := failsIf()
	test := func(ctx context.Context, set []string) (Verdict, string, error) {
		calls++
		return inner(ctx, set)
	}
	r, _ := Find(context.Background(), []string{"a", "b", "c"}, test)
	if calls != r.Checks {
		t.Fatalf("test called %d times but Checks=%d", calls, r.Checks)
	}
	// fallback re-tests {a,b},{a,c},{b,c} etc.: they must come from the cache
	if calls != 3+3+1 { // singles, pairs, then only the full set is new
		t.Fatalf("calls=%d", calls)
	}
}

func TestFindPropagatesErrorAndCancel(t *testing.T) {
	boom := errors.New("boom")
	_, err := Find(context.Background(), []string{"a"}, func(context.Context, []string) (Verdict, string, error) {
		return Pass, "", boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Find(ctx, []string{"a"}, failsIf()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestAttribute(t *testing.T) {
	changed := map[string][]string{
		"agent-1": {"profile.go", "profile_test.go"},
		"agent-3": {"users.go", "users_test.go"},
	}
	pair := [2]string{"agent-1", "agent-3"}
	cases := []struct {
		name, out, breaker, victim string
	}{
		{"error in agent-1 file", "# fixture\n.\\profile.go:4:23: undefined: getUser", "agent-3", "agent-1"},
		{"unix path", "./profile.go:4:23: undefined", "agent-3", "agent-1"},
		{"error in agent-3 file", "users.go:9: boom", "agent-1", "agent-3"},
		{"both mentioned", "profile.go:1 users.go:2", "", ""},
		{"neither mentioned", "exit status 1", "", ""},
		{"suffix must not match", "myprofile.go:4", "", ""},
	}
	for _, c := range cases {
		b, v := Attribute(pair, changed, c.out)
		if b != c.breaker || v != c.victim {
			t.Errorf("%s: got breaker=%q victim=%q", c.name, b, v)
		}
	}
	// a file changed by both branches is not evidence for either
	shared := map[string][]string{"x": {"common.go", "x.go"}, "y": {"common.go"}}
	if b, v := Attribute([2]string{"x", "y"}, shared, "common.go:1: err"); b != "" || v != "" {
		t.Errorf("shared file attributed: %q %q", b, v)
	}
}
