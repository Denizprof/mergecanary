// Package integrate merges branches into a throwaway worktree.
//
// Everything happens in a temporary detached worktree that is removed by
// Cleanup. User branches, worktrees and working directories are never written:
// merges create commits on the detached HEAD only (their objects go into the
// shared object database and are unreferenced).
package integrate

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Denizprof/mergecanary/internal/gitcmd"
)

// Branch is a branch tip to integrate. SHA is what is merged, so results are
// tied to a specific commit.
type Branch struct {
	Name string
	SHA  string
}

// Integration is a temporary detached worktree used for speculative merges.
type Integration struct {
	Path string // the temp worktree

	repo string
	base string
	root string // temp dir containing Path
	once sync.Once
}

// New creates the temp worktree, detached at base. Callers must call Cleanup
// (or use Run, which does).
func New(ctx context.Context, repo, base string) (*Integration, error) {
	if _, err := gitcmd.Run(ctx, repo, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
		return nil, fmt.Errorf("base %q is not a commit in %s", base, repo)
	}
	root, err := os.MkdirTemp("", "mergecanary-")
	if err != nil {
		return nil, err
	}
	in := &Integration{repo: repo, base: base, root: root, Path: filepath.Join(root, "wt")}
	if _, err := gitcmd.Run(ctx, repo, "-c", "core.hooksPath="+os.DevNull, "worktree", "add", "--detach", in.Path, base); err != nil {
		in.Cleanup()
		return nil, err
	}
	return in, nil
}

// Cleanup removes the temp worktree. It is idempotent, safe for concurrent
// use, and does not depend on any caller context (which may be cancelled).
func (in *Integration) Cleanup() {
	in.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		gitcmd.Run(ctx, in.repo, "worktree", "remove", "--force", in.Path)
		os.RemoveAll(in.root)
		gitcmd.Run(ctx, in.repo, "worktree", "prune")
	})
}

// git runs git inside the temp worktree, with hooks disabled and a synthetic
// identity so no user config is read for authorship or written.
func (in *Integration) git(ctx context.Context, args ...string) (string, error) {
	full := append([]string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "user.name=mergecanary",
		"-c", "user.email=mergecanary@localhost",
		"-c", "commit.gpgsign=false",
	}, args...)
	return gitcmd.Run(ctx, in.Path, full...)
}

