package build

import (
	"os"
	"strings"
	"testing"
	"unicode"
)

// smokeTestFile is read from disk: the smoke test runs from a checkout and is
// not part of the embedded build context.
const smokeTestFile = "assets/smoke-test.sh"

// smokeBodyQuoteError reports why one line of a single-quoted `bash -c '…'`
// body would break that body, or "" when it is safe. Each line must start and
// end inside the single quotes; a quote may close them only for a segment with
// no unquoted whitespace (`'%A'`, `'"'"'`), since whitespace there hands the
// words to the host shell as separate docker arguments.
func smokeBodyQuoteError(line string) string {
	single, double := true, false
	for _, r := range line {
		switch {
		case single:
			single = r != '\''
		case double:
			double = r != '"'
		case r == '"':
			double = true
		case r == '\'':
			single = true
		case unicode.IsSpace(r):
			return "a single quote closes the body and the host shell splits what follows"
		}
	}
	if !single || double {
		return "the line ends outside the body's single quotes"
	}
	return ""
}

// TestSmokeTestBodiesKeepTheirSingleQuotes catches, at go test time and on the
// right line, the break the smoke test's own Results-line guard only catches
// after a full image build: an apostrophe — in a comment too — inside a
// `docker run … -c '` body. A body closes on a line that is a lone quote or a
// quote followed by a space (`' | tee …`).
func TestSmokeTestBodiesKeepTheirSingleQuotes(t *testing.T) {
	b, err := os.ReadFile(smokeTestFile)
	if err != nil {
		t.Fatalf("read smoke-test.sh: %v", err)
	}
	bodies, inBody := 0, false
	for i, line := range strings.Split(string(b), "\n") {
		switch {
		case !inBody && strings.HasSuffix(strings.TrimRight(line, " "), " -c '"):
			inBody = true
			bodies++
		case inBody && (line == "'" || strings.HasPrefix(line, "' ")):
			inBody = false
		case inBody:
			if why := smokeBodyQuoteError(line); why != "" {
				t.Errorf("smoke-test.sh:%d: %s — reword without single quotes or apostrophes: %s", i+1, why, line)
			}
		}
	}
	if bodies == 0 {
		t.Fatal("no `-c '` body found in smoke-test.sh: the opener pattern no longer matches")
	}
}

func TestSmokeBodyQuoteError(t *testing.T) {
	for line, broken := range map[string]bool{
		`check_optional "x" x x --version`:                     false,
		`perms=$(stat -c '%A' "$p") || return 1`:               false,
		`sh -c '"'"'echo "" | goimports'"'"'`:                  false,
		`# the background git status of codegraph never holds`: false,
		`# so codegraph's background git status never holds`:   true,
		`# codegraph's status and the developer's git`:         true,
		`# a 'single-word' quote leaves nothing to split`:      false,
	} {
		if got := smokeBodyQuoteError(line) != ""; got != broken {
			t.Errorf("smokeBodyQuoteError(%q) broken = %v, want %v", line, got, broken)
		}
	}
}
