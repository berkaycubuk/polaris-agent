//go:build !darwin && !linux

package chat

import "fmt"

type termState struct{}

const (
	ctrlC     rune = 3
	enter     rune = 13
	backspace rune = 8
	del       rune = 127
	tab       rune = 9
	esc       rune = 27
)

func makeRaw() (*termState, error) {
	return nil, fmt.Errorf("raw terminal not supported on this platform")
}

func restore(*termState) {}
