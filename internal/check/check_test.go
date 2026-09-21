package check

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// helperCmd returns a shell command that re-runs this test binary as a helper.
func helperCmd(mode string) string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf(`"%s" -test.run=^TestHelperProcess$ -- %s`, exe, mode)
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("MC_CHECK_HELPER") != "1" {
		t.Skip("helper process only")
	}
	switch os.Args[len(os.Args)-1] {
	case "sleep":
		time.Sleep(time.Minute)
	case "noisy":
		fmt.Println("stdout line")
		fmt.Fprintln(os.Stderr, "stderr line")
		os.Exit(3)
	}
}

func TestRunPass(t *testing.T) {
	res, err := Run(context.Background(), t.TempDir(), "exit 0", 0)
	if err != nil || !res.OK || res.ExitCode != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestRunFailExitCodeAndOutput(t *testing.T) {
	t.Setenv("MC_CHECK_HELPER", "1")
	res, err := Run(context.Background(), t.TempDir(), helperCmd("noisy"), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.ExitCode != 3 || res.TimedOut {
		t.Fatalf("res=%+v", res)
	}
	if !strings.Contains(res.Output, "stdout line") || !strings.Contains(res.Output, "stderr line") {
		t.Fatalf("output missing a stream: %q", res.Output)
	}
}

func TestRunRunsInDir(t *testing.T) {
	dir := t.TempDir()
	cmd := "cd"
	if runtime.GOOS != "windows" {
		cmd = "pwd"
	}
	res, err := Run(context.Background(), dir, cmd, 0)
	if err != nil || !res.OK {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// compare on the last path element; temp dirs may be symlinked/short-named
	base := dir[strings.LastIndexAny(dir, `/\`)+1:]
	if !strings.Contains(res.Output, base) {
		t.Fatalf("output %q does not contain %q", res.Output, base)
	}
}

func TestRunTimeoutKills(t *testing.T) {
	t.Setenv("MC_CHECK_HELPER", "1")
	start := time.Now()
	res, err := Run(context.Background(), t.TempDir(), helperCmd("sleep"), 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !res.TimedOut {
		t.Fatalf("res=%+v", res)
	}
	if el := time.Since(start); el > 4*time.Second {
		t.Fatalf("took %v; process was not killed promptly", el)
	}
}

func TestRunTimeoutKillsGrandchildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix process-group behaviour")
	}
	start := time.Now()
	// two background-ish children under sh; WaitDelay (5s) would mask a missed kill
	res, err := Run(context.Background(), t.TempDir(), "sleep 30 & sleep 30; wait", 1*time.Second)
	if err != nil || !res.TimedOut {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Fatalf("took %v; grandchildren were not killed", el)
	}
}

func TestRunCallerCancelIsError(t *testing.T) {
	t.Setenv("MC_CHECK_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	res, err := Run(ctx, t.TempDir(), helperCmd("sleep"), time.Minute)
	if err == nil || res.TimedOut {
		t.Fatalf("want ctx error, got res=%+v err=%v", res, err)
	}
}

func TestTailKeepsEnd(t *testing.T) {
	tl := &tail{max: 10}
	tl.Write([]byte("0123456789"))
	tl.Write([]byte("abc"))
	if got := tl.String(); got != "3456789abc" {
		t.Fatalf("got %q", got)
	}
}
