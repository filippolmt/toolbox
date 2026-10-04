# Domain docs

This repository uses a single-context domain documentation layout.

## Before exploring

- Read `CONTEXT.md` for project vocabulary and concept ownership.
- Read relevant ADRs under `docs/adr/`.
- Follow topic pointers from `docs/README.md`.

Proceed silently when no relevant ADR exists.

## Use established vocabulary

Use domain concepts exactly as defined in `CONTEXT.md`. Before refactoring across a pipeline seam, read the corresponding entry.

When a design conversation gives a new concept its name, add it to `CONTEXT.md`. Do not introduce competing synonyms.

## Flag ADR conflicts

If proposed work contradicts an ADR, identify the conflict explicitly rather than silently overriding the decision.
