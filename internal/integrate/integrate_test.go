package integrate_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Denizprof/mergecanary/internal/integrate"
	"github.com/Denizprof/mergecanary/internal/testutil"
)

func branch(t *testing.T, f testutil.Fix, name string) integrate.Branch {
	t.Helper()
	return integrate.Branch{Name: name, SHA: testutil.Git(t, f.Path(name), "rev-parse", "HEAD")}
}

func worktreeListed(t *testing.T, f testutil.Fix, path string) bool {
	t.Helper()
	out := testutil.Git(t, f.Repo, "worktree", "list", "--porcelain")
	return strings.Contains(out, filepath.ToSlash(path))
}

func TestNewAndCleanup(t *testing.T) {
	f := testutil.Fixture(t)
	ctx := context.Background()
	in, err := integrate.New(ctx, f.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(in.Path, "users.go")); err != nil {
		t.Fatalf("temp worktree not populated: %v", err)
	}
	if !worktreeListed(t, f, in.Path) {
		t.Fatal("temp worktree not registered with git")
	}
	in.Cleanup()
	in.Cleanup() // idempotent
	if _, err := os.Stat(in.Path); !os.IsNotExist(err) {
		t.Fatalf("temp worktree dir still exists: %v", err)
	}
	if worktreeListed(t, f, in.Path) {
		t.Fatal("temp worktree still registered with git")
	}
}

func TestNewBadBase(t *testing.T) {
	f := testutil.Fixture(t)
	if _, err := integrate.New(context.Background(), f.Repo, "nope"); err == nil {
		t.Fatal("expected error for unknown base")
	}
}

