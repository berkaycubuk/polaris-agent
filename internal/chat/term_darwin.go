//go:build darwin

package chat

func init() {
	tiocgwinsz = 0x40087468
}
