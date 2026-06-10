//go:build !darwin && !linux

package bridge

func NewAgent() (Agent, error) { return nil, ErrUnsupported }