// Reset points the temp worktree at the current tip of base and returns its SHA.
func (in *Integration) Reset(ctx context.Context) (string, error) {
	out, err := gitcmd.Run(ctx, in.repo, "rev-parse", "--verify", in.base+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if err := in.hardReset(ctx, sha); err != nil {
		return "", err
	}
	return sha, nil
}

// hardReset makes the temp worktree exactly sha, removing untracked and
// ignored files so leftovers from a previous check cannot influence the next.
func (in *Integration) hardReset(ctx context.Context, sha string) error {
	if _, err := in.git(ctx, "reset", "--hard", "--quiet", sha); err != nil {
		return err
	}
	_, err := in.git(ctx, "clean", "-ffdxq")
	return err
}

// ChangedFiles lists the paths b changed relative to its merge-base with base.
func (in *Integration) ChangedFiles(ctx context.Context, b Branch) ([]string, error) {
	out, err := gitcmd.Run(ctx, in.repo, "diff", "--name-only", in.base+"..."+b.SHA)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// MergeResult is the outcome of merging one branch.
type MergeResult struct {
	Conflict bool
	Files    []string // conflicted paths, when Conflict
}

// Merge merges b into the current temp worktree state. On conflict it aborts
// the merge (leaving the state as before) and reports the conflicted files.
// A non-conflict failure is returned as an error.
func (in *Integration) Merge(ctx context.Context, b Branch) (MergeResult, error) {
	_, err := in.git(ctx, "merge", "--no-edit", b.SHA)
	if err == nil {
		return MergeResult{}, nil
	}
	out, derr := in.git(ctx, "diff", "--name-only", "--diff-filter=U")
	files := strings.Fields(out)
	if derr != nil || len(files) == 0 {
		in.git(context.Background(), "merge", "--abort")
		return MergeResult{}, fmt.Errorf("merging %s: %w", b.Name, err)
	}
	if _, aerr := in.git(context.Background(), "merge", "--abort"); aerr != nil {
		return MergeResult{}, fmt.Errorf("aborting merge of %s: %w", b.Name, aerr)
	}
	return MergeResult{Conflict: true, Files: files}, nil
}

// Conflict describes a branch that could not be merged.
type Conflict struct {
	Branch   string   `json:"branch"`
	With     string   `json:"with,omitempty"`      // the earlier branch it conflicts with, if a single one was found
	WithBase bool     `json:"with_base,omitempty"` // it conflicts with base itself
	Files    []string `json:"files"`
}

// BuildResult is the outcome of building the integration.
type BuildResult struct {
	BaseSHA   string
	Merged    []Branch // branches present in the integration, in merge order
	Conflicts []Conflict
}

// Build resets to base and merges each branch in order. A branch that
// conflicts is skipped and recorded; the rest continue. On return the temp
// worktree holds base plus every branch in Merged.
func (in *Integration) Build(ctx context.Context, branches []Branch) (BuildResult, error) {
	base, err := in.Reset(ctx)
	if err != nil {
		return BuildResult{}, err
	}
	res := BuildResult{BaseSHA: base}
	for _, b := range branches {
		r, err := in.Merge(ctx, b)
		if err != nil {
			return res, err
		}
		if !r.Conflict {
			res.Merged = append(res.Merged, b)
			continue
		}
		c, err := in.attribute(ctx, b, res.Merged, r.Files)
		if err != nil {
			return res, err
		}
		res.Conflicts = append(res.Conflicts, c)
		// attribute leaves the worktree in an arbitrary state; restore it.
		if err := in.replay(ctx, base, res.Merged); err != nil {
			return res, err
		}
	}
	return res, nil
}

// attribute finds which single earlier branch (or base) b conflicts with.
func (in *Integration) attribute(ctx context.Context, b Branch, merged []Branch, files []string) (Conflict, error) {
	c := Conflict{Branch: b.Name, Files: files}
	base, err := in.Reset(ctx)
	if err != nil {
		return c, err
	}
	r, err := in.Merge(ctx, b)
	if err != nil {
		return c, err
	}
	if r.Conflict {
		c.WithBase, c.Files = true, r.Files
		return c, nil
	}
	for _, a := range merged {
		if err := in.replay(ctx, base, []Branch{a}); err != nil {
			return c, err
		}
		r, err := in.Merge(ctx, b)
		if err != nil {
			return c, err
		}
		if r.Conflict {
			c.With, c.Files = a.Name, r.Files
			return c, nil
		}
	}
	return c, nil // conflict only arises from a combination of several branches
}

// replay resets to base and merges the branches, all of which merged cleanly before.
func (in *Integration) replay(ctx context.Context, base string, branches []Branch) error {
	if err := in.hardReset(ctx, base); err != nil {
		return err
	}
	for _, b := range branches {
		r, err := in.Merge(ctx, b)
		if err != nil {
			return err
		}
		if r.Conflict {
			return fmt.Errorf("replaying %s conflicted unexpectedly", b.Name)
		}
	}
	return nil
}

// Run creates an Integration, calls fn, and always cleans up: on return, on
// panic (the panic keeps propagating), and on SIGINT/SIGTERM (which cancel the
// context passed to fn). It cannot help if the process is SIGKILLed.
func Run(ctx context.Context, repo, base string, fn func(context.Context, *Integration) error) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	in, err := New(ctx, repo, base)
	if err != nil {
		return err
	}
	defer in.Cleanup()
	return fn(ctx, in)
}
