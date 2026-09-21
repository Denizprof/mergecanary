package board

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Denizprof/mergecanary/internal/engine"
	"github.com/Denizprof/mergecanary/internal/integrate"
)

var at = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func failing() *engine.Report {
	return &engine.Report{
		Base: "main", BaseSHA: "e208aabcdef0123", CheckedAt: at, Runs: 8, DurationMS: 7400,
		Branches: []engine.BranchReport{
			{Name: "agent-1", Status: engine.StatusFail},
			{Name: "agent-2", Status: engine.StatusOK},
			{Name: "agent-3", Status: engine.StatusFail},
		},
		Findings: []engine.Finding{{
			Set: []string{"agent-1", "agent-3"}, Breaker: "agent-3", Victim: "agent-1",
			Summary: "agent-3 breaks agent-1", Output: "# fixture\n./profile.go:4:23: undefined: getUser\nFAIL\n",
		}},
		Conflicts: []integrate.Conflict{},
	}
}

func TestRenderFailingPlain(t *testing.T) {
	got := Render(View{Report: failing()}, false)
	if strings.Contains(got, "\x1b") {
		t.Fatal("plain render contains escape sequences")
	}
	lines := strings.Split(got, "\n")
	if !strings.Contains(lines[0], "mergecanary") || !strings.HasSuffix(strings.TrimSpace(lines[0]), "FAIL") {
		t.Fatalf("overall status must be on the first line: %q", lines[0])
	}
	// exactly one line per branch, each starting with its verdict
	for name, want := range map[string]string{"agent-1": "FAIL", "agent-2": "PASS", "agent-3": "FAIL"} {
		n := 0
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), want) && strings.Contains(l, name) && len(l) < 90 {
				n++
			}
		}
		if n < 1 {
			t.Errorf("no %s line for %s in:\n%s", want, name, got)
		}
	}
	for _, want := range []string{"agent-3 breaks agent-1", "undefined: getUser", "base main@e208aab", "8 check runs"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderColors(t *testing.T) {
	got := Render(View{Report: failing()}, true)
	if !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "\x1b[32m") {
		t.Fatalf("expected red and green in colored render:\n%q", got)
	}
}

func TestRenderPass(t *testing.T) {
	r := &engine.Report{Base: "main", OK: true, CheckedAt: at, Runs: 1,
		Branches: []engine.BranchReport{{Name: "a", Status: engine.StatusOK}}}
	got := Render(View{Report: r}, false)
	if !strings.Contains(strings.Split(got, "\n")[0], "PASS") || strings.Contains(got, "FAIL") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestRenderConflictBaseFailingEmptyAndStates(t *testing.T) {
	r := &engine.Report{Base: "main", CheckedAt: at,
		Branches:  []engine.BranchReport{{Name: "x", Status: engine.StatusConflict}, {Name: "y", Status: engine.StatusConflict}},
		Conflicts: []integrate.Conflict{{Branch: "y", With: "x", Files: []string{"a.go", "b.go"}}}}
	got := Render(View{Report: r}, false)
	if !strings.Contains(got, "y conflicts with x in a.go, b.go") || strings.Count(got, "CONFLICT") != 2 {
		t.Fatalf("conflict not shown on both branches:\n%s", got)
	}

	r = &engine.Report{Base: "main", CheckedAt: at, BaseFailing: true}
	got = Render(View{Report: r}, false)
	if !strings.Contains(got, "fails on main alone") || !strings.Contains(got, "no in-flight branches") {
		t.Fatalf("got:\n%s", got)
	}

	if got := Render(View{}, false); !strings.Contains(got, "CHECKING") {
		t.Fatalf("first-pass state:\n%s", got)
	}
	if got := Render(View{Report: failing(), Running: true}, false); !strings.Contains(got, "RE-CHECKING") {
		t.Fatalf("running state:\n%s", got)
	}
	if got := Render(View{Err: errors.New("boom\nline2")}, false); !strings.Contains(got, "error: boom line2") {
		t.Fatalf("error state:\n%s", got)
	}
	if got := Render(View{Report: failing(), Err: errors.New("boom")}, false); !strings.Contains(got, "showing previous result") {
		t.Fatalf("error over previous result:\n%s", got)
	}
}

func TestOutputIsTruncatedToTail(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 50; i++ {
		sb.WriteString("line" + string(rune('A'+i%26)) + "\n")
	}
	sb.WriteString("LAST\n")
	r := failing()
	r.Findings[0].Output = sb.String()
	got := Render(View{Report: r}, false)
	if !strings.Contains(got, "LAST") || !strings.Contains(got, "...") || strings.Count(got, "line") > MaxOutputLines {
		t.Fatalf("output not tailed:\n%s", got)
	}
}

func TestRedrawerFrame(t *testing.T) {
	var sb strings.Builder
	(&Redrawer{W: &sb}).Draw("a\nb\n")
	if want := "\x1b[Ha\x1b[K\nb\x1b[K\n\x1b[J"; sb.String() != want {
		t.Fatalf("got %q want %q", sb.String(), want)
	}
}
