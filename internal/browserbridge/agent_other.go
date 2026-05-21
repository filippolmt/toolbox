//go:build !darwin && !linux

package browserbridge

func NewAgent() (Agent, error) { return nil, ErrUnsupported }
