package engine_test

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/Denizprof/mergecanary/internal/engine"
	"github.com/Denizprof/mergecanary/internal/testutil"
)

func opts(f testutil.Fix, cmd string) engine.Options {
	return engine.Options{Repo: f.Repo, Base: "main", Command: cmd, Timeout: 2 * time.Minute}
}

func status(r engine.Report) map[string]engine.Status {
	m := map[string]engine.Status{}
	for _, b := range r.Branches {
		m[b.Name] = b.Status
	}
	return m
}

func needGo(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; the fixture's check command is `go test ./...`")
	}
}

// The main end-to-end test: agent-1 and agent-3 merge cleanly and pass alone,
// but break together. mergecanary must say agent-3 breaks agent-1.
func TestFixtureAgent3BreaksAgent1(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	before := testutil.Snapshot(t, f)

	rep, err := engine.Analyze(context.Background(), opts(f, "go test ./..."))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("summary: %s", rep.Findings[0].Summary)
	t.Logf("check output of failing run:\n%s", rep.Findings[0].Output)

	if rep.OK || rep.BaseFailing || len(rep.Conflicts) != 0 {
		t.Fatalf("want failing report without conflicts or base failure: %+v", rep)
	}
	if !rep.Check.Ran || rep.Check.OK {
		t.Fatalf("full check should have run and failed: %+v", rep.Check)
	}
	if len(rep.Findings) != 1 {
		t.Fatalf("want exactly 1 finding, got %+v", rep.Findings)
	}
	fd := rep.Findings[0]
	if !reflect.DeepEqual(fd.Set, []string{"agent-1", "agent-3"}) || fd.Breaker != "agent-3" || fd.Victim != "agent-1" {
		t.Fatalf("finding = %+v", fd)
	}
	if fd.Summary != "agent-3 breaks agent-1" {
		t.Fatalf("summary = %q", fd.Summary)
	}
	want := map[string]engine.Status{"agent-1": engine.StatusFail, "agent-2": engine.StatusOK, "agent-3": engine.StatusFail}
	if got := status(rep); !reflect.DeepEqual(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	// 1 full + 1 base + 3 singles + 3 pairs
	if rep.Runs != 8 {
		t.Errorf("check runs = %d, want 8", rep.Runs)
	}
	if after := testutil.Snapshot(t, f); after != before {
		t.Fatalf("real repos changed:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

func TestFixtureGreenWithoutAgent3(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	testutil.Git(t, f.Repo, "worktree", "remove", "--force", f.Path("agent-3"))
	rep, err := engine.Analyze(context.Background(), opts(f, "go test ./..."))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK || !rep.Check.Ran || len(rep.Findings) != 0 || rep.Runs != 1 {
		t.Fatalf("want green single-run report: %+v", rep)
	}
	for n, s := range status(rep) {
		if s != engine.StatusOK {
			t.Errorf("%s = %s", n, s)
		}
	}
}

func TestSingleBrokenBranch(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	testutil.AddBranch(t, f, "broken", map[string]string{"bad.go": "package fixture\n\nfunc bad() { undefinedThing() }\n"})
	rep, err := engine.Analyze(context.Background(), opts(f, "go test ./..."))
	if err != nil {
		t.Fatal(err)
	}
	// agent-1+agent-3 also break together in this fixture; the broken branch is reported alone.
	var summaries []string
	for _, fd := range rep.Findings {
		summaries = append(summaries, fd.Summary)
	}
	want := []string{"broken fails the check on its own", "agent-3 breaks agent-1"}
	if !reflect.DeepEqual(summaries, want) {
		t.Fatalf("summaries = %v, want %v", summaries, want)
	}
}

func TestBaseFailingIsNotBlamedOnBranches(t *testing.T) {
	f := testutil.Fixture(t)
	rep, err := engine.Analyze(context.Background(), opts(f, "exit 1"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || !rep.BaseFailing || len(rep.Findings) != 0 {
		t.Fatalf("want base_failing and no findings: %+v", rep)
	}
	if rep.Runs != 2 { // full + base only; no bisect
		t.Errorf("runs = %d, want 2", rep.Runs)
	}
}

func TestConflictReportedAndOthersStillChecked(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	// Same line edited two ways (conflicts), but behaviour is unchanged so the check still passes.
	body := "package fixture\n\nimport \"fmt\"\n\nfunc getUser(id int) string {\n\treturn fmt.Sprintf(\"user-%%d\", id) // %s\n}\n"
	testutil.AddBranch(t, f, "conflict-a", map[string]string{"users.go": sprintf(body, "A")})
	testutil.AddBranch(t, f, "conflict-b", map[string]string{"users.go": sprintf(body, "B")})
	testutil.Git(t, f.Repo, "worktree", "remove", "--force", f.Path("agent-3"))

	rep, err := engine.Analyze(context.Background(), opts(f, "go test ./..."))
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || len(rep.Conflicts) != 1 {
		t.Fatalf("want a failing report with one conflict: %+v", rep)
	}
	c := rep.Conflicts[0]
	if c.Branch != "conflict-b" || c.With != "conflict-a" || !reflect.DeepEqual(c.Files, []string{"users.go"}) {
		t.Fatalf("conflict = %+v", c)
	}
	if !rep.Check.Ran || !rep.Check.OK {
		t.Fatalf("the merged branches should still be checked (and pass): %+v", rep.Check)
	}
	if s := status(rep); s["conflict-a"] != engine.StatusConflict || s["conflict-b"] != engine.StatusConflict || s["agent-1"] != engine.StatusOK {
		t.Fatalf("statuses = %v", s)
	}
}

func TestTimeoutIsFailure(t *testing.T) {
	f := testutil.Fixture(t)
	o := opts(f, "go test ./...")
	o.Command = sleepCommand()
	o.Timeout = time.Second
	rep, err := engine.Analyze(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK || !rep.Check.TimedOut {
		t.Fatalf("want timed-out failure: %+v", rep)
	}
}
