// Package worktrees discovers the git worktrees of a repository.
package worktrees

import (
	"context"
	"strings"

	"github.com/Denizprof/mergecanary/internal/gitcmd"
)

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	Head     string // commit SHA
	Branch   string // short name, empty when detached or bare
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// List returns all worktrees of the repository containing dir.
func List(ctx context.Context, dir string) ([]Worktree, error) {
	out, err := gitcmd.Run(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return Parse(out), nil
}

// Parse parses `git worktree list --porcelain` output.
func Parse(out string) []Worktree {
	var list []Worktree
	var cur *Worktree
	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if line == "" {
			flush()
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		if key == "worktree" {
			flush()
			cur = &Worktree{Path: val}
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "locked":
			cur.Locked = true
		case "prunable":
			cur.Prunable = true
		}
	}
	flush()
	return list
}

// Agents returns the worktrees whose branches should be integrated: those on a
// branch, other than the base branch, that are not bare or prunable.
// Detached-HEAD worktrees (including mergecanary's own temp worktree) are skipped.
func Agents(all []Worktree, base string) []Worktree {
	var out []Worktree
	for _, w := range all {
		if w.Bare || w.Prunable || w.Detached || w.Branch == "" || w.Branch == base {
			continue
		}
		out = append(out, w)
	}
	return out
}
