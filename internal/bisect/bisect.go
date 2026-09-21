// Package bisect finds the smallest set of branches whose combination fails.
package bisect

import (
	"context"
	"strings"
)

// Verdict is the result of testing one set of branches merged into base.
type Verdict int

const (
	Pass Verdict = iota
	Fail
	Skip // the set could not be built (e.g. merge conflict); says nothing about the check
)

// Tester merges the named branches into base and runs the check. It also
// returns the check output (used to explain a failure).
type Tester func(ctx context.Context, set []string) (Verdict, string, error)

// Finding is a minimal failing set of branches, in input order.
type Finding struct {
	Set    []string
	Output string // check output of the failing run
}

// Result is what Find discovered.
type Result struct {
	Findings []Finding
	Checks   int // how many distinct sets were tested
}

// Find looks for minimal failing subsets of names, all of which together are
// already known to fail:
//
//  1. each branch alone (base + one branch); failing ones are reported alone;
//  2. each pair of the remaining branches; failing pairs are reported;
//  3. if neither finds anything, the full set is shrunk by dropping one branch
//     at a time while it keeps failing (a 1-minimal set, typically of size 3+).
//
// Cost is n + n(n-1)/2 checks in the worst case for steps 1-2.
func Find(ctx context.Context, names []string, test Tester) (Result, error) {
	type memo struct {
		v   Verdict
		out string
	}
	seen := map[string]memo{}
	var res Result
	run := func(set []string) (Verdict, string, error) {
		key := strings.Join(set, "\x00")
		if m, ok := seen[key]; ok {
			return m.v, m.out, nil
		}
		if err := ctx.Err(); err != nil {
			return Pass, "", err
		}
		v, out, err := test(ctx, set)
		if err != nil {
			return Pass, "", err
		}
		res.Checks++
		seen[key] = memo{v, out}
		return v, out, nil
	}

	var clear []string // branches that do not fail alone
	for _, n := range names {
		v, out, err := run([]string{n})
		if err != nil {
			return res, err
		}
		if v == Fail {
			res.Findings = append(res.Findings, Finding{Set: []string{n}, Output: out})
		} else {
			clear = append(clear, n)
		}
	}

	for i := 0; i < len(clear); i++ {
		for j := i + 1; j < len(clear); j++ {
			set := []string{clear[i], clear[j]}
			v, out, err := run(set)
			if err != nil {
				return res, err
			}
			if v == Fail {
				res.Findings = append(res.Findings, Finding{Set: set, Output: out})
			}
		}
	}
	if len(res.Findings) > 0 {
		return res, nil
	}

	// Only a larger combination fails: shrink greedily.
	cur := append([]string(nil), names...)
	for _, drop := range names {
		trial := without(cur, drop)
		if len(trial) == 0 {
			continue
		}
		v, _, err := run(trial)
		if err != nil {
			return res, err
		}
		if v == Fail {
			cur = trial
		}
	}
	_, out, err := run(cur)
	if err != nil {
		return res, err
	}
	res.Findings = append(res.Findings, Finding{Set: cur, Output: out})
	return res, nil
}

func without(set []string, drop string) []string {
	var out []string
	for _, s := range set {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// Attribute names a breaker and a victim for a failing pair, using where the
// check output points: if the output mentions files changed by exactly one of
// the two branches, that branch is the victim (its code stopped working) and
// the other is the breaker. It returns empty strings when that is ambiguous.
// This is a heuristic over the failure text, not proof.
func Attribute(pair [2]string, changed map[string][]string, output string) (breaker, victim string) {
	out := strings.ReplaceAll(output, `\`, "/")
	mentions := func(self, other string) bool {
		theirs := map[string]bool{}
		for _, f := range changed[other] {
			theirs[f] = true
		}
		for _, f := range changed[self] {
			if !theirs[f] && mentioned(out, f) {
				return true
			}
		}
		return false
	}
	a := mentions(pair[0], pair[1])
	b := mentions(pair[1], pair[0])
	switch {
	case a && !b:
		return pair[1], pair[0]
	case b && !a:
		return pair[0], pair[1]
	}
	return "", ""
}

// mentioned reports whether path appears in out as a whole path suffix
// (so "users.go" does not match "allusers.go").
func mentioned(out, path string) bool {
	for i := 0; ; {
		j := strings.Index(out[i:], path)
		if j < 0 {
			return false
		}
		j += i
		if j == 0 || !isNameChar(out[j-1]) {
			return true
		}
		i = j + 1
	}
}

func isNameChar(c byte) bool {
	return c == '_' || c == '-' ||
		'0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}
