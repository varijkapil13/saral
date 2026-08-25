# Architecture

Saral is a terminal client for Jira Cloud. It is built to be modular, testable without a Jira
instance, fast on large datasets, and safe for several people (or agents) to work on at once.

Three constraints drive every decision below:

1. **Nothing may be assumed about the Jira instance.** Field IDs, statuses, issue types, board
   configuration and permissions all vary per site.
2. **No shared mutable state.** The UI is a single-writer event loop; all IO happens in commands.
3. **Adding a feature must not require editing a file another feature owns.** Central switch
   statements are the enemy of parallel work, so we use registries instead.

## Layers

```
┌─────────────────────────────────────────────────────────────┐
│  cmd/saral            entrypoint, flags, subcommands        │
├─────────────────────────────────────────────────────────────┤
│  internal/ui          Bubble Tea models — one per view      │
│    kernel/            root model, view stack, registries    │
│    board/ issue/ ...  self-contained views                  │
│    widget/            shared table, form, pager, zones      │
├─────────────────────────────────────────────────────────────┤
│  internal/app         use cases — orchestration, no IO libs │
│                       cache policy: kinds, TTLs, the codec  │
├─────────────────────────────────────────────────────────────┤
│  internal/store       bbolt: the file, buckets, records     │
├─────────────────────────────────────────────────────────────┤
│  pkg/jira  (PORT)     interfaces + domain types             │
│    ├── cloud/         adapter: Jira Cloud REST v3 + Agile   │
│    ├── jiratest/      adapter: in-memory fake + httptest    │
│    └── (dc/ later)    adapter: Data Center                  │
│  pkg/adf              ADF ⇄ markdown, lossless              │
└─────────────────────────────────────────────────────────────┘
```

Dependencies point **downward only**. `pkg/*` must never import `internal/*`; `internal/ui` must
never import `pkg/jira/cloud` directly — it takes the port interface; only `cmd/*` and
`internal/config` construct a concrete adapter; `internal/app` must never import `internal/ui`,
because a use case is driven by a view and never reaches back up into one; `pkg/adf` must never
import `pkg/jira`, because the document library does not know about issues; `internal/store` must
never import `internal/ui`, because the cache is written to and read by the layers above it and never
renders anything; and `internal/ui` must never import `internal/store`, because a view takes what it
needs as an interface declared above the store and can then be driven by a fake. All seven are
enforced in CI by an import-boundary test (see `docs/TESTING.md`).

The "no IO libs" on `internal/app` is the one line above that no test can hold you to: the
import-boundary test only sees imports within this module, so a `net/http` in a use case is invisible
to it. Treat it as a rule a reviewer enforces. It means a use case does not open sockets or files
itself, not that it may not reach the layer below: `internal/app` holds the cache policy and so imports
`internal/store`, which is downward and deliberate. bbolt is named in exactly one package.

## Ports and adapters

`pkg/jira` defines *what* Jira can do; adapters define *how*. The port is deliberately narrow and
expressed in domain terms, not HTTP terms.

```go
// pkg/jira/port.go
type Client interface {
	Capabilities(ctx context.Context, projectKey string) (Capabilities, error)

	Search(ctx context.Context, q Query) (Page[Issue], error)
	Issue(ctx context.Context, key string) (Issue, error)
	CreateIssue(ctx context.Context, in IssueInput) (Issue, error)
	UpdateIssue(ctx context.Context, key string, in IssuePatch) error
	Transitions(ctx context.Context, key string) ([]Transition, error)
	Transition(ctx context.Context, key, transitionID string, in IssuePatch) error

	Comments(ctx context.Context, key string) (Page[Comment], error)
	AddComment(ctx context.Context, key string, body adf.Doc) (Comment, error)
	EditComment(ctx context.Context, key, id string, body adf.Doc) (Comment, error)
	DeleteComment(ctx context.Context, key, id string) error

	Attachments(ctx context.Context, key string) ([]Attachment, error)
	Upload(ctx context.Context, key string, files []FileRef) ([]Attachment, error)
	Download(ctx context.Context, id string, w io.Writer, opt DownloadOptions) error
	DeleteAttachment(ctx context.Context, id string) error

	Versions(ctx context.Context, projectKey string) ([]Version, error)
	SaveVersion(ctx context.Context, v VersionInput) (Version, error)
	UnresolvedCount(ctx context.Context, versionID string) (int, error)
	ReleaseVersion(ctx context.Context, id string, in ReleaseInput) (Version, error)

	Boards(ctx context.Context, projectKey string) ([]Board, error)
	BoardConfig(ctx context.Context, boardID int64) (BoardConfig, error)
	Sprints(ctx context.Context, boardID int64, states ...SprintState) (Page[Sprint], error)
	CreateSprint(ctx context.Context, in SprintInput) (Sprint, error)
	UpdateSprint(ctx context.Context, id int64, in SprintPatch) (Sprint, error)
	StartSprint(ctx context.Context, id int64) (Sprint, error)
	CompleteSprint(ctx context.Context, id int64) (Sprint, error)
	MoveToSprint(ctx context.Context, sprintID int64, keys []string) error
	MoveToBacklog(ctx context.Context, keys []string) error

	Fields(ctx context.Context) ([]Field, error)
	CreateMeta(ctx context.Context, projectKey, issueTypeID string) (Schema, error)
	BulkMove(ctx context.Context, in MoveRequest) (TaskRef, error)
	Task(ctx context.Context, ref TaskRef) (TaskStatus, error)
	Plans(ctx context.Context) ([]Plan, error)
	Me(ctx context.Context) (User, error)
}
```

