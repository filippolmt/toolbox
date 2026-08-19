package build

import (
	"regexp"
	"strings"
	"testing"
)

// TestFinalStageARGsScopedToTheirRUN pins the placement rule that makes the
// Invalidation Floor ordering actually bite: in the final stage, every ARG other
// than the stage-wide ones is declared immediately above the single RUN that
// consumes it.
//
// A build ARG that is in scope lands in the cache key of every RUN below it —
// visible as the `|N` prefix `docker history` prints for each layer. Declared as
// one block at the top of the stage, all 14 version ARGs sat in all 21 tail
// layers, so bumping any single tool gave every tail RUN a new cache key and the
// whole tail rebuilt. That silently defeated the rare→frequent ordering ADR 0002
// built this stage around — the position of a RUN cannot matter while every RUN
// is invalidated by every bump. Figures for the cost are in ADR 0002's
// follow-up, which also records what they can and cannot be attributed to.
//
// The Dockerfile is a static asset no Go code reads, so only a test over the
// embedded bytes can hold this — same technique as TestWorkspaceInstallRefreshGate.
// stageWideARGs are the final-stage ARGs that legitimately apply to the whole
// stage rather than to one RUN: neither carries a version, so neither moves on a
// Renovate bump, and both are read by many RUNs.
var stageWideARGs = map[string]bool{
	"TARGETARCH":      true,
	"DEBIAN_FRONTEND": true,
}

func TestFinalStageARGsScopedToTheirRUN(t *testing.T) {
	body := readAsset(t, "Dockerfile")

	// The final stage is the last FROM: everything below it is the RUN tail the
	// rule is about.
	from := strings.LastIndex(body, "\nFROM node:")
	if from < 0 {
		t.Fatal("Dockerfile: cannot locate the final `FROM node:` stage")
	}
	stage := body[from+1:]
	lines := strings.Split(stage, "\n")

	// EVERY ARG in the stage, not just the `_VERSION` ones: the pathology is
	// about scope, and an `ARG OMZ_COMMIT` block would reintroduce it just as
	// well. The default is matched too (`ARG FOO=1.2`) — a defaulted ARG is the
	// regression shape as much as a bare one.
	argRE := regexp.MustCompile(`^ARG ([A-Za-z_][A-Za-z0-9_]*)(=.*)?$`)

	var seen, checked int
	for i := 0; i < len(lines); i++ {
		m := argRE.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}

		// Consecutive ARGs form one group: two of them can share a RUN.
		group := []string{m[1]}
		j := i + 1
		for ; j < len(lines); j++ {
			if next := argRE.FindStringSubmatch(lines[j]); next != nil {
				group = append(group, next[1])
				continue
			}
			break
		}

		// The deliberately stage-wide ones carry no version and are consumed by
		// many RUNs, so the rule cannot apply to them. Adding a name here is a
		// decision about the Dockerfile, which is why it is an explicit list and
		// not a pattern: a new ARG reddens this test until someone says which of
		// the two it is.
		kept := group[:0:0]
		for _, name := range group {
			if !stageWideARGs[name] {
				kept = append(kept, name)
				seen++
			}
		}
		if len(kept) == 0 {
			i = j - 1
			continue
		}
		group = kept
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

		// Body of that RUN: continuation lines until one does not end in `\`.
		// An in-body comment does NOT end the instruction — the Dockerfile parser
		// strips comments before joining continuations, so a mid-body comment
		// carries no trailing backslash (the azure RUN has one). Treating it as
		// the end truncates the body and the reference check below reads half a
		// RUN.
		var runBody strings.Builder
		for k := j; k < len(lines); k++ {
			if strings.HasPrefix(strings.TrimSpace(lines[k]), "#") {
				// Skipped, not appended: a comment mentioning ${FOO_VERSION}
				// would otherwise satisfy the reference check below on its own.
				continue
			}
			runBody.WriteString(lines[k])
			runBody.WriteString("\n")
			if !strings.HasSuffix(strings.TrimRight(lines[k], " \t"), `\`) {
				break
			}
		}

		for _, name := range group {
			ref := "${" + name + "}"
			inRUN := strings.Count(runBody.String(), ref)
			if inRUN == 0 {
				t.Errorf("ARG %s is declared above a RUN that does not reference %s — move it to the RUN that consumes it", name, ref)
				continue
			}
			// "the single RUN that consumes it" is the claim the name makes, so
			// check it: a second consumer further down the stage would put this
			// ARG back in the cache key of RUNs between the two.
			if total := strings.Count(stage, ref); total != inRUN {
				t.Errorf("ARG %s is referenced %d times in the stage but only %d inside its own RUN — a second consumer re-widens its scope; split the version or move the ARG above the first consumer",
					name, total, inRUN)
			}
			checked++
		}
		i = j - 1
	}

	// Anti-vacuity only. A floor tied to today's tool count would fail the day a
	// tool is legitimately removed, and would be a further hardcoded copy of a
	// number the Dockerfile already owns — the repo's rule is to derive counts,
	// not restate them (.claude/rules/image-build.md).
	// `seen`, not `checked`: on a Dockerfile that violates the rule the ARGs are
	// found but never verified, and blaming the parse there would misdiagnose a
	// real finding.
	if seen == 0 {
		t.Error("found no final-stage ARG declarations at all — the parse is broken, not the Dockerfile")
	}
}
