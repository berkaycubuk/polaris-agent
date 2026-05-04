//go:build darwin

package chat

func init() {
	tiocgwinsz = 0x40087468
}

const (
	tiocGetTermios = 0x40487413 // TIOCGETA
	tiocSetTermios = 0x80487414 // TIOCSETA
)
