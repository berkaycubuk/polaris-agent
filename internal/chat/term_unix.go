//go:build darwin || linux

package chat

import (
	"os"
	"syscall"
	"unsafe"
)

// TIOCGWINSZ values differ between platforms — set per-OS in term_*.go.
var tiocgwinsz uintptr

type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// termCols queries the terminal width via TIOCGWINSZ. Returns (cols, ok).
func termCols() (int, bool) {
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		os.Stdout.Fd(),
		tiocgwinsz,
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 || ws.Col == 0 {
		return 0, false
	}
	return int(ws.Col), true
}
