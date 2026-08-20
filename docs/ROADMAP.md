# Roadmap

> **Where the work lives.** This file is the plan; the trackable units are **GitHub issues
> [#1–#31](https://github.com/varijkapil13/saral/issues)**, one per packet, grouped into
> [milestones](https://github.com/varijkapil13/saral/milestones) by batch. Each packet below links to
> its issue. Tick the checkbox here in the same PR that closes the issue.
>
> **Picking this up cold:** start with [`docs/BOOTSTRAP.md`](BOOTSTRAP.md) — it takes a fresh clone to
> the first line of code. Then open the lowest-numbered open
> milestone and take any unassigned packet with no unchecked dependency —
> `gh issue list --milestone "Batch 0 — Foundations"`. Claim it by commenting on the issue.
> `AGENTS.md` is the working agreement; `docs/PARALLEL.md` is the definition of done.

Work is organised into **batches** (waves) containing **packets** (single-PR units of work). Batches
are ordered by *when the value arrives*, not by architectural tidiness: the aim is that Saral is
worth opening every day as early as possible, and that everything after that is additive.

Within a batch, packets are independent and can run in parallel. A batch closes when all its packets
merge; the next batch opens then. Batch 0 is the exception — it is strictly serial.

Legend: `[ ]` open · `[~]` in progress · `[x]` merged · **owns** = the only paths that packet may touch.

---

## Batch 0 — Foundations · **serial, blocks everything**

One agent, one PR, reviewed carefully. Nothing else starts until this merges, because it defines the
contracts every other packet codes against.

- [x] **P0.1 — Contracts and kernel** · [#1](https://github.com/varijkapil13/saral/issues/1)
  **owns** `go.mod`, `pkg/jira/{port,types,errors,page}.go`, `pkg/adf/doc.go`,
  `pkg/jira/jiratest/**`, `internal/ui/kernel/**`, `internal/config/**`, `docs/adr/0001*`
  - dependencies added: bubbletea v2, lipgloss v2, bubbles v2, bubblezone
    (bbolt lands with P3.2, which is the packet that imports it — `make check` runs `go mod tidy`,
    so a dependency nothing imports is stripped on the first CI run)
  - `jira.Client` port, domain types, `Page[T]`, typed error taxonomy, `Capabilities`
  - `jiratest`: in-memory fake implementing the full port + an `httptest` server replaying fixtures
  - kernel: root model, view stack, focus, theme tokens, the three registries, help overlay shell
  - config: XDG profiles, token resolvers (keychain / env / command)
  - import-boundary test asserting `pkg/**` never imports `internal/**`

---

## Batch 1 — See your work · parallel ×5

The first batch that produces something usable. Read-only, so it is safe to use against a real
instance from day one.

- [x] **P1.1 — Transport and auth** · [#2](https://github.com/varijkapil13/saral/issues/2) · **owns** `pkg/jira/cloud/{client,auth,retry,paginate}.go`
  Basic auth, `Retry-After` backoff, both paginators, repeated-cursor guard, request coalescing.
- [ ] **P1.2 — Search and JQL** · [#3](https://github.com/varijkapil13/saral/issues/3) · **owns** `pkg/jira/cloud/search.go`, `internal/app/search.go`
  `POST /search/jql` with explicit field sets, `approximate-count`, saved queries.
- [ ] **P1.3 — Capability probe** · [#4](https://github.com/varijkapil13/saral/issues/4) · **owns** `pkg/jira/cloud/caps.go`
  `/mypermissions`, `/configuration`, `/myself`, `/plans` probe, board presence, terminal graphics
  detection. Populates `Capabilities` with a human-readable `Reason` for every negative.
- [ ] **P1.4 — ADF to markdown** · [#5](https://github.com/varijkapil13/saral/issues/5) · **owns** `pkg/adf/render*.go`
  Paragraphs, headings, lists, code, links, marks, panels, mentions, status lozenges. Unknown nodes
  preserved verbatim. Golden-file tests over real captured ADF.
- [ ] **P1.5 — Issue list and detail views** · [#6](https://github.com/varijkapil13/saral/issues/6) · **owns** `internal/ui/{list,issue}/**`
  Virtualized table, incremental cursor paging, `142+` counts, detail pane, comment thread read-only.
- [ ] **P1.6 — First-run onboarding** · [#7](https://github.com/varijkapil13/saral/issues/7) · **owns** `internal/ui/onboarding/**`
  Site, email, token, project picker; writes the profile; explains what the probe found.

## Batch 2 — Change your work · parallel ×4

- [ ] **P2.1 — Markdown to ADF** · [#8](https://github.com/varijkapil13/saral/issues/8) · **owns** `pkg/adf/parse*.go`
  The inverse of P1.4, with round-trip property tests asserting byte-stability on untouched docs.
- [ ] **P2.2 — Schema-driven forms** · [#9](https://github.com/varijkapil13/saral/issues/9) · **owns** `internal/ui/form/**`, `pkg/jira/cloud/meta.go`
  `createmeta` → widgets, required-field validation, `ValidationError` mapped to fields.
- [ ] **P2.3 — Create, edit, transition** · [#10](https://github.com/varijkapil13/saral/issues/10) · **owns** `internal/ui/issue/edit*.go`, `pkg/jira/cloud/issue.go`
  `$EDITOR` handoff for long text, transition picker with per-issue transitions.
- [ ] **P2.4 — Comments CRUD** · [#11](https://github.com/varijkapil13/saral/issues/11) · **owns** `internal/ui/comment/**`, `pkg/jira/cloud/comment.go`

## Batch 3 — Make it a daily driver · parallel ×5

This is the batch that earns the habit. Deliberately ahead of the remaining features.

- [ ] **P3.1 — Command palette** · [#12](https://github.com/varijkapil13/saral/issues/12) · **owns** `internal/ui/palette/**`
  `ctrl+k`, fuzzy over the command registry, frecency ranking, shows the keybinding for what you ran.
- [ ] **P3.2 — Cache and offline** · [#13](https://github.com/varijkapil13/saral/issues/13) · **owns** `internal/store/**`, `go.mod`
  Adds the bbolt dependency, in its own commit ahead of the code that needs it. bbolt buckets, TTLs,
  stale-while-revalidate, cursor-preserving row patching, stale badge.
- [ ] **P3.3 — Mouse** · [#14](https://github.com/varijkapil13/saral/issues/14) · **owns** `internal/ui/widget/zone*.go` + zone wiring in own files
  Click, double-click, wheel-under-pointer, drag-to-resize, clickable chips and footer.
- [ ] **P3.4 — Local fuzzy index** · [#15](https://github.com/varijkapil13/saral/issues/15) · **owns** `internal/app/index.go`
  Instant search over cached issues with no round trip; the thing that makes it feel faster than the web.
- [ ] **P3.5 — Help, hints and theming** · [#16](https://github.com/varijkapil13/saral/issues/16) · **owns** `internal/ui/help/**`, `internal/ui/theme/**`
  Contextual footer showing only valid keys, `?` overlay from the key registry, "you could have
  pressed `s`" hints after a menu path is used repeatedly, light/dark/no-color themes.

## Batch 4 — Attachments · parallel ×3

- [ ] **P4.1 — List and download** · [#17](https://github.com/varijkapil13/saral/issues/17) · **owns** `pkg/jira/cloud/attachment.go`
  Ranged download with progress and resume, temp-file-then-rename.
- [ ] **P4.2 — Upload** · [#18](https://github.com/varijkapil13/saral/issues/18) · same file, second PR (sequential with P4.1)
  Multipart part named `file`, `X-Atlassian-Token: no-check`, multi-file, delete.
- [ ] **P4.3 — Preview** · [#19](https://github.com/varijkapil13/saral/issues/19) · **owns** `internal/ui/attach/**`
  Inline images via kitty/iTerm2 graphics, chafa half-blocks fallback, name+size last resort;
  system handler for everything else.

## Batch 5 — Releases · parallel ×2

- [ ] **P5.1 — Versions** · [#20](https://github.com/varijkapil13/saral/issues/20) · **owns** `pkg/jira/cloud/version.go`, `internal/ui/release/list*.go`
  CRUD, archive, unresolved counts, bulk fix-version assignment from the list.
- [ ] **P5.2 — The release flow** · [#21](https://github.com/varijkapil13/saral/issues/21) · **owns** `internal/ui/release/flow*.go`
  Check `unresolvedIssueCount`, then offer the same three choices the web app does (move to another
  version / strip the version / release anyway), confirm, then `PUT released: true`.

## Batch 6 — Sprints and boards · parallel ×3

- [ ] **P6.1 — Board configuration** · [#22](https://github.com/varijkapil13/saral/issues/22) · **owns** `pkg/jira/cloud/board.go`
  Columns by `statusCategory`, estimation field and rank field read from board config — never guessed.
- [ ] **P6.2 — Sprint lifecycle** · [#23](https://github.com/varijkapil13/saral/issues/23) · **owns** `pkg/jira/cloud/sprint.go`
  `UpdateSprint` over the partial-update `POST`; `StartSprint`/`CompleteSprint` validate state
  locally first. **The raw `PUT` must never be reachable from the port** — it nulls omitted fields.
- [ ] **P6.3 — Board and backlog views** · [#24](https://github.com/varijkapil13/saral/issues/24) · **owns** `internal/ui/board/**`, `internal/ui/backlog/**`
  Column view, drag or key to move between sprint and backlog (50-issue cap per call), rank-aware
  reorder when the board exposes a rank field.

## Batch 7 — Cross-project move · parallel ×1

- [ ] **P7.1 — Move wizard** · [#25](https://github.com/varijkapil13/saral/issues/25) · **owns** `pkg/jira/cloud/bulkmove.go`, `internal/ui/move/**`
  Target project and issue type, status remap, mandatory-field resolution, a confirm screen showing
  the full mapping, submit, then poll the task. Hidden with a reason when `BULK_CHANGE` is absent.

## Batch 8 — Timeline and plans · parallel ×3

- [ ] **P8.1 — Date resolution** · [#26](https://github.com/varijkapil13/saral/issues/26) · **owns** `internal/app/dates.go`
  The cascade that gives every issue a start and an end (see below). Reports provenance per bar.
- [ ] **P8.2 — Timeline view** · [#27](https://github.com/varijkapil13/saral/issues/27) · **owns** `internal/ui/timeline/**`
  Horizontal bars, zoom by day/week/month/quarter, today marker, version and sprint markers,
  milestone diamonds where only one date resolves. Virtualized like every other list.
- [ ] **P8.3 — Plans** · [#28](https://github.com/varijkapil13/saral/issues/28) · **owns** `pkg/jira/cloud/plan.go`, `internal/ui/plan/**`
  Real plans where the token has Administer Jira; locally defined plans (projects/filters + date
  mapping from config) everywhere else, with the reason shown.

## Batch 9 — Ship it · parallel ×3

- [ ] **P9.1 — Release engineering** · [#29](https://github.com/varijkapil13/saral/issues/29) — goreleaser dry-run, Homebrew tap, install script, `v0.1.0`.
- [ ] **P9.2 — Performance gate** · [#30](https://github.com/varijkapil13/saral/issues/30) — benchmarks in CI with `benchstat` regression detection.
- [ ] **P9.3 — README, demo GIF, docs pass.** · [#31](https://github.com/varijkapil13/saral/issues/31)

---

## Timeline date resolution

The timeline is derived from **start and end dates**, not only releases. Per issue, first match wins,
and the UI shows which source a bar came from so a wrong-looking bar is diagnosable:

| # | Start | End | Notes |
|---|---|---|---|
| 1 | field from `[profiles.x.timeline].start` | field from `.end` | explicit config always wins |
| 2 | `Target start` (Advanced Roadmaps) | `Target end` | resolved by **name** via `/rest/api/3/field`, never a hardcoded `customfield_*` |
| 3 | `Start date` custom field | `duedate` | the common non-Premium shape |
| 4 | sprint `startDate` | sprint `endDate` | when the issue is in a sprint |
| 5 | `created` | `fixVersion.releaseDate` | last resort; renders as a faded bar |
| 6 | whichever single date exists | — | milestone diamond, not a bar |

Parents roll up to the min/max of their children when they have no dates of their own, and the
rollup is drawn distinctly from a real date range.

## Later, deliberately not now

- **Confluence.** Arrives as `pkg/confluence` behind its own port. Note that Confluence storage
  format is not Jira's ADF, so `pkg/adf` does not carry over.
- **Jira Data Center.** A second adapter under `pkg/jira/dc`. No bulk-move and no plans API there.
- **Release-note generation.** No API exists; out of scope by decision.
- **Live push updates.** Webhooks require a Connect/OAuth app and a public URL. The optional
  per-view poller in P3.2 is the supported equivalent.
