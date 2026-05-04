//go:build linux

package chat

func init() {
	tiocgwinsz = 0x5413
}

const (
	tiocGetTermios = 0x5401 // TCGETS
	tiocSetTermios = 0x5402 // TCSETS
)