Rules for the port:

- **No `PUT`-shaped methods that mirror the API.** `StartSprint` exists precisely because the
  underlying `PUT /rest/agile/1.0/sprint/{id}` nulls every omitted field. The port hides that.
- **No `map[string]any` in signatures.** Custom fields are carried in `Issue.Fields` as a typed
  `FieldSet` keyed by resolved field ID, with accessors that take a `FieldRef`.
- **An issue knows which fields it was read with**, in `Issue.Requested`. A narrow read is otherwise
  indistinguishable from an empty one — a nil `Assignee` means both unassigned and never asked for —
  and anything that merges, caches or writes an issue back has to tell those apart.
- **Every method takes `context.Context`** and must honour cancellation — views cancel in-flight
  work when they close.

### Roles: what a caller asks for

`Client` is what an adapter grows into. It is **not** what a caller takes. A view that runs a search
needs a search, and an adapter that cannot yet do the other thirty-three should not have to pretend —
the reasoning `internal/app.Counter` was already written with, generalised.

So `pkg/jira/roles.go` declares one interface per job, and callers take those:

```go
type Prober         interface{ Capabilities(ctx, projectKey) (Capabilities, error) }
type Identifier     interface{ Me(ctx) (User, error) }
type Searcher       interface{ Search(ctx, Query) (Page[Issue], error) }
type FieldCatalogue interface{ Fields(ctx) ([]Field, error) }
// … SchemaReader, IssueWriter, Mover, CommentReader, Commenter

// SessionClient is the union of every role the views in this build call.
type SessionClient interface { Prober; Identifier; Searcher; … }
```

`kernel.Deps.Jira` and `onboarding.Connector` take `SessionClient`. A batch that lands the adapter
method its own view needs adds that role to the union in the same PR; nothing that already compiled
stops compiling, and the diff says which capability the batch added.

A role restates a signature `Client` already carries, which is drift a reader cannot see. The
compiler can: one type cannot hold two methods of a name, so a role that drifts from the port makes
the assertions in `pkg/jira/jiratest` fail to build.

**Every adapter states what it satisfies, in its own built source:**

```go
var _ jira.Prober = (*Client)(nil)
```

Not in a `_test.go` file — an assertion there fails `go test` and not `go build`. `internal/arch`
fails an adapter package under `pkg/jira/**` that carries none at all. Their absence is exactly how
`pkg/jira/cloud` implemented 12 of 34 methods for two batches while passing CI, lint, race and a
cross-build: nothing outside the package ever assigned a `*Client` to anything, so nothing asked.

## Two pagination models, one type

The platform API pages by opaque cursor and returns no total; the Agile API pages by `startAt` and
does return one. One generic type covers both so widgets never branch on which API they came from.

```go
type Page[T any] struct {
	Items       []T
	next        func(context.Context) (Page[T], error) // nil when exhausted
	ApproxTotal *int                                   // nil for cursor endpoints
}

func (p Page[T]) HasMore() bool
func (p Page[T]) Next(ctx context.Context) (Page[T], error)
```

The cursor implementation must detect a repeated token and treat it as exhaustion — there are
reports of Jira returning a token that loops back to page one.

## Capabilities as a value object

