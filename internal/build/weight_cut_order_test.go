package build

import (
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// dockerfileRUNs splits the embedded Dockerfile into its RUN instructions and
// returns each one's body as lines, comment lines removed.
//
// Per RUN rather than per stage, because both rules below are about what a
// single layer does to itself: a later RUN writes into its own layer, and what
// it puts there is that layer's business. Comments go for the same reason
// dockerfileStages drops them — several of them quote the very commands these
// tests look for, and an in-body comment carries no trailing backslash (the
// Dockerfile parser strips comments before joining continuations), so it must
// not be read as the end of the instruction either.
func dockerfileRUNs(t *testing.T) [][]string {
	t.Helper()
	lines := strings.Split(readAsset(t, "Dockerfile"), "\n")

	var runs [][]string
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], "RUN ") {
			continue
		}
		var body []string
		for ; i < len(lines); i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
				continue
			}
			body = append(body, lines[i])
			if !strings.HasSuffix(strings.TrimRight(lines[i], " \t"), `\`) {
				break
			}
		}
		runs = append(runs, body)
	}
	if len(runs) == 0 {
		t.Fatal("Dockerfile: no RUN instructions found — the parse is broken, not the file")
	}
	return runs
}

// commandSepRE splits a shell line into the commands it runs. Pipes count: the
// right-hand side of a pipe is a command like any other.
var commandSepRE = regexp.MustCompile(`;|&&|\|\||\|`)

// shellKeywords are the leading words that introduce a command rather than
// being one. `do`/`then`/`else` are how a loop body starts on a Dockerfile
// continuation line, which is exactly where an invocation hides.
var shellKeywords = map[string]bool{
	"do": true, "then": true, "else": true, "!": true, "time": true,
}

// commandsIn returns the program each command on a shell line invokes, by
// basename.
//
// Only the first word of each command counts, and that is the whole point: the
// oci stage has to link the binary it just verified, so a line reading
// `ln -s /opt/oci-cli/bin/oci …` *mentions* `oci` without running it. A rule
// that read mentions would be unsatisfiable by any stage that ships what it
// tested.
func commandsIn(line string) []string {
	line = strings.TrimSuffix(strings.TrimRight(line, " \t"), `\`)

	var out []string
	for _, cmd := range commandSepRE.Split(line, -1) {
		for field := range strings.FieldsSeq(cmd) {
			// `FOO=bar cmd` and the keywords above sit in front of the program
			// without being it.
			if shellKeywords[field] || strings.Contains(field, "=") {
				continue
			}
			out = append(out, path.Base(strings.Trim(field, `"'`)))
			break
		}
	}
	return out
}

// pycacheOwners maps the tree a `__pycache__` purge names to the commands whose
// every run writes bytecode back into it.
//
// Explicit, and deliberately not derived from the purge path, because the link
// between the two is not textual: `az` runs from PATH and never spells
// `/opt/az` on the command line, so no pattern over the purge root could find
// its invocations. A new interpreter tree in the image reddens this test until
// someone says which commands reach it — the same shape as stageWideARGs, and
// for the same reason.
var pycacheOwners = map[string][]string{
	"/out/opt/google-cloud-sdk": {"gcloud", "gsutil", "bq", "gke-gcloud-auth-plugin"},
	"/opt/oci-cli":              {"oci"},
	"/opt/az":                   {"az"},
	"/usr/local/lib/python*":    {"python3", "pip", "graphify"},
}

var pycachePurgeRE = regexp.MustCompile(`find (\S+) -type d -name __pycache__`)

// TestPycachePurgeFollowsTheLastCLIRun pins the ordering half of "delete only
// from the layer that created the bytes": within a RUN, the `__pycache__` purge
// comes below every invocation of the CLI whose tree it purges.
//
// These layers run their CLI as root to prove the install survived whatever was
// cut from it, and every such run writes bytecode straight back into the tree.
// A purge placed above those checks therefore ships exactly what it removed, in
// the same layer, with nothing to see in the Dockerfile diff — it regressed that
// way once, and put back more than the removal had taken out. The cleanup and
// the verification are both obviously correct in isolation; only their order is
// wrong, which is why this needs a test rather than a comment.
//
// → .claude/rules/image-build.md, CONTEXT.md "Creating Layer", ADR 0016
func TestPycachePurgeFollowsTheLastCLIRun(t *testing.T) {
	var seen int
	for _, body := range dockerfileRUNs(t) {
		for i, line := range body {
			m := pycachePurgeRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			root := m[1]
			owners, known := pycacheOwners[root]
			if !known {
				t.Errorf("__pycache__ purge over %q has no pycacheOwners entry — name the commands that write bytecode into that tree, or this test holds nothing for it", root)
				continue
			}
			seen++

			for _, later := range body[i+1:] {
				for _, cmd := range commandsIn(later) {
					if !slices.Contains(owners, cmd) {
						continue
					}
					t.Errorf("RUN purges __pycache__ under %s and then runs %s (%q) — that run writes bytecode back into the tree and the layer ships what the purge removed; move the purge below the last %s invocation",
						root, cmd, strings.TrimSpace(later), cmd)
				}
			}
		}
	}

	// Anti-vacuity only, in the repo's usual shape: a floor tied to today's
	// number of purges would fail the day one is legitimately removed.
	if seen == 0 {
		t.Error("found no __pycache__ purge inside any RUN — the parse is broken, not the Dockerfile")
	}
}

var (
	// The vendored runtime is replaced by a link to the image's own, so the
	// link's target is the image's node and its source is the vendored copy.
	vendoredNodeSymlinkRE = regexp.MustCompile(`\bln -sf? /usr/local/bin/node\b`)
	// `test "$(<something> --version …)" = "$(node --version …)"`, whatever the
	// two sides pipe through to reduce a version to its major.
	nodeMajorAssertRE = regexp.MustCompile(`test "\$\(.*--version.*\)" = "\$\(node --version.*\)"`)
)

// TestVendoredRuntimeAssertsMajorBeforeSymlink holds the load-bearing half of
// the CodeGraph bundle's Node deduplication: the majors are asserted equal
// before the vendored runtime is replaced by a symlink to the image's own.
//
// The symlink is what saves the bytes, and it is also the part that cannot fail
// loudly. Drop the assert and an upstream bump of the platform package past the
// base image's Node stops being a red build and becomes a silent downgrade: the
// tool keeps starting, on an older runtime than it was built against, and the
// only evidence is whatever it does wrong later. The two are one edit in the
// Dockerfile and would be one deletion too, so the ordering is pinned here
// rather than left to the comment above the stage.
//
// → .claude/rules/image-build.md, ADR 0016 Decision 1
func TestVendoredRuntimeAssertsMajorBeforeSymlink(t *testing.T) {
	var seen int
	for _, body := range dockerfileRUNs(t) {
		for i, line := range body {
			if !vendoredNodeSymlinkRE.MatchString(line) {
				continue
			}
			seen++

			if !slices.ContainsFunc(body[:i], nodeMajorAssertRE.MatchString) {
				t.Errorf("%q replaces a vendored runtime with a link to the image's node, with no major-version assert above it in the same RUN — an upstream bump past this base image's Node then degrades silently onto an older runtime instead of failing the build", strings.TrimSpace(line))
			}
		}
	}

	// Not the usual anti-vacuity: zero matches means the deduplication itself is
	// gone. If that was deliberate, this test goes with it — and its rule bullet.
	if seen == 0 {
		t.Error("found no symlink onto the image's own node — either the parse is broken, or the vendored-runtime deduplication was removed without its guardrail")
	}
}
