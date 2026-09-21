// Package engine runs one analysis pass: discover worktrees, merge every
// in-flight branch into a throwaway worktree, run the check, and if it fails,
// bisect to the smallest failing set of branches.
package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Denizprof/mergecanary/internal/bisect"
	"github.com/Denizprof/mergecanary/internal/check"
	"github.com/Denizprof/mergecanary/internal/integrate"
	"github.com/Denizprof/mergecanary/internal/worktrees"
)

// Options configures a pass.
type Options struct {
	Repo    string        // any path inside the repository
	Base    string        // base branch or ref, e.g. "main"
	Command string        // check command, run via the platform shell
	Timeout time.Duration // per check run; 0 = none
}

// Status is a branch's state in the integration.
type Status string

const (
	StatusOK       Status = "ok"
	StatusConflict Status = "conflict" // could not be merged
	StatusFail     Status = "fail"     // part of a failing set
)

// BranchReport describes one in-flight branch.
type BranchReport struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA    string `json:"sha"`
	Status Status `json:"status"`
}

// Finding is a minimal failing set of branches.
type Finding struct {
	Set     []string `json:"set"`
	Breaker string   `json:"breaker,omitempty"` // pairs only; see bisect.Attribute
	Victim  string   `json:"victim,omitempty"`
	Summary string   `json:"summary"`
	Output  string   `json:"output"` // tail of the failing check output
}

// CheckReport is the result of checking the full integration.
type CheckReport struct {
	Ran        bool  `json:"ran"`
	OK         bool  `json:"ok"`
	ExitCode   int   `json:"exit_code"`
	TimedOut   bool  `json:"timed_out,omitempty"`
	DurationMS int64 `json:"duration_ms"`
}

// Report is the result of one pass.
type Report struct {
	Base        string               `json:"base"`
	BaseSHA     string               `json:"base_sha"`
	OK          bool                 `json:"ok"`
	BaseFailing bool                 `json:"base_failing,omitempty"` // the check fails on base alone
	Branches    []BranchReport       `json:"branches"`
	Conflicts   []integrate.Conflict `json:"conflicts"`
	Check       CheckReport          `json:"check"`
	Findings    []Finding            `json:"findings"`
	Runs        int                  `json:"check_runs"` // total check invocations this pass
	CheckedAt   time.Time            `json:"checked_at"`
	DurationMS  int64                `json:"duration_ms"` // whole pass, including merges
}

// Analyze runs one pass. The temporary worktree is removed before it returns.
func Analyze(ctx context.Context, opt Options) (rep Report, _ error) {
	started := time.Now()
	rep = Report{Base: opt.Base, Conflicts: []integrate.Conflict{}, Findings: []Finding{}, Branches: []BranchReport{}}
	defer func() { rep.CheckedAt, rep.DurationMS = started.UTC(), time.Since(started).Milliseconds() }()

	all, err := worktrees.List(ctx, opt.Repo)
	if err != nil {
		return rep, err
	}
	agents := worktrees.Agents(all, opt.Base)
	sort.Slice(agents, func(i, j int) bool { return agents[i].Branch < agents[j].Branch })
	var branches []integrate.Branch
	byName := map[string]integrate.Branch{}
	paths := map[string]string{}
	for _, w := range agents {
		b := integrate.Branch{Name: w.Branch, SHA: w.Head}
		branches = append(branches, b)
		byName[b.Name] = b
		paths[b.Name] = w.Path
	}

	err = integrate.Run(ctx, opt.Repo, opt.Base, func(ctx context.Context, in *integrate.Integration) error {
		runCheck := func() (check.Result, error) {
			rep.Runs++
			return check.Run(ctx, in.Path, opt.Command, opt.Timeout)
		}

		build, err := in.Build(ctx, branches)
		if err != nil {
			return err
		}
		rep.BaseSHA = build.BaseSHA
		rep.Conflicts = append(rep.Conflicts, build.Conflicts...)
		rep.Check.Ran = false
		rep.Check.OK = true

		if len(build.Merged) > 0 {
			res, err := runCheck()
			if err != nil {
				return err
			}
			rep.Check = CheckReport{Ran: true, OK: res.OK, ExitCode: res.ExitCode, TimedOut: res.TimedOut, DurationMS: res.Duration.Milliseconds()}
			if !res.OK {
				if rep.Findings, err = explain(ctx, in, opt, &rep, build.Merged, byName, runCheck); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return rep, err
	}

	bad := map[string]Status{}
	for _, c := range rep.Conflicts {
		bad[c.Branch] = StatusConflict
		if c.With != "" {
			bad[c.With] = StatusConflict
		}
	}
	for _, f := range rep.Findings {
		for _, n := range f.Set {
			if bad[n] == "" {
				bad[n] = StatusFail
			}
		}
	}
	for _, b := range branches {
		st := bad[b.Name]
		if st == "" {
			st = StatusOK
		}
		rep.Branches = append(rep.Branches, BranchReport{Name: b.Name, Path: paths[b.Name], SHA: b.SHA, Status: st})
	}
	rep.OK = len(rep.Conflicts) == 0 && rep.Check.OK
	return rep, nil
}

// explain is called when the full integration failed its check. It first
// confirms base alone passes (otherwise no branch can be blamed), then bisects.
func explain(ctx context.Context, in *integrate.Integration, opt Options, rep *Report, merged []integrate.Branch,
	byName map[string]integrate.Branch, runCheck func() (check.Result, error)) ([]Finding, error) {

	if _, err := in.Reset(ctx); err != nil {
		return nil, err
	}
	baseRes, err := runCheck()
	if err != nil {
		return nil, err
	}
	if !baseRes.OK {
		rep.BaseFailing = true
		return nil, nil
	}

	names := make([]string, len(merged))
	for i, b := range merged {
		names[i] = b.Name
	}
	test := func(ctx context.Context, set []string) (bisect.Verdict, string, error) {
		sub := make([]integrate.Branch, len(set))
		for i, n := range set {
			sub[i] = byName[n]
		}
		build, err := in.Build(ctx, sub)
		if err != nil {
			return bisect.Pass, "", err
		}
		if len(build.Conflicts) > 0 {
			return bisect.Skip, "", nil
		}
		res, err := runCheck()
		if err != nil {
			return bisect.Pass, "", err
		}
		if res.OK {
			return bisect.Pass, "", nil
		}
		return bisect.Fail, res.Output, nil
	}
	found, err := bisect.Find(ctx, names, test)
	if err != nil {
		return nil, err
	}

	var out []Finding
	for _, f := range found.Findings {
		fd := Finding{Set: f.Set, Output: f.Output}
		if len(f.Set) == 2 {
			changed := map[string][]string{}
			for _, n := range f.Set {
				if changed[n], err = in.ChangedFiles(ctx, byName[n]); err != nil {
					return nil, err
				}
			}
			fd.Breaker, fd.Victim = bisect.Attribute([2]string{f.Set[0], f.Set[1]}, changed, f.Output)
		}
		fd.Summary = summary(fd)
		out = append(out, fd)
	}
	return out, nil
}

func summary(f Finding) string {
	switch {
	case len(f.Set) == 1:
		return f.Set[0] + " fails the check on its own"
	case f.Breaker != "":
		return f.Breaker + " breaks " + f.Victim
	case len(f.Set) == 2:
		return f.Set[0] + " and " + f.Set[1] + " fail together"
	default:
		return strings.Join(f.Set, ", ") + " fail together"
	}
}
