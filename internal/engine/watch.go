package engine

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/Denizprof/mergecanary/internal/gitcmd"
	"github.com/Denizprof/mergecanary/internal/worktrees"
)

// Fingerprint identifies the state a pass would analyze: the base tip and the
// branch and HEAD of every in-flight worktree. It is cheap (two git calls).
func Fingerprint(ctx context.Context, opt Options) (string, error) {
	all, err := worktrees.List(ctx, opt.Repo)
	if err != nil {
		return "", err
	}
	base, err := gitcmd.Run(ctx, opt.Repo, "rev-parse", "--verify", opt.Base+"^{commit}")
	if err != nil {
		return "", err
	}
	parts := []string{"base=" + strings.TrimSpace(base)}
	for _, w := range worktrees.Agents(all, opt.Base) {
		parts = append(parts, w.Branch+"="+w.Head)
	}
	sort.Strings(parts[1:])
	return strings.Join(parts, ";"), nil
}

// Event is emitted by Watch: a pass starting (Running), or its outcome
// (Report, or Err when the pass could not run).
type Event struct {
	Running bool
	Report  *Report
	Err     error
}

// Watch runs a pass immediately, then again whenever the fingerprint changes
// (polled about once a second) or interval has elapsed since the last pass
// finished. It returns nil when ctx is cancelled.
func Watch(ctx context.Context, opt Options, interval time.Duration, emit func(Event)) error {
	poll := time.Second
	if interval < poll {
		poll = interval
	}
	var lastFP string
	var last time.Time
	ran := false
	for {
		fp, ferr := Fingerprint(ctx, opt)
		if ctx.Err() != nil {
			return nil
		}
		switch {
		case ferr != nil && !ran:
			emit(Event{Err: ferr})
		case ferr == nil && (!ran || fp != lastFP || time.Since(last) >= interval):
			emit(Event{Running: true})
			rep, err := Analyze(ctx, opt)
			if ctx.Err() != nil {
				return nil
			}
			ran, lastFP, last = true, fp, time.Now() // fp from before the pass, so a move during it re-triggers
			if err != nil {
				emit(Event{Err: err})
			} else {
				emit(Event{Report: &rep})
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(poll):
		}
	}
}
