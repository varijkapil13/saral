# Saral

**Jira in your terminal, made simple.**

*Saral* (सरल) is Hindi for simple, straightforward, plain — the opposite of what happens to a tool
when every team bolts another field onto it. That is the whole idea: Jira's data, none of Jira's web
app.

> **Demo.** The demo is committed as a **tape**, not as a picture: [`demo.tape`](demo.tape) holds the
> sequence, the terminal size, the theme and the typing speeds, so anyone can re-record it the day the
> UI moves. [`docs/DEMO.md`](docs/DEMO.md) is how — `vhs demo.tape` — and what to check before
> committing the output. `demo.gif` is not in the repo yet.

<!-- Once someone has recorded it, replace the note above with:
![Saral: the issue list, an issue transitioned, the command palette and the timeline](demo.gif)
-->

> **Status.** Batches 0 to 8 have merged: fifteen view packages, seven of them root views on the
> `g`-and-a-digit slots. What is left is [Batch 9](https://github.com/varijkapil13/saral/milestones) —
> release engineering, the performance gate and this documentation pass — and then `v0.1.0`.
> [`docs/ROADMAP.md`](docs/ROADMAP.md) has every packet and links each one to its issue.

## What it does

Fifteen views, and the seven that are places rather than panes sit on `g1`–`g7`: **issues**, **board**,
**backlog**, **sprints**, **releases**, **timeline** and **plans**. The rest are reached from the thing
they are about — an issue, the files on it, a transition, a filter, a move.

- **Tickets** — JQL search, read, create, edit, transition. Forms are generated from your instance's
  own `createmeta`, so they are right on any site.
- **Comments** — add, edit, delete, in `$EDITOR`, with faithful markdown ⇄ ADF conversion.
- **Attachments** — upload, resumable ranged download with progress, delete, and inline image preview
  in terminals that speak the kitty or iTerm2 protocol, half-blocks where they do not.
- **Releases** — manage versions *and* release them, with the unresolved-issue decision the web app
  makes you take: release anyway, move the open issues to another version, or strip the version off
  them.
- **Sprints and boards** — columns from your board configuration, backlog, create/start/complete
  sprints, move issues between sprint and backlog, rank-aware reordering.
- **Cross-project move** — a wizard over Jira's bulk-move API with status and field remapping, which
  polls the task and reports a move that stopped half way.
- **Timeline** — built from real start and end dates through a seven-rule cascade that says which rule
  each bar came from, with sprint and version markers.
- **Plans** — the real thing if your token has Administer Jira; otherwise plans built from the
  session's project and your saved queries, with the reason the site refused shown beside them.

## What the web app does not do

- **The whole keyboard, and the whole mouse.** Every action is reachable three ways — a key, the
  command palette (`ctrl+k`), and the pointer. Click a status cell to filter by it, drag the divider
  between the description and the fields, right-click for the menu of what applies to what is in front
  of you.
- **A footer that only shows keys that work right now.** Hints come from the key registry and move
  with the state the view is actually in, so nothing on the row is a stroke that answers with a
  refusal.
- **First paint before the network.** Views read the cache in their constructor, draw, and revalidate
  behind the frame. A refresh patches rows without moving your cursor, your scroll offset or your
  filter.
- **A 403 is an answer.** Capabilities are probed once per site and project, kept between runs and
  re-asked on every start. A feature your token lacks is hidden or disabled *with the site's own
  sentence saying why* — never a crash, never a silently empty list.
- **Nothing about your Jira is assumed.** No project keys, custom field IDs, statuses or permissions
  are baked in. Field IDs resolve by name against the catalogue, board columns come from board
  configuration, statuses come from the project's own workflows, and required fields from
  `createmeta`.
- **Nothing leaves the machine but Jira API calls.** Frecency tables, JQL, saved queries and the
  fuzzy index over cached issues are local files. No telemetry, ever.
- **Budgets, not hopes.** Cold start to first paint under **250 ms**, failed in CI; a keystroke to a
  frame in a **mean under 16 ms** at 10,000 rows; **one** allocation a frame while scrolling — the
  frame string and nothing behind it; a stripped binary under **15 MiB**, failed in CI. The numbers,
  and which of them a build actually fails on, are in [`docs/PERFORMANCE.md`](docs/PERFORMANCE.md).

## Install

Not released yet. `v0.1.0` is the last thing in Batch 9. Once it ships, from the
[Homebrew tap](https://github.com/varijkapil13/homebrew-tap) or from source:

```sh
brew install varijkapil13/tap/saral
go install github.com/varijkapil13/saral/cmd/saral@latest
```

Each release also carries `saral_<version>_<os>_<arch>.tar.gz` for darwin and linux on amd64 and
arm64, with a `checksums.txt` beside them. Building from a clone needs only Go:

```sh
git clone https://github.com/varijkapil13/saral && cd saral
make build && ./saral
```

## Point it at a site

`saral` with nothing configured opens the setup view. It asks for four things — the site, the account
email the API token belongs to, where the token lives, and a project to open in — checks them against
the site before saving, and writes the profile. Re-running it over an existing profile replaces only
those four and leaves your theme, timeline field names and saved queries alone.
[Get an API token here.](https://id.atlassian.com/manage-profile/security/api-tokens)

The file it writes is `~/.config/saral/config.toml`, and it is meant to stay safe to hand somebody:
**the token is never in it, only where to find it.**

```toml
active = "work"

[profiles.work]
site    = "example.atlassian.net"
email   = "you@example.com"
project = "ENG"                          # several capabilities are answered per project
theme   = "dark"                         # or light, no-color; omit to follow the terminal
token   = { keychain = "saral:work" }    # or { env = "JIRA_TOKEN" } / { command = ["pass", "jira"] }

[profiles.work.timeline]
start = ["Target start", "Start date"]   # field names, resolved to IDs at runtime
end   = ["Target end", "Due date"]

[[profiles.work.queries]]
name = "Blockers"
jql  = "priority = Highest AND resolution = EMPTY ORDER BY updated DESC"
key  = 2                                 # the number key that runs it; omit for none
```

Then:

```sh
saral                     # the active profile
saral --profile personal  # another one
saral --project OPS       # scope this run to a project
saral ENG-142             # open an issue
saral https://example.atlassian.net/browse/ENG-142
saral board               # open a view by name
saral --poll 30s          # re-read the focused view, pausing when Jira rate-limits
saral --mouse=false       # off all the way down, for terminal text selection
saral version
```

`SARAL_CONFIG_DIR` and `SARAL_CACHE_DIR` move both directories, which is how a throwaway profile is
kept out of the way of a real one.

## Use as a library

The Jira client is a standalone, documented package with a semver promise — useful even if you never
want a TUI:

```go
import "github.com/varijkapil13/saral/pkg/jira"
```

`pkg/jira` is the port — 42 methods in domain terms, none of them shaped like the REST call
underneath — plus a Cloud adapter and an in-memory fake. `pkg/adf` converts Atlassian Document Format
to and from markdown without losing nodes it does not understand. Everything under `internal/` is the
application and carries no compatibility promise.

## Documentation

| Document | What's in it |
|---|---|
| [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) | Layers, the port, the registries, caching, rendering, the error taxonomy |
| [`docs/UX.md`](docs/UX.md) | Navigation, the keymap, mouse, progressive mastery, terminal rendering rules |
| [`docs/API-NOTES.md`](docs/API-NOTES.md) | Every Jira API trap already found, and how each one is known |
| [`docs/TESTING.md`](docs/TESTING.md) | The fake, the fixtures, golden files, conformance across adapters |
| [`docs/PERFORMANCE.md`](docs/PERFORMANCE.md) | Budgets, and which of them a build fails on |
| [`docs/SCOPE.md`](docs/SCOPE.md) | What's in, what's out, and the non-negotiables |
| [`docs/ROADMAP.md`](docs/ROADMAP.md) | Batches and packets, in value order |
| [`docs/PARALLEL.md`](docs/PARALLEL.md) | How several people or agents work at once without conflicts |
| [`docs/DEMO.md`](docs/DEMO.md) | How to record `demo.tape`, and what to check first |
| [`docs/BOOTSTRAP.md`](docs/BOOTSTRAP.md) | Verifying a token and capturing fixtures, from a fresh clone |
| [`AGENTS.md`](AGENTS.md) | Working agreement for coding agents |

## Contributing

Read [`CONTRIBUTING.md`](CONTRIBUTING.md) and [`docs/PARALLEL.md`](docs/PARALLEL.md). Pick an
unassigned packet from the current batch's milestone, claim it on the issue, and open one PR for it.

**You do not need a Jira account to build or test.** The whole suite runs against an in-memory fake
and recorded fixtures, with no network: CI runs the race suite inside a namespace with only loopback
up, so a test that reaches a real host fails the build.

## Built with

[Bubble Tea v2](https://github.com/charmbracelet/bubbletea) ·
[Lip Gloss v2](https://github.com/charmbracelet/lipgloss) ·
[Bubbles v2](https://github.com/charmbracelet/bubbles) ·
[bubblezone v2](https://github.com/lrstanley/bubblezone) · [bbolt](https://github.com/etcd-io/bbolt)

## License

MIT
