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
