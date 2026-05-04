//go:build !darwin && !linux

package chat

import "errors"

type rawState struct{}

func enableRaw(_ uintptr) (*rawState, error) {
	return nil, errors.New("raw mode unsupported on this platform")
}

func (s *rawState) restore() {}
