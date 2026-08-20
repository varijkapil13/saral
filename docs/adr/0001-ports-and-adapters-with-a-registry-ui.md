# ADR 0001 — Ports and adapters, with a registry-based UI

- **Status:** accepted
- **Date:** 2026-08-19

## Context

Saral must run against any Jira Cloud site, be testable without a Jira instance, and be worked on by
several agents in parallel without constant merge conflicts. It should also be able to grow a Jira
Data Center backend and a Confluence surface later without a rewrite.

Two problems dominate:

1. Jira's API is wide, inconsistent and instance-dependent. Letting its shape leak into the UI would
   make the UI untestable and every instance-specific quirk viral.
2. In a typical TUI, adding a view means editing a central dispatcher — a `switch` over view IDs, a
   keymap table, a footer definition. Those files become permanent merge conflicts when several
   people add views at the same time.

## Decision

**Ports and adapters** for the Jira boundary: `pkg/jira` defines a narrow port in domain terms with
adapters for Cloud REST, an in-memory fake, and (later) Data Center. The port deliberately does not
mirror HTTP — for example it exposes `StartSprint` rather than a sprint `PUT`, because the underlying
call nulls omitted fields.

**Registries instead of dispatchers** in the UI: views, commands and keybindings self-register from a
file inside the view's own package. The kernel iterates registrations; it never enumerates views.

**Capabilities as data**: a probe produces a `Capabilities` value with a human-readable reason for
each negative, and views are gated on it rather than discovering 403s at the point of use.

## Consequences

Good:

- Adding a feature is additive — new directory, new registration, no shared file touched. This is
  what makes many parallel agents viable.
- Everything above the adapter is testable with no network, so CI needs no credentials and agents
  need no shared sandbox.
- Instance quirks are contained in one adapter; a second backend is an added file tree, not a rewrite.
- Degradation is designed rather than emergent, because a missing capability has a reason attached.

Costs:

- The port is a shared file and a genuine bottleneck. Extending it needs a small, fast-reviewed PR
  labelled `contract`, and it is frozen after Batch 0 for signature changes.
- Registration via `init()` means import side effects; the kernel needs a blank-import list in one
  place (`internal/ui/views.go`) which is the one file that grows per view. It is a single line each
  and conflicts trivially resolve.
- A layer of mapping code between wire types and domain types that a direct client would not need.

## Alternatives rejected

- **Thin wrapper over an existing Jira Go client.** None model capabilities, and the sprint `PUT`
  footgun is exposed by all of them.
- **Direct HTTP calls from views.** Fastest to start, untestable, and every instance quirk would
  spread across the UI.
- **Central view dispatcher.** Simpler to read, but it is precisely the file that makes parallel work
  painful.

## Implementation notes from P0.1

Decisions taken while landing the contracts that later packets inherit and should not relitigate
without a new ADR.

**The error taxonomy is seven types, not five.** `docs/ARCHITECTURE.md` lists the five the UI renders
differently. `*AuthError` (401) and `*NotFoundError` (404) were added because the Cloud adapter has
to map those statuses to something, and inventing them later would mean touching the frozen package
from a feature packet. `*TransportError` deliberately covers 5xx as well as dial and timeout
failures: from the UI's point of view they are the same event — no usable answer, keep the cached
data, retry in the background.

**`Page[T]` ships its own pagination drivers.** `next` is unexported, so an adapter in another
package cannot build a page directly. `jira.Cursor` and `jira.Offset` are the two constructors, and
the repeated-token guard lives inside `Cursor` rather than in each adapter method — the bug it
defends against is a property of Jira, not of any one endpoint.

**`pkg/adf` keeps the original bytes.** Round-tripping a document byte-for-byte is a requirement, and
ADF's JSON key order is neither documented nor stable, so a canonical re-encoding cannot reproduce
it. Each node stores the bytes it was parsed from plus a hash of its canonical form; on marshal the
canonical form is recomputed and the original bytes are re-emitted only when the hash still matches.
That makes verbatim output safe even though the model's fields are freely assignable, which a dirty
flag would not. `adf.Marshal` exists because `encoding/json` compacts and HTML-escapes whatever a
`MarshalJSON` returns, which is valid but no longer byte-identical.

