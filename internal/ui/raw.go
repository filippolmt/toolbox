package ui

import (
	"fmt"
	"sync"

	"golang.org/x/term"
)

// RawTerminal puts fd in raw mode — every keypress delivered as it is struck,
// nothing held back for a Return — and returns the func that restores it. Two
// callers want the same thing for opposite spans: an attached session, which
// forwards every key to the container for as long as it runs, and a prompt,
// which needs one keypress to be a whole answer.
//
// Off a terminal (a pipe, a CI runner) isTTY is false and restore is a no-op,
// so a caller can defer it unconditionally. Restore runs at most once however
// many times it is called, which is what lets a caller restore early — before
// re-raising a signal, say — and still defer it.
func RawTerminal(fd int) (restore func(), isTTY bool, err error) {
	noop := func() {
		// The terminal was never put in raw mode, so there is nothing to
		// restore — callers defer this unconditionally.
	}
	if !term.IsTerminal(fd) {
		return noop, false, nil
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return noop, false, fmt.Errorf("set terminal to raw mode: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			if rerr := term.Restore(fd, oldState); rerr != nil {
				Warning("terminal restore failed: " + rerr.Error())
			}
		})
	}, true, nil
}
