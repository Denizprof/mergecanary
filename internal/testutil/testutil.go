// Package testutil builds the demo fixture for tests. Test-only; not imported by the binary.
package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Denizprof/mergecanary/internal/gitcmd"
)

// Fix is a fixture built by testdata/make-fixture.sh.
type Fix struct {
	Dir  string // contains repo, agent-1, agent-2, agent-3
	Repo string // main checkout
}

// Path returns the worktree directory of the named fixture entry.
func (f Fix) Path(name string) string { return filepath.Join(f.Dir, name) }

func bashPath() string {
	if runtime.GOOS == "windows" {
		// Avoid the WSL bash that may sit first on PATH.
		for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
			if pf := os.Getenv(env); pf != "" {
				p := filepath.Join(pf, "Git", "bin", "bash.exe")
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	p, _ := exec.LookPath("bash")
	return p
}

// Fixture runs make-fixture.sh into a fresh temp dir.
func Fixture(t testing.TB) Fix {
	t.Helper()
	bash := bashPath()
	if bash == "" {
		t.Skip("bash not found")
	}
	_, file, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(file), "..", "..", "testdata", "make-fixture.sh")
	dir := t.TempDir()
	cmd := exec.Command(bash, filepath.ToSlash(script), filepath.ToSlash(dir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make-fixture.sh: %v\n%s", err, out)
	}
	return Fix{Dir: dir, Repo: filepath.Join(dir, "repo")}
}

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := gitcmd.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// AddBranch adds a worktree Dir/name on a new branch off main, writes files
// (path -> content), and commits them. It returns the branch tip SHA.
func AddBranch(t testing.TB, f Fix, name string, files map[string]string) string {
	t.Helper()
	dir := f.Path(name)
	Git(t, f.Repo, "worktree", "add", "-q", "-b", name, dir, "main")
	for p, c := range files {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "-m", name)
	return Git(t, dir, "rev-parse", "HEAD")
}

// Snapshot captures all branch refs plus the HEAD and status (including
// ignored files) of every checkout under Dir, to prove nothing was touched.
func Snapshot(t testing.TB, f Fix) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(Git(t, f.Repo, "for-each-ref"))
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(f.Dir, e.Name(), ".git")); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		d := f.Path(n)
		b.WriteString("\n== " + n + "\nHEAD " + Git(t, d, "rev-parse", "HEAD"))
		b.WriteString("\n" + Git(t, d, "status", "--porcelain", "--ignored"))
	}
	return b.String()
}
