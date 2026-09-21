package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Denizprof/mergecanary/internal/engine"
	"github.com/Denizprof/mergecanary/internal/testutil"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  string
)

// binary returns the mergecanary executable: $MC_BIN if set (lets a
// cross-compiled binary be tested where Go is absent), otherwise a fresh build.
func binary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("MC_BIN"); p != "" {
		return p
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH and MC_BIN not set")
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "mc-bin-")
		if err != nil {
			buildErr = err.Error()
			return
		}
		binPath = filepath.Join(dir, "mergecanary")
		if runtime.GOOS == "windows" {
			binPath += ".exe"
		}
		if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
			buildErr = string(out)
		}
	})
	if buildErr != "" {
		t.Fatalf("go build: %s", buildErr)
	}
	return binPath
}

func needGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; the fixture check is `go test ./...`")
	}
}

// cmd builds a command whose temp dir is private, so leaks are detectable.
func cmd(t *testing.T, tmp string, args ...string) *exec.Cmd {
	t.Helper()
	c := exec.Command(binary(t), args...)
	c.Env = append(os.Environ(), "TMP="+tmp, "TEMP="+tmp, "TMPDIR="+tmp, "NO_COLOR=1")
	return c
}

func leaked(t *testing.T, tmp string) []string {
	t.Helper()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "mergecanary-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -999
}

func runOnce(t *testing.T, f testutil.Fix, extra ...string) (stdout, stderr string, code int, tmp string) {
	t.Helper()
	tmp = t.TempDir()
	args := append([]string{"watch", "--once", "--repo", f.Repo, "--check", "go test ./..."}, extra...)
	c := cmd(t, tmp, args...)
	var so, se bytes.Buffer
	c.Stdout, c.Stderr = &so, &se
	code = exitCode(c.Run())
	return so.String(), se.String(), code, tmp
}

func TestOnceFixtureExitsNonZeroAndNamesCulprit(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	out, errOut, code, tmp := runOnce(t, f)
	t.Logf("exit=%d\n%s", code, out)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", code, errOut)
	}
	for _, want := range []string{"FAIL", "agent-3 breaks agent-1", "PASS"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q", want)
		}
	}
	if l := leaked(t, tmp); len(l) != 0 {
		t.Errorf("temp worktrees leaked: %v", l)
	}
}

func TestOnceJSON(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	out, errOut, code, _ := runOnce(t, f, "--json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", code, errOut)
	}
	var rep engine.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if rep.OK || len(rep.Findings) != 1 || rep.Findings[0].Breaker != "agent-3" || rep.Findings[0].Victim != "agent-1" {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if rep.Base != "main" || len(rep.Branches) != 3 || rep.CheckedAt.IsZero() {
		t.Fatalf("unexpected report: %+v", rep)
	}
	// stable field names for scripting
	var raw map[string]any
	json.Unmarshal([]byte(out), &raw)
	for _, k := range []string{"ok", "base", "base_sha", "branches", "conflicts", "check", "findings", "check_runs", "checked_at"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("JSON lacks %q", k)
		}
	}
}

func TestOnceGreenExitsZero(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	testutil.Git(t, f.Repo, "worktree", "remove", "--force", f.Path("agent-3"))
	out, errOut, code, _ := runOnce(t, f)
	if code != 0 || !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
		t.Fatalf("exit=%d\n%s\nstderr: %s", code, out, errOut)
	}
}

func TestUsageAndConfigErrorsExit2(t *testing.T) {
	f := testutil.Fixture(t)
	notRepo := t.TempDir()
	cases := map[string][]string{
		"no args":        {},
		"unknown cmd":    {"frobnicate"},
		"missing check":  {"watch", "--once", "--repo", f.Repo},
		"bad base":       {"watch", "--once", "--repo", f.Repo, "--check", "true", "--base", "nope"},
		"not a repo":     {"watch", "--once", "--repo", notRepo, "--check", "true"},
		"bad interval":   {"watch", "--once", "--repo", f.Repo, "--check", "true", "--interval", "abc"},
		"zero interval":  {"watch", "--once", "--repo", f.Repo, "--check", "true", "--interval", "0"},
		"unknown option": {"watch", "--once", "--repo", f.Repo, "--check", "true", "--nope"},
	}
	for name, args := range cases {
		c := cmd(t, t.TempDir(), args...)
		var se bytes.Buffer
		c.Stderr = &se
		if code := exitCode(c.Run()); code != 2 {
			t.Errorf("%s: exit = %d, want 2 (stderr: %s)", name, code, se.String())
		} else if se.Len() == 0 {
			t.Errorf("%s: no message on stderr", name)
		}
	}
}

