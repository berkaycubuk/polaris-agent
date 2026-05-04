//go:build linux

package chat

func init() {
	tiocgwinsz = 0x5413
}
