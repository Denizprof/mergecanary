//go:build windows

package board

import (
	"os"
	"syscall"
	"unsafe"
)

const enableVirtualTerminalProcessing = 0x0004

// IsTerminal reports whether f is a console and turns on ANSI escape processing
// for it. It is false when f is redirected to a file or pipe.
func IsTerminal(f *os.File) bool {
	k32 := syscall.NewLazyDLL("kernel32.dll")
	get, set := k32.NewProc("GetConsoleMode"), k32.NewProc("SetConsoleMode")
	var mode uint32
	if r, _, _ := get.Call(f.Fd(), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return false
	}
	r, _, _ := set.Call(f.Fd(), uintptr(mode|enableVirtualTerminalProcessing))
	return r != 0
}
