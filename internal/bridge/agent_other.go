//go:build !darwin && !linux

package bridge

import "github.com/filippolmt/toolbox/internal/fsx"

func NewAgent(fsx.Host) (Agent, error) { return nil, ErrUnsupported }
