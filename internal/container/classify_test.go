package container

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestClassifyStartupFailure(t *testing.T) {
	state := func(errStr string, code int) *container.State {
		return &container.State{Error: errStr, ExitCode: code}
	}

	cases := []struct {
		name         string
		state        *container.State
		signals      []string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:         "ENOSPC string in stream is definitive",
			signals:      []string{"runc: ... : no space left on device"},
			wantContains: []string{"out of disk space", "docker system df"},
			wantAbsent:   []string{"common cause"},
		},
		{
			name:         "runc broken pipe in raw error is definitive",
			state:        state("", 1),
			signals:      []string{"write init-p: broken pipe"},
			wantContains: []string{"out of disk space", "(exit 1)"},
		},
		{
			name:         "signature from State.Error is definitive",
			state:        state("no space left on device", 137),
			wantContains: []string{"out of disk space", "(exit 137)"},
		},
		{
			name:         "ENOSPC token matched case-insensitively",
			signals:      []string{"failed: ENOSPC writing layer"},
			wantContains: []string{"out of disk space"},
		},
		{
			name:         "no signature falls back to hedged message",
			state:        state("", 2),
			signals:      []string{"cannot exec in a stopped container"},
			wantContains: []string{"exited at startup", "(exit 2)", "common cause", "disk space", "docker system df"},
			wantAbsent:   []string{"Docker is out of disk space"},
		},
		{
			name:         "nil state omits the exit detail",
			signals:      []string{"cannot exec in a stopped container"},
			wantContains: []string{"exited at startup", "disk space"},
			wantAbsent:   []string{"(exit"},
		},
		{
			name:         "no signals at all still names disk as likely cause",
			wantContains: []string{"exited at startup", "disk space"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyStartupFailure(tc.state, tc.signals...)
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("message %q missing %q", got, want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("message %q should not contain %q", got, absent)
				}
			}
		})
	}
}
