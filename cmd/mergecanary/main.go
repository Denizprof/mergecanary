// Command mergecanary checks whether in-flight git worktree branches still
// work together when merged: it merges them all into a throwaway worktree,
// runs your check command, and if that fails, finds which branch caused it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Denizprof/mergecanary/internal/board"
	"github.com/Denizprof/mergecanary/internal/engine"
	"github.com/Denizprof/mergecanary/internal/gitcmd"
	"github.com/Denizprof/mergecanary/internal/worktrees"
)

// Exit codes.
const (
	exitOK          = 0
	exitFail        = 1   // --once: conflicts or a failing check
	exitUsage       = 2   // bad flags, not a repo, bad base, internal error
	exitInterrupted = 130 // SIGINT/SIGTERM
)

const usage = `mergecanary: do your parallel branches still work TOGETHER?

Usage:
  mergecanary watch --check CMD [flags]

Merges every in-flight worktree branch into a throwaway worktree, runs CMD there,
and if it fails, finds the smallest set of branches that breaks it.

Flags:
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	switch args[0] {
	case "watch":
		return watch(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		newFlags(&watchOpts{}, stdout).PrintDefaults()
		return exitOK
	}
	fmt.Fprintf(stderr, "mergecanary: unknown command %q\n\n%s", args[0], usage)
	return exitUsage
}

type watchOpts struct {
	check    string
	base     string
	repo     string
	interval time.Duration
	timeout  time.Duration
	once     bool
	json     bool
}

// seconds accepts "10" (seconds) or a Go duration such as "1m30s".
type seconds struct{ d *time.Duration }

func (s seconds) String() string {
	if s.d == nil {
		return ""
	}
	return s.d.String()
}

func (s seconds) Set(v string) error {
	if n, err := strconv.Atoi(v); err == nil {
		*s.d = time.Duration(n) * time.Second
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return errors.New("want seconds (10) or a duration (1m30s)")
	}
	*s.d = d
	return nil
}

func newFlags(o *watchOpts, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.check, "check", "", "command to run on the merged result, e.g. \"npm test\" (required)")
	fs.StringVar(&o.base, "base", "main", "base branch every other branch is merged onto")
	fs.StringVar(&o.repo, "repo", ".", "path inside the git repository")
	o.interval, o.timeout = 10*time.Second, 5*time.Minute
	fs.Var(seconds{&o.interval}, "interval", "re-check at least this often, seconds or a duration")
	fs.Var(seconds{&o.timeout}, "timeout", "kill a check run after this long; 0 = no limit")
	fs.BoolVar(&o.once, "once", false, "run a single pass and exit: 0 all green, 1 failure, 2 error")
	fs.BoolVar(&o.json, "json", false, "print reports as JSON (one line each; indented with --once)")
	return fs
}

func watch(args []string, stdout, stderr io.Writer) int {
	var o watchOpts
	fs := newFlags(&o, stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if o.check == "" {
		fmt.Fprintln(stderr, "mergecanary: --check is required")
		return exitUsage
	}
	if o.interval <= 0 {
		fmt.Fprintln(stderr, "mergecanary: --interval must be positive")
		return exitUsage
	}
	repo, err := filepath.Abs(o.repo)
	if err != nil {
		fmt.Fprintln(stderr, "mergecanary:", err)
		return exitUsage
	}
	opt := engine.Options{Repo: repo, Base: o.base, Command: o.check, Timeout: o.timeout}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fail fast on configuration errors instead of looping on them.
	if _, err := worktrees.List(ctx, repo); err != nil {
		fmt.Fprintf(stderr, "mergecanary: %s is not inside a git repository (%v)\n", repo, err)
		return exitUsage
	}
	if _, err := gitcmd.Run(ctx, repo, "rev-parse", "--verify", "--quiet", o.base+"^{commit}"); err != nil {
		fmt.Fprintf(stderr, "mergecanary: base %q is not a branch or commit in this repository\n", o.base)
		return exitUsage
	}

	if o.once {
		return once(ctx, opt, o.json, stdout, stderr)
	}
	return loop(ctx, opt, o, stdout, stderr)
}

func once(ctx context.Context, opt engine.Options, asJSON bool, stdout, stderr io.Writer) int {
	rep, err := engine.Analyze(ctx, opt)
	if ctx.Err() != nil {
		return exitInterrupted
	}
	if err != nil {
		fmt.Fprintln(stderr, "mergecanary:", err)
		return exitUsage
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		fmt.Fprint(stdout, board.Render(board.View{Report: &rep}, false))
	}
	if rep.OK {
		return exitOK
	}
	return exitFail
}

func loop(ctx context.Context, opt engine.Options, o watchOpts, stdout, stderr io.Writer) int {
	out, _ := stdout.(*os.File)
	tty := !o.json && out != nil && board.IsTerminal(out) && os.Getenv("NO_COLOR") == ""
	footer := fmt.Sprintf("watching: re-checks every %s or when a branch moves. Ctrl-C to quit.", o.interval)

	var view board.View
	view.Footer = footer
	rd := &board.Redrawer{W: stdout}
	if tty {
		board.Clear(stdout)
	}
	enc := json.NewEncoder(stdout)

	err := engine.Watch(ctx, opt, o.interval, func(ev engine.Event) {
		if o.json {
			switch {
			case ev.Report != nil:
				enc.Encode(ev.Report)
			case ev.Err != nil:
				fmt.Fprintln(stderr, "mergecanary:", ev.Err)
			}
			return
		}
		view.Running, view.Err = ev.Running, ev.Err
		if ev.Report != nil {
			view.Report = ev.Report
		}
		switch {
		case tty:
			rd.Draw(board.Render(view, true))
		case !ev.Running: // plain output: one block per finished pass
			fmt.Fprintln(stdout, board.Render(board.View{Report: view.Report, Err: ev.Err}, false))
		}
	})
	if err != nil {
		fmt.Fprintln(stderr, "mergecanary:", err)
		return exitUsage
	}
	if ctx.Err() != nil {
		return exitInterrupted
	}
	return exitOK
}
