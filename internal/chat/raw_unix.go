//go:build darwin || linux

package chat

import (
	"syscall"
	"unsafe"
)

type rawState struct {
	fd      uintptr
	termios syscall.Termios
}

func tcGet(fd uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL, fd, tiocGetTermios,
		uintptr(unsafe.Pointer(t)), 0, 0, 0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func tcSet(fd uintptr, t *syscall.Termios) error {
	_, _, errno := syscall.Syscall6(
		syscall.SYS_IOCTL, fd, tiocSetTermios,
		uintptr(unsafe.Pointer(t)), 0, 0, 0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}

// enableRaw puts the terminal into a minimal raw mode: canonical line
// buffering, local echo, CR→NL input mapping, and signal generation are
// disabled so we can render input ourselves and intercept Ctrl-C/D as bytes.
// OPOST is left on so plain "\n" still translates to "\r\n" on output.
func enableRaw(fd uintptr) (*rawState, error) {
	var orig syscall.Termios
	if err := tcGet(fd, &orig); err != nil {
		return nil, err
	}
	raw := orig
	raw.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG
	raw.Iflag &^= syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	if err := tcSet(fd, &raw); err != nil {
		return nil, err
	}
	return &rawState{fd: fd, termios: orig}, nil
}

func (s *rawState) restore() {
	if s == nil {
		return
	}
	_ = tcSet(s.fd, &s.termios)
}
