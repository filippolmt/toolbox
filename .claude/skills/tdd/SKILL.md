---
name: tdd
description: Implement a change using test-driven development in Go via the specify-encode-fulfill loop (Kent Beck's Canon TDD). Use when the user invokes `/tdd <spec>` or asks to build something test-first, one test at a time, with a behavior change committed separately from any refactor. Adapted for this repo — Go stdlib tests (no testify), no Ruby/RSpec, and every test run goes through `make go-test` because the host has no Go toolchain.
argument-hint: [specification]
disable-model-invocation: true
---

# Test-Driven Development

## Initial Specification

$ARGUMENTS

## The Specify-Encode-Fulfill loop

Use "specify-encode-fulfill" (SEF):

1. **Specify**: come up with the specifications for what you want to build
2. **Encode**: encode those specifications as automated tests (executable specifications)
3. **Fulfill**: write the code to fulfill the specifications

At a finer grain — Kent Beck's [Canon TDD](https://tidyfirst.substack.com/p/canon-tdd):

1. Write a list of the specifications within scope of the current TDD session
2. Encode **one** item in the list as an automated test
3. Change the code *just barely enough* to *make the current test failure go away*. Avoid "speculative coding" — code written beyond what the current test needs risks never being exercised by any test
4. Optionally refactor, but **not before committing the behavior change**. Never mix a behavior change with a refactor in the same commit
5. Until the list is empty, go back to #2

## Clarifying Specifications

Before writing any test:

1. Repeat the specifications back to the user in your own words
2. Ask the user to confirm your articulation is correct or explain how it's wrong
3. If confirmed, proceed to writing tests; otherwise use the response and go back to step 1

Specifications take the form: "under scenario A, X happens; under scenario B, Y happens".

## Translating Specifications into Tests

This repo uses **Go stdlib tests** with small custom helpers (`t.Helper()`) and `t.Run` subtests or table-driven cases — **no testify**. See `internal/ui/output_test.go`, `internal/mountplan/defaults_test.go`, `cmd/config_test.go` for the house style.

Each scenario maps to a named subtest. If the spec is "when a run's status is `passed`, its label says `Passed`", the test looks like this:

```go
func TestLabel(t *testing.T) {
	t.Run("when status is passed", func(t *testing.T) {
		run := Run{Status: "passed"}
		if got := run.Label(); got != "Passed" {
			t.Errorf("Label() = %q, want %q", got, "Passed")
		}
	})
}
```

A **bad** way to write the same test:

```go
func TestLabel(t *testing.T) {
	run := Run{Status: "passed"}
	if got := run.Label(); got != "Passed" {
		t.Errorf("returns the correct value, got %q", got)
	}
}
```

Of course it returns the "correct" value — what else could we ever want? Never assert that a behavior "works correctly", "works properly", or "handles" some scenario. Specify in every scenario *what the correct behavior is*. The subtest name is the specification: `"when status is passed"` not `"label test"`.

## Workflow

1. The user invokes `/tdd` with a draft specification
2. After back-and-forth, agree on "final" specifications
3. Check whether we need to "clean the kitchen before we make dinner" (see below)
4. Write **just one** test (per Canon TDD)
5. Run it via `make go-test` to see it fail, show the user the test, and ask for approval before continuing
6. Write the application code, run `make go-test` again, then `/verify`. Show the code and ask for approval before committing. See "Fulfilling Test Specifications"
7. The user provides a new specification — start over from step 2

### Running tests in this repo

The host has **no Go toolchain** — every test run goes through the `golang:1.26` container:

- Full suite: `make go-test` (`go test ./... -count=1`).
- A single test: `make go-shell`, then `go test ./internal/pkg -run TestFoo -count=1`.
- Never invoke `go test` / `gofmt` / `golangci-lint` directly on the host — they won't be found.
- Close every cycle with the `/verify` skill (mirrors PR CI) before calling a change "done".

### Cleaning the Kitchen

Before writing a test, picture the test and where it goes. Does the conceptual framework of this new behavior slot tidily into the area of the code where it'll live? If not, is there a reconceptualizing of the current behavior that would make the result more conceptually elegant? If so, suggest it to the user. If the user approves, **abandon the current change, get to a clean working state, and on a new branch perform the refactoring**. "Clean the kitchen before you make dinner." Then pause, consult the user, and begin again.

### Fulfilling Test Specifications

Write **only enough code** to make the current test failure go away. Never use "defensive coding" — it's almost always speculative coding, i.e. code added without justification or feedback. After the test passes, optionally hand the diff to the `code-review` skill to scrutinize test and application code for design violations (e.g. a test focusing on means rather than ends).

### Don't Be Sloppy

This kind of thinking is bad:

> That failure is pre-existing (unrelated to our change). Our new tests pass. Want me to commit and push?

We don't make dinner in a dirty kitchen. If you discover a pre-existing failure, pause, stash the change, fix the pre-existing failure, then resume.
