//go:build linux

package chat

import (
	"syscall"
	"unsafe"
)

type termState syscall.Termios

const (
	ignbrk = syscall.IGNBRK
	brkint = syscall.BRKINT
	parmrk = syscall.PARMRK
	istrip = syscall.ISTRIP
	inlcr  = syscall.INLCR
	igncr  = syscall.IGNCR
	icrnl  = syscall.ICRNL
	ixon   = syscall.IXON
	opost  = syscall.OPOST
	echo   = syscall.ECHO
	echonl = syscall.ECHONL
	icanon = syscall.ICANON
	isig   = syscall.ISIG
	iexten = syscall.IEXTEN
	csize  = syscall.CSIZE
	parenb = syscall.PARENB
	cs8    = syscall.CS8
	vmmin  = syscall.VMIN
	vmtime = syscall.VTIME
)

func tcgetattr(fd int, t *termState) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}

func tcsetattr(fd int, t *termState) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(t)))
	if errno != 0 {
		return errno
	}
	return nil
}
