package chat

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// errInterrupted is returned by readLine when the user presses Ctrl-C while
// editing a line.
var errInterrupted = errors.New("interrupted")

// rawCleanup, when non-nil, restores the terminal to cooked mode. The signal
// handler in Run installs this so a SIGINT delivered while raw mode is active
// (e.g. from another terminal via `kill -INT`) doesn't leave the tty wedged.
var rawCleanup func()

// readLine reads one input line from stdin with line editing: left/right
// move the cursor, up/down walk message history, plus the usual emacs-style
// shortcuts. history is the in-memory list of prior submissions (oldest
// first); readLine never mutates it.
//
// Returns io.EOF on Ctrl-D at an empty line or stdin close, errInterrupted
// on Ctrl-C. Falls back to cooked-mode bufio reads when stdin is not a tty
// or raw mode can't be enabled.
func readLine(promptStyled string, history []string) (string, error) {
	if !tty {
		return readLineCooked(promptStyled)
	}
	rs, err := enableRaw(os.Stdin.Fd())
	if err != nil {
		return readLineCooked(promptStyled)
	}
	rawCleanup = rs.restore
	defer func() {
		rawCleanup = nil
		rs.restore()
	}()

	var (
		buf        []rune
		cursor     int
		histIdx    = len(history)
		savedDraft []rune
		pending    []byte
	)

	redraw := func() {
		fmt.Print("\r\033[K")
		fmt.Print(promptStyled)
		fmt.Print(string(buf))
		if back := len(buf) - cursor; back > 0 {
			fmt.Printf("\033[%dD", back)
		}
	}

	insertRune := func(r rune) {
		buf = append(buf, 0)
		copy(buf[cursor+1:], buf[cursor:])
		buf[cursor] = r
		cursor++
		redraw()
	}

	redraw()

	var b [1]byte
	for {
		n, err := os.Stdin.Read(b[:])
		if err != nil || n == 0 {
			fmt.Print("\r\n")
			return "", io.EOF
		}
		c := b[0]

		switch {
		case c == '\r' || c == '\n':
			fmt.Print("\r\n")
			return string(buf), nil
		case c == 0x03: // Ctrl-C
			fmt.Print("^C\r\n")
			return "", errInterrupted
		case c == 0x04: // Ctrl-D
			if len(buf) == 0 {
				fmt.Print("\r\n")
				return "", io.EOF
			}
			if cursor < len(buf) {
				buf = append(buf[:cursor], buf[cursor+1:]...)
				redraw()
			}
		case c == 0x7f || c == 0x08: // Backspace / DEL
			if cursor > 0 {
				buf = append(buf[:cursor-1], buf[cursor:]...)
				cursor--
				redraw()
			}
		case c == 0x01: // Ctrl-A
			cursor = 0
			redraw()
		case c == 0x05: // Ctrl-E
			cursor = len(buf)
			redraw()
		case c == 0x02: // Ctrl-B
			if cursor > 0 {
				cursor--
				redraw()
			}
		case c == 0x06: // Ctrl-F
			if cursor < len(buf) {
				cursor++
				redraw()
			}
		case c == 0x0B: // Ctrl-K: kill to end
			buf = buf[:cursor]
			redraw()
		case c == 0x15: // Ctrl-U: kill to start
			buf = buf[cursor:]
			cursor = 0
			redraw()
		case c == 0x17: // Ctrl-W: delete previous word
			i := cursor
			for i > 0 && buf[i-1] == ' ' {
				i--
			}
			for i > 0 && buf[i-1] != ' ' {
				i--
			}
			buf = append(buf[:i], buf[cursor:]...)
			cursor = i
			redraw()
		case c == 0x0C: // Ctrl-L: clear screen
			fmt.Print("\033[2J\033[H")
			redraw()
		case c == 0x1B: // ESC — start of an arrow / function-key sequence
			var s1 [1]byte
			n1, _ := os.Stdin.Read(s1[:])
			if n1 == 0 || (s1[0] != '[' && s1[0] != 'O') {
				continue
			}
			var s2 [1]byte
			n2, _ := os.Stdin.Read(s2[:])
			if n2 == 0 {
				continue
			}
			switch s2[0] {
			case 'A': // Up
				if histIdx > 0 {
					if histIdx == len(history) {
						savedDraft = append(savedDraft[:0], buf...)
					}
					histIdx--
					buf = []rune(history[histIdx])
					cursor = len(buf)
					redraw()
				}
			case 'B': // Down
				if histIdx < len(history) {
					histIdx++
					if histIdx == len(history) {
						buf = append(buf[:0], savedDraft...)
					} else {
						buf = []rune(history[histIdx])
					}
					cursor = len(buf)
					redraw()
				}
			case 'C': // Right
				if cursor < len(buf) {
					cursor++
					redraw()
				}
			case 'D': // Left
				if cursor > 0 {
					cursor--
					redraw()
				}
			case 'H': // Home
				cursor = 0
				redraw()
			case 'F': // End
				cursor = len(buf)
				redraw()
			case '3': // Delete (followed by '~')
				var tail [1]byte
				_, _ = os.Stdin.Read(tail[:])
				if cursor < len(buf) {
					buf = append(buf[:cursor], buf[cursor+1:]...)
					redraw()
				}
			case '1', '2', '4', '5', '6', '7', '8':
				// Swallow common '~'-terminated sequences (PgUp/PgDn/etc.)
				var tail [1]byte
				_, _ = os.Stdin.Read(tail[:])
			}
		default:
			if c < 0x20 {
				continue
			}
			if c < 0x80 && len(pending) == 0 {
				insertRune(rune(c))
				continue
			}
			pending = append(pending, c)
			if utf8.FullRune(pending) {
				r, _ := utf8.DecodeRune(pending)
				pending = pending[:0]
				if r != utf8.RuneError {
					insertRune(r)
				}
			} else if len(pending) >= 4 {
				pending = pending[:0]
			}
		}
	}
}

func readLineCooked(prompt string) (string, error) {
	fmt.Print(prompt)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		if err == io.EOF && line == "" {
			return "", io.EOF
		}
		if err != io.EOF {
			return "", err
		}
	}
	return trimNL(line), nil
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
