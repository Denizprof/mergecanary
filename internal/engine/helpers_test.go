package engine_test

import (
	"fmt"
	"runtime"
)

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

// sleepCommand is a check command that outlives any test timeout.
func sleepCommand() string {
	if runtime.GOOS == "windows" {
		return "ping -n 60 127.0.0.1 >nul"
	}
	return "sleep 60"
}
