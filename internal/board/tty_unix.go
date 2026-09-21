//go:build !windows

package board

import "os"

// IsTerminal reports whether f is an interactive terminal that understands ANSI
// (and enables ANSI processing where needed). /dev/null is not a terminal.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, null) {
		return false
	}
	return true
}