The probe runs once per site and project, on the kernel's `Init`, and is refreshable with `R`. Views
read it; nobody re-probes ad hoc. It is **not** kept between runs: what a token may do is exactly the
kind of answer that changes without warning, and a first frame drawn from a stored one would hide or
offer a view on last week's permissions. Persisting it needs the kernel to revalidate behind the
frame, which is [#81](https://github.com/varijkapil13/saral/issues/81).

```go
type Capabilities struct {
	Plans          Capability // Administer Jira required
	BulkMove       Capability // BULK_CHANGE + MOVE_ISSUES + CREATE_ISSUES
	Boards         Capability // project has at least one board
	Attachments    Capability // instance-wide setting
	DeleteIssues   Capability
	Graphics       GraphicsMode // kitty | iterm2 | halfblocks | none
	TimeZone       *time.Location
	TimeZoneReason string // why the zone is not the account's own; empty when it is
}

type Capability struct {
	OK     bool
	Reason string // shown in the UI when !OK — never swallowed
}
```

A view whose capability is absent is never registered into the footer, and any keybinding that would
reach it renders `Reason` in the status line. **A 403 is a capability answer, not an error.**

A timezone the probe could not establish is reported the same way. `TimeZone` stays nil,
`TimeZoneReason` carries the sentence — a refusal in Jira's own words, a zone name this machine has
no zoneinfo entry for, or an account that named none — and `Zone()` hands a view both at once. Dates
render in UTC whichever it was, so that sentence is the only thing on screen that can say why they
may be an hour out.

The probe is scoped to a project, because three of these are: boards belong to a project, and Jira
scopes Move, Delete and Create as project permissions, so one token answers differently in two
projects on one site. Probing with no project leaves those three unavailable with a reason saying so.

## The UI kernel and its registries

`internal/ui/kernel` owns the root model, the view stack, focus, the theme and three registries.
Views are added by *registering*, never by editing the kernel.

```go
// internal/ui/board/register.go  — owned solely by the board packet
func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:       "board",
		Title:    "Board",
		Slot:     2,                       // footer position, reached with g2
		Requires: kernel.CapBoards,        // hidden when absent
		New:      func(d kernel.Deps) kernel.View { return New(d) },
	})
}
```

Three registries, all with the same conflict-free property:

| Registry | Registered by | Consumed by |
|---|---|---|
| `RegisterView` | each view package's `register.go` | footer, view stack, `saral <view>` |
| `RegisterCommand` | any package | command palette (`ctrl+k`) |
| `RegisterKeys` | each view, scoped to itself | help overlay, footer hints |

Slots are allocated, not picked: the table in `docs/UX.md` says which view holds which digit, and
`RegisterView` refuses a second claim on one at startup. A bare digit runs a saved query in a root
view, so a slot is reached with `g` and its digit; the kernel buffers that `g` rather than forwarding
it, because two views already spend `g` on gestures of their own.

A command carries the ways to reach the same action without the palette, in `Command.Keys`, each
spelt the way the owning view's footer spells it — the help label of the binding, so `"a"` and not
the `"a c"` it also answers to. The registrar writes them down because nothing can work them out: a
command ID says nothing about a keybinding, and `issue.edit` is both a command and a view whose keys
belong to the editor pane rather than to the command. A command that opens a view by its footer slot
asks `kernel.SlotGesture` for the key rather than spelling it, so that moving a view between slots
cannot leave a command teaching the key of a different one. An empty set means there is no key, and
the palette then shows none rather than guessing. `internal/ui` holds the sweep that checks each
command's key against what its view renders; the kernel cannot, because it may not import a view.

Nothing calls `Command.Run` itself. A `RunCommandMsg` names a command by ID and the kernel does the
rest: it refuses one the site does not allow in the capability's own words, closes the palette if
that is what is on top, and runs `Run` against the deps the kernel holds — current as of the
keypress, rather than as of whenever the palette was built. A command scoping a search to
`Deps.Project` from a stale copy searches the whole site and looks like it worked. Afterwards every
view hears a `CommandRanMsg` carrying the ID and the keys, which is where counting what gets used and
offering the key for it hang.

`ctrl+k` **pushes** the palette rather than switching to it. Opening it as a root view would discard
whatever it was pressed from — an editor, a form, a comment thread — leave `esc` with nothing to pop
back to, and silence every command that reaches a view by broadcast. It is built fresh each time, so
a command is offered the session as it is rather than as it was the first time the palette opened,
and pressing it again while it is up does nothing rather than stacking a second one.

A view that is taking typing — a filter, a form field, the command palette — implements
`kernel.KeyCapturer` and answers `WantsRawKeys() true` while it is. The kernel then hands it every
key except `ctrl+c` and `ctrl+k`, and the footer drops the globals it is swallowing while keeping the
one it cannot. Without it a global keymap makes the letters `q` and `r`, and the escape key,
unreachable inside any text input.

The two exceptions are not equally free. `ctrl+c` costs nothing to reserve. `ctrl+k` costs
kill-to-end-of-line: `bubbles` binds it to delete-after-cursor in both `textinput` and `textarea`, and
no view here overrides that, so reserving it takes the gesture out of every field in the program
([#80](https://github.com/varijkapil13/saral/issues/80)). It is reserved anyway, because a palette that
cannot be opened from the editor it is most wanted in is worse than a field that cannot kill a line —
but it is a real loss rather than a free one, and the views owe it a replacement binding.

A view holding something unsaved implements `kernel.Blocker`. Going back asks the view being popped;
quitting and switching root view ask **every** entry on the stack, because both throw all of it away
and the entry holding the draft is usually not the top one — the palette is pushed over whatever it
was opened from and holds nothing itself.

Because every registration lives in a file that exactly one packet owns, two agents adding two views
never touch the same line. This is the single most important structural decision for parallel work.

The one file that does grow per view is `internal/ui/views.go`, a list of blank imports that pulls each
view package in so its `init()` runs:

```go
package ui

import (
	_ "github.com/varijkapil13/saral/internal/ui/board"
	_ "github.com/varijkapil13/saral/internal/ui/issue"
)
```

**Ownership policy:** `internal/ui/views.go` is created by P0.1 and is the *only* shared file a view
packet may edit, restricted to adding its own single import line in alphabetical order. One line each
means git resolves concurrent additions cleanly; a conflict here is a one-second fix rather than a
semantic merge.

## Message flow

Standard Elm/MVU with Bubble Tea v2. The kernel routes messages; views never talk to each other
directly.

```
KeyPressMsg / MouseClickMsg
        │
        ▼
  kernel.Update ──► focused view.Update ──► tea.Cmd (async IO)
        │                                        │
        │                                        ▼
        │                                  jira.Client call
        │                                        │
        └──────────◄── typed result Msg ◄────────┘
```

Conventions:

- One message type per outcome, named for the outcome: `IssuesLoadedMsg`, `IssueLoadFailedMsg`.
  Never a generic `DataMsg` with an `error` field and a switch on nil.
- Commands are pure functions returning `tea.Cmd`; they close over a context tied to the view's
  lifetime.
- Cross-view effects go through `kernel.Broadcast` (e.g. an issue edited in the detail view tells
  the board to refresh that one row) — not by holding a pointer to another model.
- **Request coalescing:** identical in-flight requests are deduplicated in `internal/app` with a
  singleflight keyed on the request signature. Rapid cursor movement must not fan out N fetches.

## Caching

`internal/store` is bbolt: the file, the buckets, and records that carry the moment they were
written. It knows nothing about issues. The policy over it — what the kinds are, how long each lives,
how a value is encoded, and what a bound is — is `internal/app/cache.go`, together with the `app.Cache`
interface a view reaches it through. That interface is declared with the implementation that exercises
it so that its shape answers to a caller rather than being guessed at, and it is declared in
`internal/app` because `internal/ui` sits above `internal/store` and must not import it, which
`internal/arch` enforces.

Stale-while-revalidate, as it actually runs:

1. A view reads the cache **in its constructor**, not in `Init`. `kernel.FirstPaint` builds a view and
   renders one frame without ever calling `Init`, so a read there is a read that never happens on the
   path docs/PERFORMANCE.md budgets. First paint never waits.
2. Rows inside their TTL are drawn and nothing is asked of the site at all. Rows past it are drawn and
   revalidated behind the frame.
3. The revalidation arrives as the same cursor-preserving patch a manual refresh uses, so the row
   under the cursor, the scroll offset and the filter all survive it.
4. Anything still on screen that could not be confirmed — past its TTL, or a 403, 429 or transport
   failure over the top of it — is badged with `Theme.StaleBadge` rather than cleared. Seeing
   yesterday's rows beats seeing none.

TTLs by kind, from `Kind.TTL()`: fields and createmeta 24h, board config 1h, versions 10m, issue 60s,
search 30s. All refreshable on demand; `R` also drops the stored answer rather than only refetching.
The cache is keyed by site + account through `store.Scope`, so profiles cannot bleed into each other.
The account is the profile's email: the Jira account ID takes a round trip to learn, and the first
frame is drawn before one could have answered.

Issues are stored once each, keyed by issue key, and a search stores the keys it matched and their
order. That is what makes two things work. A refresh merges into the copy already held rather than
replacing it, so a narrow read cannot blank a field it never asked for — `app.MergeIssue` over
`Issue.Requested`, which is what that mask exists for. And the issue bucket is one corpus rather than
one per search, bounded to `app.DefaultIssueBound` (5,000) by dropping what was stored longest ago, so
a long session cannot grow the file without limit.

`Cache.Generation()` counts every write. Anything holding a derived copy of the cache — the local
fuzzy index in P3.4 — compares one number to find out that its copy is behind, rather than walking the
corpus to check. It over-reports rather than under-reports: a write a particular reader does not care
about still moves it, which costs that reader a rebuild and never leaves it answering from a stale
index. An in-memory counter is a complete answer because bbolt holds the file exclusively, so the
process that has it open is the only writer there is.

A session with nowhere to keep a cache carries a nil one and every caller draws without it: a first
run has nothing on disk, another copy of Saral may be holding the file (`store.ErrLocked`, which must
not stop the program starting), and a home directory is not always writable.

## Rendering and performance

Bubble Tea v2's renderer already diffs frames; our job is to not hand it work it doesn't need.

- **Virtualize every list.** Render only the visible window plus a small overscan. A 10k-row board
  and a 20-row board must cost the same per frame.
- **Memoize row rendering** keyed by `(issue.UpdatedAt, width, selected, theme generation)`.
  Styling is the expensive part, not layout.
- **Never style in a loop over all data.** Build `lipgloss` styles once at theme load, not per cell.
- **No allocation per frame in the steady state.** Reuse row buffers; benchmarks assert `allocs/op`.
- **Width-aware truncation** with grapheme-cluster awareness, cached per string.

Budgets and how they are measured live in `docs/PERFORMANCE.md`; CI enforces the binary-size one.

## Mouse

Mouse is a first-class input, not an afterthought. Bubble Tea v2 declares it in the view:

```go
func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}
```

Hit-testing uses [bubblezone](https://github.com/lrstanley/bubblezone): each interactive element
wraps its rendered string in a zone ID, and a click is resolved by lookup rather than by arithmetic
on coordinates. Required interactions: click to focus a pane, click to select a row, double-click to
open, wheel to scroll the pane under the pointer, drag the pane divider to resize, click a status
chip or label to filter by it, click the footer entries to switch views.

## Errors

A typed taxonomy in `pkg/jira`, because the UI needs to render different things:

| Error | UI behaviour |
|---|---|
| `*RateLimitError` (429, `Retry-After`) | back off, show a countdown, pause any poller |
| `*CapabilityError` | disable the action and show `Reason` |
| `*ValidationError` (field → message) | annotate the offending form fields inline |
| `*ConflictError` (409) | offer reload-and-reapply |
| `*TransportError` | show cached data with a stale badge, retry in background |

`errors.As` is the only way callers inspect these. No string matching on messages.

## Configuration

XDG paths, profiles, and **no secrets in the config file** — it must stay safe to share or commit.

```toml
# ~/.config/saral/config.toml
active = "work"

[profiles.work]
site  = "example.atlassian.net"
email = "you@example.com"
token = { keychain = "saral:work" }   # or { env = "JIRA_TOKEN" } / { command = "pass jira" }

[profiles.work.timeline]
start = ["Target start", "Start date"]   # resolved by name to field IDs at runtime
end   = ["Target end", "Due date"]

[[profiles.work.queries]]
name = "Blockers"
jql  = "priority = Highest AND resolution = EMPTY ORDER BY updated DESC"
key  = 2                                 # the number key that runs it; omit for none
```

The queries are held by `app.SavedQueries` and validated by its rules rather than a second copy of
them, so a file and a keypress cannot disagree about what a saved query is. The file refuses the two
things a keyboard cannot express: two queries under one name, and two on one key.

## Decisions recorded separately

Anything architecturally load-bearing gets an ADR in `docs/adr/`. Start there before proposing a
change to the layers or the port.
