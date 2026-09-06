# Serving artifacts from the bridge origin: the daemon holds the bytes and opens a URL at itself

Status: accepted

An agent working inside the container writes an HTML report and tries to show
it: `xdg-open report.html`. Nothing happens on the host, and the two reasons
are independent.

The first is deliberate. `internal/bridge/allowlist.go` admits only `http` and
`https` on `/open`, and `docs/bridge.md` (§Security boundary) records why:
`file://` would let any process in the container aim the host's default handler
at an arbitrary host path — `~/.ssh/id_rsa` rendered in the user's editor is
the canonical example. The scheme allowlist is one of the few things standing
between a readable bridge token and the host filesystem.

The second is structural, and it survives the first. Only the workspace is
bound into the container; an agent's scratchpad is container-local storage. A
path the daemon was willing to accept would still name a file the host cannot
see, so relaxing the allowlist would fix the error message and not the feature.

**We serve the artifact instead of naming it.** The container-side `xdg-open`
shim, when its argument resolves to a local file rather than a URL, reads the
bytes and POSTs them to a new `/view` route. The daemon keeps them in memory
under an id drawn from `crypto/rand`, then calls the same host URL handler
`/open` uses — with `http://127.0.0.1:<port>/artifact/<id>`, its own listener.
`ValidateURL` passes that URL unmodified. The scheme allowlist is never
touched, and no file is ever written to the host.

## Considered options

**Allow `file://` on `/open`.** The obvious move, and the one a future reader
will reach for first. It trades the scheme allowlist — a boundary that is cheap
to hold and expensive to re-establish once callers depend on it being open — for
a feature it does not actually deliver, since the scratchpad case still fails.
Rejected on both counts.

**Copy the artifact into the workspace and reuse `/edit`.** `/edit` already
takes host paths and already has the container→host translation in
`internal/build/assets/bin/code`. But it opens an editor, and the artifact is a
rendered document: the user would land in a source view and click through to a
browser. It also puts a generated file in the user's repo, where git has to be
told to ignore it.

**A second listener on its own port**, so the artifact origin differs from the
control-API origin. It buys the same isolation the CSP buys, and costs a port
to allocate, publish through the state directory, and hold in the shim contract
test. Rejected as the more expensive half of a pair.

## Consequences

**The artifact `GET` is the only unauthenticated bridge route.** A browser
cannot be handed an `Authorization` header, so the id in the URL is the
credential. It is scoped to one artifact and dies with it, which is a smaller
secret than the bridge token would be as a query parameter — that variant was
rejected precisely because it would copy the token into browser history and
into the audit log. The URL is still a credential in a history file, and the
docs say so.

**Container-authored active content runs on the daemon's origin.** Same-origin
`fetch` to `/proximo` or `/credential` needs no preflight; today it would take a
401, but "the other routes check a token" is a single line of defence and this
ADR adds a route that does not. The artifact response therefore carries
`connect-src 'none'; form-action 'none'; frame-ancestors 'none'`, with inline
script, inline style and `data:` images permitted. The page renders; it cannot
talk to the daemon and it cannot exfiltrate what it shows.

**Self-contained artifacts only.** `connect-src 'none'` means an artifact that
pulls a charting library from a CDN renders broken. This is the constraint that
does not live in the code that enforces it: whoever generates the HTML has to
inline everything. The `/view` route serves an allowlisted set of extensions —
HTML at first — for the same reason `editorAllowlist` is closed: a route that
opens whatever the host's default handler claims is a route with no boundary.

**Memory is the budget, and it is bounded.** Artifacts live in the daemon
process, capped at 8 MiB each and 32 MiB in total with LRU eviction, plus a
one-hour TTL. Bytes rather than a count, because bytes are the resource that
runs out; LRU, because the artifact being read is the one that must not be
evicted; a TTL, because a daemon that runs for weeks should not still hold
Tuesday's report. Nothing survives a daemon restart, which is the flip side of
never writing to the host: an open tab reloaded after a restart gets a 404
rather than a stale file nobody would have cleaned up.

**Failure stays quiet.** `xdg-open` exits 0 when the bridge is missing or
unreachable, and this route inherits that: the shim prints the container path
and the `toolbox bridge install` hint on stderr. The feature degrades to the
behaviour that prompted this ADR, which is a usable fallback rather than a
broken command.
