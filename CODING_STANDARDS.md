# Coding Standards

## Security-linter suppressions

Treat every new or changed `//nolint:gosec` as a security review, not a lint
cleanup. Trace the flagged value from its source through validation to the sink,
and accept the suppression only when that concrete path cannot cross the stated
trust boundary unsafely.

The suppression comment must name the trusted source or validation and the
safety property that makes the sink intentional. “No shell is invoked” proves
only that shell parsing is absent; variable executables and arguments still need
the invoked program's option and side-effect semantics reviewed.

Prefer removing the unsafe flow. Scope exclusions, such as omitting test
fixtures, are threat-model decisions and must not be described as false
positives.

## Behaviour changes reach the user-facing docs

A change a developer can observe — a new or removed command, a step that now
runs on demand instead of automatically, a changed default — updates the pages
that describe that surface to its user: the `README.md` Highlights and Updating
sections, the how-to under `docs/`, and `docs/troubleshooting.md` when the
change creates a new way to end up stuck. Updating the glossary, the internals
and the ADR alone leaves the reader who never opens them with a wrong picture.
Review the diff against each of those pages and name the one left behind.
