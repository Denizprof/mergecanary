// Package board renders an engine.Report as a terminal status board.
package board

import (
	"fmt"
	"strings"
	"time"

	"github.com/Denizprof/mergecanary/internal/engine"
	"github.com/Denizprof/mergecanary/internal/integrate"
)

// View is everything the board shows.
type View struct {
	Report  *engine.Report // latest completed pass, nil before the first
	Running bool           // a pass is in progress
	Err     error          // the last pass could not run
	Footer  string         // optional dim line at the bottom
}

// MaxOutputLines is how many trailing lines of failing check output are shown.
const MaxOutputLines = 10

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
)

// Render returns the board as text, one line per branch, overall status on top.
// With color=false it contains no escape sequences. Lines end in "\n".
func Render(v View, color bool) string {
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + reset
	}
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	rep := v.Report
	overall, code := "CHECKING", yellow
	switch {
	case rep != nil && v.Running:
		overall, code = "RE-CHECKING", yellow
	case rep != nil && rep.OK:
		overall, code = "PASS", green
	case rep != nil:
		overall, code = "FAIL", red
	}
	line("%s  %s", paint(bold, "mergecanary"), paint(bold+code, overall))

	if rep == nil {
		if v.Err != nil {
			line("%s", paint(red, "error: "+oneLine(v.Err.Error())))
		} else {
			line("%s", paint(dim, "running first pass..."))
		}
		writeFooter(&b, v, paint)
		return b.String()
	}

	line("%s", paint(dim, fmt.Sprintf("base %s@%s   %s   checked %s   %d check run%s in %s",
		rep.Base, short(rep.BaseSHA), plural(len(rep.Branches), "branch", "branches"),
		rep.CheckedAt.Local().Format("15:04:05"), rep.Runs, sOf(rep.Runs), dur(rep.DurationMS))))
	if v.Err != nil {
		line("%s", paint(red, "error (showing previous result): "+oneLine(v.Err.Error())))
	}
	line("")

	if len(rep.Branches) == 0 {
		line("  %s", paint(dim, "no in-flight branches: no worktrees on a branch other than "+rep.Base))
	}
	width := 0
	for _, br := range rep.Branches {
		if len(br.Name) > width {
			width = len(br.Name)
		}
	}
	for _, br := range rep.Branches {
		word, col := "PASS", green
		switch br.Status {
		case engine.StatusFail:
			word, col = "FAIL", red
		case engine.StatusConflict:
			word, col = "CONFLICT", red
		}
		text := fmt.Sprintf("  %s  %s", paint(col, fmt.Sprintf("%-8s", word)), br.Name)
		if d := detail(rep, br); d != "" {
			text += strings.Repeat(" ", width-len(br.Name)+2) + paint(dim, d)
		}
		line("%s", text)
	}

	switch {
	case rep.BaseFailing:
		line("")
		line("  %s", paint(red, "the check fails on "+rep.Base+" alone; branches were not bisected"))
	case len(rep.Findings) > 0:
		line("")
		for _, f := range rep.Findings {
			line("  %s", paint(bold+red, f.Summary))
			for _, l := range lastLines(f.Output, MaxOutputLines) {
				line("    %s", paint(dim, l))
			}
		}
	case rep.Check.TimedOut:
		line("")
		line("  %s", paint(red, "the check timed out"))
	}
	if len(rep.Conflicts) > 0 {
		line("")
		for _, c := range rep.Conflicts {
			line("  %s", paint(red, conflictText(c)))
		}
	}
	writeFooter(&b, v, paint)
	return b.String()
}

func writeFooter(b *strings.Builder, v View, paint func(string, string) string) {
	if v.Footer != "" {
		fmt.Fprintf(b, "\n%s\n", paint(dim, v.Footer))
	}
}

// detail is the dim text after a branch name.
func detail(rep *engine.Report, br engine.BranchReport) string {
	switch br.Status {
	case engine.StatusConflict:
		for _, c := range rep.Conflicts {
			if c.Branch == br.Name || c.With == br.Name {
				return conflictText(c)
			}
		}
	case engine.StatusFail:
		for _, f := range rep.Findings {
			for _, n := range f.Set {
				if n == br.Name {
					return f.Summary
				}
			}
		}
	}
	return ""
}

func conflictText(c integrate.Conflict) string {
	other := "another branch"
	switch {
	case c.WithBase:
		other = "base"
	case c.With != "":
		other = c.With
	}
	return fmt.Sprintf("%s conflicts with %s in %s", c.Branch, other, strings.Join(c.Files, ", "))
}

func lastLines(s string, n int) []string {
	s = strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n ")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append([]string{"..."}, lines[len(lines)-n:]...)
	}
	return lines
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func sOf(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dur(ms int64) string {
	return (time.Duration(ms) * time.Millisecond).Round(100 * time.Millisecond).String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
