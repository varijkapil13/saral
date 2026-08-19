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
├─────────────────────────────────────────────────────────────┤
│  internal/store       bbolt cache, stale-while-revalidate   │
├─────────────────────────────────────────────────────────────┤
│  pkg/jira  (PORT)     interfaces + domain types             │
│    ├── cloud/         adapter: Jira Cloud REST v3 + Agile   │
│    ├── jiratest/      adapter: in-memory fake + httptest    │
│    └── (dc/ later)    adapter: Data Center                  │
│  pkg/adf              ADF ⇄ markdown, lossless              │
└─────────────────────────────────────────────────────────────┘
```

Dependencies point **downward only**. `pkg/*` must never import `internal/*`; `internal/ui` must
never import `pkg/jira/cloud` directly — it takes the port interface. This is enforced in CI by an
import-boundary test (see `docs/TESTING.md`).

## Ports and adapters

`pkg/jira` defines *what* Jira can do; adapters define *how*. The port is deliberately narrow and
expressed in domain terms, not HTTP terms.

```go
// pkg/jira/port.go
type Client interface {
	Capabilities(ctx context.Context) (Capabilities, error)

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
	Download(ctx context.Context, id string, w io.Writer, progress func(int64)) error
	DeleteAttachment(ctx context.Context, id string) error

	Versions(ctx context.Context, projectKey string) ([]Version, error)
	SaveVersion(ctx context.Context, v VersionInput) (Version, error)
	UnresolvedCount(ctx context.Context, versionID string) (int, error)
	ReleaseVersion(ctx context.Context, id string, in ReleaseInput) (Version, error)

	Boards(ctx context.Context, projectKey string) ([]Board, error)
	BoardConfig(ctx context.Context, boardID int64) (BoardConfig, error)
	Sprints(ctx context.Context, boardID int64) (Page[Sprint], error)
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
- **Every method takes `context.Context`** and must honour cancellation — views cancel in-flight
  work when they close.

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

The probe runs once per site, is cached in the store, and is refreshable with `R`. Views read it;
nobody re-probes ad hoc.

```go
type Capabilities struct {
	Plans        Capability // Administer Jira required
	BulkMove     Capability // BULK_CHANGE + MOVE_ISSUES + CREATE_ISSUES
	Boards       Capability // project has at least one board
	Attachments  Capability // instance-wide setting
	DeleteIssues Capability
	Graphics     GraphicsMode // kitty | iterm2 | halfblocks | none
	TimeZone     *time.Location
}

type Capability struct {
	OK     bool
	Reason string // shown in the UI when !OK — never swallowed
}
```

A view whose capability is absent is never registered into the footer, and any keybinding that would
reach it renders `Reason` in the status line. **A 403 is a capability answer, not an error.**

## The UI kernel and its registries

`internal/ui/kernel` owns the root model, the view stack, focus, the theme and three registries.
Views are added by *registering*, never by editing the kernel.

```go
// internal/ui/board/register.go  — owned solely by the board packet
func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:       "board",
		Title:    "Board",
		Slot:     1,                       // footer position
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

`internal/store` is bbolt with typed buckets and a stale-while-revalidate policy:

1. A view asks for data and gets whatever is cached **synchronously** — first paint never waits.
2. If the entry is older than its TTL, a background refresh is issued.
3. When it lands, a `Msg` carries the delta and the view patches its rows in place, preserving the
   cursor and scroll position.

TTLs by kind: fields and createmeta 24h, board config 1h, versions 10m, issue detail 60s, search
results 30s. All refreshable on demand. The cache is keyed by site + account so profiles cannot
bleed into each other.

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
```

## Decisions recorded separately

Anything architecturally load-bearing gets an ADR in `docs/adr/`. Start there before proposing a
change to the layers or the port.