**Capabilities are keyed.** `jira.CapabilityKey` and `Capabilities.Capability(key)` exist so a
`ViewSpec` can name what it needs as data. Without a key there is no way to gate a view without a
switch over capability fields somewhere, which is the thing this ADR exists to avoid.

**Bad registrations are recorded, not raised.** `init()` runs before anything can handle an error and
`docs/PARALLEL.md` forbids a panic outside `main`, so `RegisterView` appends to
`kernel.RegistrationErrors()` and `kernel.New` refuses to start. A view that silently failed to
register is much harder to diagnose than a message at startup.

**Quitting is guarded by an interface, not by kernel state.** A view that would lose the user's work
implements `kernel.Blocker`; the kernel asks it and shows the reason. The kernel therefore knows
nothing about drafts, which is what keeps `docs/UX.md`'s "never lose the user's text" out of the
root model.

**`gocritic`'s `hugeParam` is disabled.** Bubble Tea's MVU loop requires value receivers on the model,
so it fires on every method of every view. The alternative was a `nolint` on each one, forever.

**Three signatures moved before the freeze**, each because the alternative afterwards is a
deprecation step rather than an added method:

- `Capabilities` takes a project key. Boards belong to a project and Jira scopes Move, Delete and
  Create as project permissions, so a site-wide probe answers three of the five capabilities wrongly
  — and the kernel gates the footer and every keybinding on that answer.
- `Download` takes a `DownloadOptions` with a `From` offset. P4.1 is defined as resumable, and an
  `io.Writer` carries no position, so without it a dropped 300 MB transfer starts again.
- `Sprints` takes the states to narrow to. The board view needs one active sprint, and the only
  alternative was walking a four-year-old board's whole sprint history on a first-paint path.

**The fake masks struct fields, not just the custom-field set.** A search naming `summary` used to
come back with the assignee, the priority and the description populated. Every list view would have
been written and golden-tested against data the real endpoint does not send.

**`adf.Doc` has a deep `Clone`, and `FieldSet` uses it.** Copying a `FieldValue` cloned its slices
but not the node tree inside a rich-text value, so "immutable" stopped one level short and a cached
issue's description was still writable through anything handed out of the cache.

**bbolt is not in `go.mod`.** The issue listed it among P0.1's dependencies, but nothing in this
packet imports it and `make check` runs `go mod tidy && git diff --exit-code`, so it would be
stripped on the first CI run. `docs/PARALLEL.md` already says where it belongs: "add dependencies in
their own tiny PR, merged first" — that is P3.2, the packet that builds `internal/store`.
`docs/ROADMAP.md` now says so too. Adding an unused import here to satisfy a checklist would mean
creating a package P3.2 owns.

**`FieldSet` is immutable.** `With` and `Without` return a new set rather than changing the receiver.
A `FieldSet` is a value that travels into an `Issue`, into an `IssuePatch` and out of the cache; with
a mutable map behind it, seeding an edit form from a cached issue silently rewrites the cached issue.
The same reasoning applies one level down, so the slices inside a `FieldValue` are copied on the way
in and on the way out.

**P0.1 touched three paths outside the list in `docs/ROADMAP.md`**, each because no packet owns them
and a document assigns them here:

- `internal/ui/views.go` — `docs/ARCHITECTURE.md` says in as many words that P0.1 creates it.
- `cmd/saral/**` — no packet owns the entrypoint, and `docs/PERFORMANCE.md` puts `--bench-first-paint`
  in the kernel "rather than a later addition". Unwired, the flag is unreachable and both start-up
  budgets stay unmeasurable.
- `internal/arch/**` — the issue asks for the import-boundary test and `docs/TESTING.md` names the
  package.

Also touched: `.golangci.yml` (one disabled check, above), `docs/API-NOTES.md` (two API facts found
while building, which `AGENTS.md` asks for) and `docs/ROADMAP.md` (the status tick).

**No `DeleteVersion` or `DeleteIssue` on the port, despite `CapDeleteIssues`.** `docs/SCOPE.md` lists
releases as "create, edit, archive" with no delete, and issue deletion is not in scope at all, so the
port stays without them. The capability is still probed because the UI needs to know whether to offer
anything at all. If P5.1 disagrees, that is a `contract` issue and a two-line addition, not a
signature change.
