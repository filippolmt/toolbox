package container

import (
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/container"
)

// diskSignatures are substrings that positively identify disk exhaustion in a
// container's death throes: the kernel/daemon ENOSPC strings and the opaque
// runc error emitted when it cannot write the init pipe because the backing
// filesystem is full. Matched case-insensitively.
var diskSignatures = []string{
	"no space left on device",
	"enospc",
	"write init-p: broken pipe",
}

// classifyStartupFailure builds the user-facing message for a container that
// exited before (or during) the shell attach. It scans the available failure
// signals — the tail of the attach stream, the raw daemon error, and the
// recorded container State.Error — for a positive disk-exhaustion signature: on
// a hit it names the cause outright; otherwise it reports the exit and still
// flags disk exhaustion as the most common cause plus the command to confirm
// it. Every branch mentions disk + `docker system df` so the user always gets a
// lead, but only a matched signature makes the claim definitive.
//
// state may be nil (AutoRemove already reaped the container); extraSignals may
// be empty (the exec failed before any bytes streamed).
func classifyStartupFailure(state *container.State, extraSignals ...string) string {
	signals := make([]string, 0, len(extraSignals)+1)
	signals = append(signals, extraSignals...)
	detail := ""
	if state != nil {
		if state.Error != "" {
			signals = append(signals, state.Error)
		}
		detail = fmt.Sprintf(" (exit %d)", state.ExitCode)
	}

	if matchesDiskSignature(signals) {
		return "Docker is out of disk space" + detail +
			" — free space with `docker system prune` (usage: `docker system df`)"
	}
	return "container exited at startup" + detail + " before the shell could attach — " +
		"a common cause is Docker running out of disk space; check with `docker system df`"
}

// matchesDiskSignature reports whether any signal contains a known disk
// exhaustion substring.
func matchesDiskSignature(signals []string) bool {
	for _, s := range signals {
		low := strings.ToLower(s)
		for _, sig := range diskSignatures {
			if strings.Contains(low, sig) {
				return true
			}
		}
	}
	return false
}
