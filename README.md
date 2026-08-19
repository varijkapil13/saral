# Saral

**Jira in your terminal, made simple.**

*Saral* (सरल) is Hindi for simple, straightforward, plain — the opposite of what happens to a tool
when every team bolts another field onto it. That is the whole idea: Jira's data, none of Jira's web
app.

> **Status: planning.** The architecture and the work plan are complete and reviewed; no features are
> implemented yet.
>
> Work is tracked as [31 packet issues](https://github.com/varijkapil13/saral/issues) across
> [10 milestones](https://github.com/varijkapil13/saral/milestones), one milestone per batch.
> Start at [`docs/ROADMAP.md`](docs/ROADMAP.md) — every packet links to its issue.

```
┌─ saral ─ example.atlassian.net ─────────────────────────────────────────────┐
│ PROJ ▾  board: Team Board ▾   Sprint 42 · 4d left            28 issues  ⟳   │
├──────────────────────────────────┬──────────────────────────────────────────┤
│  KEY       SUM               PTS │  PROJ-142  Search returns stale rows     │
│ ▸ PROJ-142 Search returns …    3 │  ──────────────────────────────────────  │
│   PROJ-139 Retry on 429        5 │  Status    In Progress → [t] transition  │
│   PROJ-137 Cursor loop guard   2 │  Assignee  unassigned      [a] assign    │
│   PROJ-131 Upload progress     — │  Sprint    Sprint 42       [s] move      │
│   PROJ-128 Board config cache  8 │  Fix Ver   2.4.0           [v] version   │
│                                  │                                          │
│  ⋯ loading next page             │  ## Description                          │
│                                  │  The list keeps a cursor after the query │
│  ── attachments ───────── 3 ──   │  changes, so page 2 belongs to page 1.   │
│   ▸ trace.log       1.2 MB       │                                          │
│     mockup.png      412 KB  ⬛⬛  │  ── Comments ─────────────────── 2 ───   │
│     spec.pdf        890 KB       │  you  2h   Repro'd on 3.1 too.           │
├──────────────────────────────────┴──────────────────────────────────────────┤
│ 1 board 2 backlog 3 sprints 4 releases 5 timeline 6 plans  c comment m move │
└─────────────────────────────────────────────────────────────────────────────┘
```

## What it does

- **Tickets** — JQL search, read, create, edit, transition. Forms are generated from your instance's
  own `createmeta`, so they are right on any site.
- **Comments** — add, edit, delete, in `$EDITOR`, with faithful markdown ⇄ ADF conversion.
- **Attachments** — upload, ranged download with progress, and inline image preview in terminals that
  support graphics.
- **Releases** — manage versions *and* release them, with the unresolved-issue decision the web app
  makes you take.
- **Sprints and boards** — columns from your board configuration, backlog, create/start/complete
  sprints, move issues, rank-aware reordering.
- **Cross-project move** — a wizard over Jira's bulk-move API with status and field remapping.
- **Timeline** — built from real start and end dates, with sprint and version markers.
- **Plans** — the real thing if your token has Administer Jira, locally defined plans if it doesn't.

## Design commitments

**Nothing about your Jira is assumed.** No project keys, custom field IDs, statuses or permissions are
baked in. Field IDs are resolved by name, board columns come from board configuration, and required
fields come from `createmeta`.

**Capabilities are probed, not guessed.** On first connect Saral works out what your token can
actually do and caches it. Features you lack are hidden or disabled *with the reason shown* — a 403
is an answer, never a crash and never a silent empty list. Plans need Administer Jira; if you don't
have it, you get locally defined plans instead of an error.

**Fast and small.** Budgets, not hopes: under 60 ms to first paint from cache, p99 under 16 ms per
keystroke at 10,000 issues, zero allocations per frame while scrolling, and a binary under 15 MiB
enforced in CI. See [`docs/PERFORMANCE.md`](docs/PERFORMANCE.md).

**Mouse and keyboard, equally.** Click, double-click, wheel-over-pane, drag to resize, clickable
chips and footer — alongside vim keys, arrows and a command palette. Nothing is reachable only one
way.

**It gets faster the more you use it.** Frecency-ranked pickers, JQL history, saved queries, a local
fuzzy index that needs no round trip, session resume, and hints that teach you the shortcut for
whatever you keep doing the long way. All local; no telemetry, ever.

**Testable without Jira.** The entire suite runs against an in-memory fake and recorded fixtures. No
credentials, no network, no shared sandbox.

## Install

Not yet released. Once `v0.1.0` ships:

```sh
brew install varijkapil13/tap/saral
go install github.com/varijkapil13/saral/cmd/saral@latest
```

## Use as a library

The Jira client is a standalone, documented package with a semver promise — useful even if you never
want a TUI:

```go
import "github.com/varijkapil13/saral/pkg/jira"
```

`pkg/jira` is the port plus a Cloud adapter; `pkg/adf` converts Atlassian Document Format to and from
markdown without losing nodes it doesn't understand. Everything under `internal/` is the application
and carries no compatibility promise.

## Documentation

| Document | What's in it |
|---|---|
| [`docs/BOOTSTRAP.md`](docs/BOOTSTRAP.md) | **Start here** — verify your token, capture fixtures, begin P0.1 |
| [`docs/SCOPE.md`](docs/SCOPE.md) | What's in, what's out, and the non-negotiables |
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Layers, the port, registries, caching, rendering |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Batches and packets, in value order |
| [`docs/PARALLEL.md`](docs/PARALLEL.md) | How several people or agents work at once without conflicts |
| [`docs/UX.md`](docs/UX.md) | Navigation, mouse, progressive mastery, terminal rendering rules |
| [`docs/PERFORMANCE.md`](docs/PERFORMANCE.md) | Budgets and how they're measured |
| [`docs/TESTING.md`](docs/TESTING.md) | The fake, fixtures, golden files, import boundaries |
| [`docs/API-NOTES.md`](docs/API-NOTES.md) | Every Jira API trap we've already hit |
| [`AGENTS.md`](AGENTS.md) | Working agreement for coding agents |

## Contributing

Read [`docs/PARALLEL.md`](docs/PARALLEL.md) and [`CONTRIBUTING.md`](CONTRIBUTING.md). Pick an
unassigned packet from the current batch's milestone, claim it on the issue, and open one PR for it.

## Built with

[Bubble Tea v2](https://github.com/charmbracelet/bubbletea) ·
[Lip Gloss v2](https://github.com/charmbracelet/lipgloss) ·
[Bubbles v2](https://github.com/charmbracelet/bubbles) ·
[bubblezone](https://github.com/lrstanley/bubblezone) · [bbolt](https://github.com/etcd-io/bbolt)

## License

MIT
