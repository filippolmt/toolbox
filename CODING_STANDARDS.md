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

## A behaviour change is driven by a red test

The repo builds test-first (`CLAUDE.md`, the `tdd` skill). A commit that
changes behaviour carries the test that pins it, or follows a commit whose test
failed without it. A test landing after the change it pins, green on its first
run, has not been seen to catch anything until it is shown red: break the code
on purpose, watch the test fail, and say so in the commit message or PR body
together with the change it pins. Docs-only and pure-refactor commits are
exempt. Review a late test by naming the commit pair and asking for that
evidence when neither message carries it.

## Behaviour changes reach the user-facing docs

A change a developer can observe — a new or removed command, a step that now
runs on demand instead of automatically, a changed default — updates the pages
that describe that surface to its user: the `README.md` Highlights and Updating
sections, the how-to under `docs/`, and `docs/troubleshooting.md` when the
change creates a new way to end up stuck. Updating the glossary, the internals
and the ADR alone leaves the reader who never opens them with a wrong picture.
Review the diff against each of those pages and name the one left behind.

## Two routes to one operation share their exceptions

When a flag and a config key reach the same operation — `--profile` and
`mounts_root` both retarget the Mount Plan — every exception to it (a mount
that stays on the host, a warning, a validation) lives in the one place both
routes call, and a test table runs each exception through every route. An
exception written on one route only makes the routes drift apart while the
docs still call them the same, and a sweep test over the other route then pins
the drift as intended. Review a change to either route by naming its sibling
and checking the exception reaches both.

## A wait on container-side work carries its own deadline

A Docker call that waits for something inside a container — an exec, a
probe, a container wait — bounds itself with a timeout of its own rather than
trusting the caller's context to carry one. Review each new wait by naming the
deadline that ends it.

## Removing host state the command did not create is opt-in

A command that would delete or replace something the user may own on the
host — a registered machine, a volume, a config entry — refuses by default,
names what it found and the flag that replaces it, and acts only under that
flag. Review each remove call by asking who created the thing it removes.
