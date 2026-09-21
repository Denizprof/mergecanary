// Package check runs the user's check command (tests/build) in a directory.
package check

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// MaxOutput is how many trailing bytes of combined stdout/stderr are kept.
const MaxOutput = 64 << 10

// Result is the outcome of one check run.
type Result struct {
	OK       bool          `json:"ok"`
	ExitCode int           `json:"exit_code"`
	TimedOut bool          `json:"timed_out,omitempty"`
	Duration time.Duration `json:"duration_ns"`
	Output   string        `json:"-"` // tail of combined stdout+stderr
}

// Run runs command through the platform shell (sh -c, or cmd /C on Windows) in
// dir. A timeout of 0 means none. A failing or timed-out command is reported in
// the Result, not as an error; err is set only when the command could not be
// run or ctx was cancelled by the caller. On timeout the whole process tree is killed.
func Run(ctx context.Context, dir, command string, timeout time.Duration) (Result, error) {
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := shellCommand(runCtx, command)
	cmd.Dir = dir
	out := &tail{max: MaxOutput}
	cmd.Stdout, cmd.Stderr = out, out // same writer: exec copies both through one goroutine
	cmd.WaitDelay = 5 * time.Second   // don't hang on grandchildren holding the pipes
	start := time.Now()
	err := cmd.Run()
	res := Result{Duration: time.Since(start), Output: out.String()}

	if ctx.Err() != nil {
		return res, ctx.Err() // caller cancelled (e.g. Ctrl-C)
	}
	if runCtx.Err() != nil {
		res.TimedOut, res.ExitCode = true, -1
		return res, nil
	}
	if err == nil || (errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success()) {
		res.OK = true
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err // could not start the shell
}

// tail keeps the last max bytes written to it.
type tail struct {
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.buf) }