func TestBuildCleanMergesLeaveRealReposUntouched(t *testing.T) {
	f := testutil.Fixture(t)
	before := testutil.Snapshot(t, f)
	var path string
	err := integrate.Run(context.Background(), f.Repo, "main", func(ctx context.Context, in *integrate.Integration) error {
		path = in.Path
		res, err := in.Build(ctx, []integrate.Branch{branch(t, f, "agent-1"), branch(t, f, "agent-2"), branch(t, f, "agent-3")})
		if err != nil {
			return err
		}
		if len(res.Merged) != 3 || len(res.Conflicts) != 0 {
			t.Errorf("want 3 merged, 0 conflicts; got %+v", res)
		}
		// The integration really contains all branches' work: agent-1 still
		// calls getUser, agent-3 renamed it (this is the semantic break).
		for file, want := range map[string]string{"profile.go": "getUser", "greet.go": "greet", "users.go": "fetchUser"} {
			b, err := os.ReadFile(filepath.Join(in.Path, file))
			if err != nil || !strings.Contains(string(b), want) {
				t.Errorf("%s missing %q (err=%v)", file, want, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if after := testutil.Snapshot(t, f); after != before {
		t.Fatalf("real repos changed:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temp worktree not removed after Run")
	}
}

const conflictBody = `package fixture

import "fmt"

func getUser(id int) string {
	return fmt.Sprintf("%s-%%d", id)
}
`

func TestBuildConflictAttribution(t *testing.T) {
	f := testutil.Fixture(t)
	testutil.AddBranch(t, f, "conflict-a", map[string]string{"users.go": fmt.Sprintf(conflictBody, "A")})
	testutil.AddBranch(t, f, "conflict-b", map[string]string{"users.go": fmt.Sprintf(conflictBody, "B")})
	before := testutil.Snapshot(t, f)

	err := integrate.Run(context.Background(), f.Repo, "main", func(ctx context.Context, in *integrate.Integration) error {
		// agent-2 is unrelated; conflict-b must be blamed on conflict-a, not agent-2.
		res, err := in.Build(ctx, []integrate.Branch{branch(t, f, "agent-2"), branch(t, f, "conflict-a"), branch(t, f, "conflict-b"), branch(t, f, "agent-1")})
		if err != nil {
			return err
		}
		want := []integrate.Conflict{{Branch: "conflict-b", With: "conflict-a", Files: []string{"users.go"}}}
		if !reflect.DeepEqual(res.Conflicts, want) {
			t.Errorf("conflicts = %+v, want %+v", res.Conflicts, want)
		}
		var merged []string
		for _, b := range res.Merged {
			merged = append(merged, b.Name)
		}
		if !reflect.DeepEqual(merged, []string{"agent-2", "conflict-a", "agent-1"}) {
			t.Errorf("merged = %v (a conflict must not stop later branches)", merged)
		}
		// worktree is restored to a clean state holding exactly the merged set
		if s := testutil.Git(t, in.Path, "status", "--porcelain"); s != "" {
			t.Errorf("temp worktree dirty after conflict: %q", s)
		}
		if _, err := os.Stat(filepath.Join(in.Path, "profile.go")); err != nil {
			t.Errorf("agent-1 missing after conflict recovery: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if after := testutil.Snapshot(t, f); after != before {
		t.Fatalf("real repos changed:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

func TestBuildConflictWithBase(t *testing.T) {
	f := testutil.Fixture(t)
	testutil.AddBranch(t, f, "old", map[string]string{"users.go": fmt.Sprintf(conflictBody, "OLD")})
	// main moves on and changes the same lines
	if err := os.WriteFile(filepath.Join(f.Repo, "users.go"), []byte(fmt.Sprintf(conflictBody, "NEW")), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, f.Repo, "commit", "-q", "-am", "main moves")

	err := integrate.Run(context.Background(), f.Repo, "main", func(ctx context.Context, in *integrate.Integration) error {
		res, err := in.Build(ctx, []integrate.Branch{branch(t, f, "agent-2"), branch(t, f, "old")})
		if err != nil {
			return err
		}
		want := []integrate.Conflict{{Branch: "old", WithBase: true, Files: []string{"users.go"}}}
		if !reflect.DeepEqual(res.Conflicts, want) {
			t.Errorf("conflicts = %+v, want %+v", res.Conflicts, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBuildFollowsBaseMoving(t *testing.T) {
	f := testutil.Fixture(t)
	in, err := integrate.New(context.Background(), f.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Cleanup()
	first, _ := in.Reset(context.Background())
	testutil.Git(t, f.Repo, "commit", "-q", "--allow-empty", "-m", "more")
	second, _ := in.Reset(context.Background())
	if first == second || second != testutil.Git(t, f.Repo, "rev-parse", "main") {
		t.Fatalf("Reset did not follow base: %s -> %s", first, second)
	}
}

func TestCleanupOnPanic(t *testing.T) {
	f := testutil.Fixture(t)
	var path string
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed; it must keep propagating")
			}
		}()
		integrate.Run(context.Background(), f.Repo, "main", func(ctx context.Context, in *integrate.Integration) error {
			path = in.Path
			panic("boom")
		})
	}()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temp worktree not removed after panic")
	}
	if worktreeListed(t, f, path) {
		t.Fatal("temp worktree still registered after panic")
	}
}

func TestCleanupOnCancelledContext(t *testing.T) {
	f := testutil.Fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	var path string
	err := integrate.Run(ctx, f.Repo, "main", func(ctx context.Context, in *integrate.Integration) error {
		path = in.Path
		cancel()
		<-ctx.Done()
		return ctx.Err() // cleanup must not reuse this cancelled ctx
	})
	if err == nil {
		t.Fatal("expected context error")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temp worktree not removed after cancel")
	}
	if worktreeListed(t, f, path) {
		t.Fatal("temp worktree still registered after cancel")
	}
}

// TestCleanupOnSIGINT sends a real SIGINT to a child process that is holding an
// integration worktree. Helper mode is selected by MC_HELPER_REPO.
func TestCleanupOnSIGINT(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot deliver SIGINT to a child process on Windows")
	}
	f := testutil.Fixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldIntegration$")
	cmd.Env = append(os.Environ(), "MC_HELPER_REPO="+f.Repo)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var path string
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if p, ok := strings.CutPrefix(sc.Text(), "READY "); ok {
			path = p
			break
		}
	}
	if path == "" {
		cmd.Process.Kill()
		t.Fatal("helper never reported READY")
	}
	if !worktreeListed(t, f, path) {
		t.Fatal("helper worktree not registered before SIGINT")
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		t.Fatal("helper did not exit after SIGINT")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("temp worktree not removed after SIGINT")
	}
	if worktreeListed(t, f, path) {
		t.Fatal("temp worktree still registered after SIGINT")
	}
}

func TestHelperHoldIntegration(t *testing.T) {
	repo := os.Getenv("MC_HELPER_REPO")
	if repo == "" {
		t.Skip("helper process only")
	}
	integrate.Run(context.Background(), repo, "main", func(ctx context.Context, in *integrate.Integration) error {
		fmt.Printf("READY %s\n", in.Path)
		<-ctx.Done()
		return ctx.Err()
	})
}

func TestResetRemovesUntrackedAndIgnoredFiles(t *testing.T) {
	f := testutil.Fixture(t)
	in, err := integrate.New(context.Background(), f.Repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Cleanup()
	stale := []string{"stale.txt", "stale.test.exe"}
	for _, n := range stale {
		if err := os.WriteFile(filepath.Join(in.Path, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(in.Path, ".gitignore"), []byte("*.exe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, n := range append(stale, ".gitignore") {
		if _, err := os.Stat(filepath.Join(in.Path, n)); !os.IsNotExist(err) {
			t.Errorf("%s survived Reset (err=%v)", n, err)
		}
	}
}

// A user's git hooks must not run for anything mergecanary does, including
// creating the worktree (which triggers post-checkout).
func TestUserHooksDoNotRun(t *testing.T) {
	f := testutil.Fixture(t)
	marker := filepath.ToSlash(filepath.Join(f.Dir, "hook-ran"))
	hooks := filepath.Join(f.Repo, ".git", "hooks")
	script := "#!/bin/sh\necho ran >> '" + marker + "'\n"
	for _, h := range []string{"post-checkout", "post-merge", "pre-merge-commit", "commit-msg", "post-commit"} {
		if err := os.WriteFile(filepath.Join(hooks, h), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	err := integrate.Run(context.Background(), f.Repo, "main", func(ctx context.Context, in *integrate.Integration) error {
		_, err := in.Build(ctx, []integrate.Branch{branch(t, f, "agent-1"), branch(t, f, "agent-2"), branch(t, f, "agent-3")})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.Dir, "hook-ran")); !os.IsNotExist(err) {
		t.Fatal("a user git hook ran")
	}
}
