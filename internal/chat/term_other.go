//go:build !darwin && !linux

package chat

func termCols() (int, bool) { return 0, false }
