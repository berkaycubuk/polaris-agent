//go:build darwin || linux

package chat

import (
	"fmt"
	"io"
	"os"
)

const (
	ctrlC     rune = 3
	enter     rune = 13
	backspace rune = 8
	del       rune = 127
	tab       rune = 9
	esc       rune = 27
)

func makeRaw() (*termState, error) {
	fd := int(os.Stdin.Fd())
	var old termState
	if err := tcgetattr(fd, &old); err != nil {
		return nil, fmt.Errorf("terminal raw mode: %w", err)
	}

	raw := old
	raw.Iflag &^= ignbrk | brkint | parmrk | istrip | inlcr | igncr | icrnl | ixon
	raw.Oflag &^= opost
	raw.Lflag &^= echo | echonl | icanon | isig | iexten
	raw.Cflag &^= csize | parenb
	raw.Cflag |= cs8
	raw.Cc[vmmin] = 1
	raw.Cc[vmtime] = 0

	if err := tcsetattr(fd, &raw); err != nil {
		return nil, err
	}
	return &old, nil
}

func restore(s *termState) {
	fd := int(os.Stdin.Fd())
	tcsetattr(fd, s)
}

func readRune() (rune, error) {
	var buf [1]byte
	n, err := os.Stdin.Read(buf[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return rune(buf[0]), nil
}

func readEscapeSeq() string {
	var b [1]byte

	n, err := os.Stdin.Read(b[:])
	if err != nil || n == 0 || b[0] != '[' {
		return ""
	}

	n, err = os.Stdin.Read(b[:])
	if err != nil || n == 0 {
		return ""
	}

	switch b[0] {
	case 'A':
		return "up"
	case 'B':
		return "down"
	case 'C':
		return "right"
	case 'D':
		return "left"
	default:
		for {
			n, err = os.Stdin.Read(b[:])
			if err != nil || n == 0 {
				return ""
			}
			if (b[0] >= 'A' && b[0] <= 'Z') || (b[0] >= 'a' && b[0] <= 'z') {
				return ""
			}
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
