package worktrees_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Denizprof/mergecanary/internal/testutil"
	"github.com/Denizprof/mergecanary/internal/worktrees"
)

const porcelain = `worktree /r/main
HEAD aaaa
branch refs/heads/main

worktree /r/agent-1
HEAD bbbb
branch refs/heads/feat/agent-1
locked in use

worktree /r/detached
HEAD cccc
detached

worktree /r/gone
HEAD dddd
branch refs/heads/gone
prunable gitdir file points to non-existent location

worktree /r/bare.git
bare
`

func TestParse(t *testing.T) {
	got := worktrees.Parse(porcelain)
	want := []worktrees.Worktree{
		{Path: "/r/main", Head: "aaaa", Branch: "main"},
		{Path: "/r/agent-1", Head: "bbbb", Branch: "feat/agent-1", Locked: true},
		{Path: "/r/detached", Head: "cccc", Detached: true},
		{Path: "/r/gone", Head: "dddd", Branch: "gone", Prunable: true},
		{Path: "/r/bare.git", Bare: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestAgentsFilters(t *testing.T) {
	got := worktrees.Agents(worktrees.Parse(porcelain), "main")
	if len(got) != 1 || got[0].Branch != "feat/agent-1" {
		t.Fatalf("got %+v", got)
	}
}

func TestListFixture(t *testing.T) {
	f := testutil.Fixture(t)
	all, err := worktrees.List(context.Background(), f.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("want 4 worktrees, got %+v", all)
	}
	var names []string
	for _, w := range worktrees.Agents(all, "main") {
		names = append(names, w.Branch)
		if len(w.Head) != 40 {
			t.Errorf("%s: head %q is not a full SHA", w.Branch, w.Head)
		}
	}
	if !reflect.DeepEqual(names, []string{"agent-1", "agent-2", "agent-3"}) {
		t.Fatalf("agents = %v", names)
	}
	// listing works from any worktree
	if _, err := worktrees.List(context.Background(), f.Path("agent-2")); err != nil {
		t.Fatal(err)
	}
}
