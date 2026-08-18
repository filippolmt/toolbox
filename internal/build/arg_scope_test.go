package build

import (
	"regexp"
	"strings"
	"testing"
)

// finalStageARGs pins the placement rule that makes the Invalidation Floor
// ordering actually bite: in the final stage, every version ARG is declared
// immediately above the single RUN that consumes it.
//
// A build ARG that is in scope lands in the cache key of every RUN below it —
// visible as the `|N` prefix `docker history` prints for each layer. Declared as
// one block at the top of the stage, all 14 version ARGs sat in all 20 tail
// layers, so bumping any single tool gave every tail RUN a new cache key: the
// whole tail rebuilt and ~700 MB moved, measured at 16 substantial layers for a
// one-line OCI_VERSION bump. That silently defeated the rare→frequent ordering
// ADR 0002 built this stage around — the position of a RUN cannot matter while
// every RUN is invalidated by every bump.
//
// The Dockerfile is a static asset no Go code reads, so only a test over the
// embedded bytes can hold this — same technique as TestWorkspaceInstallRefreshGate.
func TestFinalStageVersionARGsScopedToTheirRUN(t *testing.T) {
	body := readAsset(t, "Dockerfile")

	// The final stage is the last FROM: everything below it is the RUN tail the
	// rule is about.
	from := strings.LastIndex(body, "\nFROM node:")
	if from < 0 {
		t.Fatal("Dockerfile: cannot locate the final `FROM node:` stage")
	}
	lines := strings.Split(body[from+1:], "\n")

	argRE := regexp.MustCompile(`^ARG ([A-Z0-9_]+_VERSION)$`)

	// Walk the stage. A version ARG must be followed by more version ARGs and
	// then a RUN, and that RUN must reference every ARG in the group. Anything
	// else means the ARG is in scope for RUNs that do not use it.
	var checked int
	for i := 0; i < len(lines); i++ {
		m := argRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}

		group := []string{m[1]}
		j := i + 1
		for ; j < len(lines); j++ {
			if next := argRE.FindStringSubmatch(lines[j]); next != nil {
				group = append(group, next[1])
				continue
			}
			break
		}
		if j >= len(lines) || !strings.HasPrefix(lines[j], "RUN ") {
			got := "end of stage"
			if j < len(lines) {
				got = lines[j]
			}
			t.Errorf("ARG %s: next instruction is %q, want the RUN that consumes it — an ARG declared away from its RUN is in the cache key of every RUN below it",
				strings.Join(group, ", "), got)
			i = j - 1
			continue
		}

		// Body of that RUN: up to the next instruction at column 0.
		var runBody strings.Builder
		for k := j; k < len(lines); k++ {
			runBody.WriteString(lines[k])
			runBody.WriteString("\n")
			if !strings.HasSuffix(strings.TrimRight(lines[k], " \t"), `\`) {
				break
			}
		}
		for _, name := range group {
			if !strings.Contains(runBody.String(), "${"+name+"}") {
				t.Errorf("ARG %s is declared above a RUN that does not reference ${%s} — move it to the RUN that consumes it", name, name)
			}
			checked++
		}
		i = j - 1
	}

	// Guard against the regex quietly matching nothing (a rename would turn this
	// test into a no-op that reports success).
	if checked < 14 {
		t.Errorf("checked only %d final-stage version ARGs, expected at least 14 — the parse stopped seeing them", checked)
	}
}