func TestIntervalAcceptsSecondsAndDurations(t *testing.T) {
	var d time.Duration
	s := seconds{&d}
	for in, want := range map[string]time.Duration{"10": 10 * time.Second, "1m30s": 90 * time.Second, "0": 0, "250ms": 250 * time.Millisecond} {
		if err := s.Set(in); err != nil || d != want {
			t.Errorf("%q -> %v, %v; want %v", in, d, err, want)
		}
	}
	if err := s.Set("soon"); err == nil {
		t.Error("expected error for garbage")
	}
}

// startWatch launches `watch --json` and streams its stdout lines.
func startWatch(t *testing.T, tmp string, args ...string) (*exec.Cmd, <-chan string, *bytes.Buffer) {
	t.Helper()
	c := cmd(t, tmp, append([]string{"watch", "--json"}, args...)...)
	var se bytes.Buffer
	c.Stderr = &se
	stdout, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	t.Cleanup(func() { c.Process.Kill(); c.Wait() })
	return c, lines, &se
}

func nextReport(t *testing.T, lines <-chan string, within time.Duration) engine.Report {
	t.Helper()
	select {
	case l, ok := <-lines:
		if !ok {
			t.Fatal("watch exited before emitting a report")
		}
		var r engine.Report
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, l)
		}
		return r
	case <-time.After(within):
		t.Fatal("timed out waiting for a report")
	}
	return engine.Report{}
}

// The interval is one hour, so a second report can only come from the
// HEAD-change trigger.
func TestWatchRerunsWhenAWorktreeHeadMoves(t *testing.T) {
	needGo(t)
	f := testutil.Fixture(t)
	tmp := t.TempDir()
	c, lines, stderr := startWatch(t, tmp, "--repo", f.Repo, "--check", "go test ./...", "--interval", "1h")

	first := nextReport(t, lines, 2*time.Minute)
	if first.OK || len(first.Findings) != 1 || first.Findings[0].Summary != "agent-3 breaks agent-1" {
		t.Fatalf("first report: %+v", first)
	}

	// agent-3 adds a compatibility shim for the old name: the combination is fixed.
	shim := "package fixture\n\nfunc getUser(id int) string { return fetchUser(id) }\n"
	if err := os.WriteFile(filepath.Join(f.Path("agent-3"), "compat.go"), []byte(shim), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.Path("agent-3"), "add", "-A")
	testutil.Git(t, f.Path("agent-3"), "commit", "-q", "-m", "shim")

	second := nextReport(t, lines, 2*time.Minute)
	if !second.OK || len(second.Findings) != 0 {
		t.Fatalf("second report should be green: %+v\nstderr: %s", second, stderr.String())
	}
	if second.CheckedAt.Before(first.CheckedAt) {
		t.Fatal("second report is not newer")
	}

	if runtime.GOOS == "windows" {
		c.Process.Kill() // no graceful signal to a child on Windows; no pass is running now
		c.Wait()
	} else {
		c.Process.Signal(syscall.SIGINT)
		if code := exitCode(c.Wait()); code != 130 {
			t.Errorf("exit after SIGINT = %d, want 130", code)
		}
	}
	if l := leaked(t, tmp); len(l) != 0 {
		t.Errorf("temp worktrees leaked: %v", l)
	}
}

// SIGINT while a check is running: the temp worktree must be gone afterwards.
func TestSIGINTDuringCheckRemovesTempWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot deliver SIGINT to a child process on Windows")
	}
	f := testutil.Fixture(t)
	tmp := t.TempDir()
	c, _, _ := startWatch(t, tmp, "--repo", f.Repo, "--check", "sleep 60")

	deadline := time.Now().Add(30 * time.Second)
	for len(leaked(t, tmp)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("temp worktree never appeared")
		}
		time.Sleep(50 * time.Millisecond)
	}
	list := testutil.Git(t, f.Repo, "worktree", "list", "--porcelain")
	if strings.Count(list, "worktree ") != 5 {
		t.Fatalf("expected the temp worktree to be registered while running:\n%s", list)
	}

	start := time.Now()
	c.Process.Signal(syscall.SIGINT)
	code := exitCode(c.Wait())
	if code != 130 {
		t.Errorf("exit = %d, want 130", code)
	}
	if time.Since(start) > 20*time.Second {
		t.Errorf("took %v to shut down", time.Since(start))
	}
	if l := leaked(t, tmp); len(l) != 0 {
		t.Errorf("temp worktree dir leaked: %v", l)
	}
	list = testutil.Git(t, f.Repo, "worktree", "list", "--porcelain")
	if n := strings.Count(list, "worktree "); n != 4 {
		t.Errorf("worktree still registered after SIGINT (%d entries):\n%s", n, list)
	}
}

func TestVersionFlag(t *testing.T) {
	for _, arg := range []string{"--version", "version", "-v"} {
		out, err := cmd(t, t.TempDir(), arg).Output()
		if err != nil || !strings.HasPrefix(string(out), "mergecanary ") {
			t.Errorf("%s: out=%q err=%v", arg, out, err)
		}
	}
}
