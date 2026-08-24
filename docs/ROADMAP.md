# Roadmap

> **Where the work lives.** This file is the plan; the trackable units are **GitHub
> [issues](https://github.com/varijkapil13/saral/issues)**, grouped into
> [milestones](https://github.com/varijkapil13/saral/milestones) by batch. Each packet below links to
> its issue — usually one apiece, occasionally two where a shared file forces them into one PR. Tick
> the checkbox here in the same PR that closes the issue.
>
> **Picking this up cold:** start with [`docs/BOOTSTRAP.md`](BOOTSTRAP.md) — it takes a fresh clone to
> the first line of code. Then open the **lowest-numbered open milestone** and take any unassigned
> packet with no unchecked dependency — today that is
> `gh issue list --milestone "Batch 1.5 — Corrections"`. Claim it by commenting on the issue.
> `AGENTS.md` is the working agreement; `docs/PARALLEL.md` is the definition of done.
>
> **A batch does not open until the one before it closes**, and Batch 1.5 exists precisely so that
> nobody starts Batch 2 on top of a claim that is not true. Lowest-numbered means 1.5 before 2.

Work is organised into **batches** (waves) containing **packets** (single-PR units of work). Batches
are ordered by *when the value arrives*, not by architectural tidiness: the aim is that Saral is
worth opening every day as early as possible, and that everything after that is additive.

Within a batch, packets are independent and can run in parallel. A batch closes when all its packets
merge; the next batch opens then. Batch 0 is the exception — it is strictly serial.

Legend: `[ ]` open · `[~]` in progress · `[x]` merged · **owns** = the only paths that packet may
touch. `P<batch>.<n>` is a feature packet; `PC.<n>` is a correction packet, which pays off something
the project already claims to be true (see Batch 1.5).

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
- [x] **P1.2 — Search and JQL** · [#3](https://github.com/varijkapil13/saral/issues/3) · **owns** `pkg/jira/cloud/search.go`, `internal/app/search.go`
  `POST /search/jql` with explicit field sets, `approximate-count`, saved queries.
- [x] **P1.3 — Capability probe** · [#4](https://github.com/varijkapil13/saral/issues/4) · **owns** `pkg/jira/cloud/caps.go`
  `/mypermissions`, `/configuration`, `/myself`, `/plans` probe, board presence, terminal graphics
  detection. Populates `Capabilities` with a human-readable `Reason` for every negative.
- [x] **P1.4 — ADF to markdown** · [#5](https://github.com/varijkapil13/saral/issues/5) · **owns** `pkg/adf/render*.go`
  Paragraphs, headings, lists, code, links, marks, panels, mentions, status lozenges. Unknown nodes
  preserved verbatim. Golden-file tests over real captured ADF.
- [x] **P1.5 — Issue list and detail views** · [#6](https://github.com/varijkapil13/saral/issues/6) · **owns** `internal/ui/{list,issue}/**`
  Virtualized table, incremental cursor paging, `142+` counts, detail pane, comment thread read-only.
- [x] **P1.6 — First-run onboarding** · [#7](https://github.com/varijkapil13/saral/issues/7) · **owns** `internal/ui/onboarding/**`
  Site, email, token, project picker; writes the profile; explains what the probe found.

## Batch 1.5 — Corrections · **PC.1 serial, then parallel ×4** · blocks Batch 2

Batch 1 shipped and left the project asserting several things that are not true. None of them is a
feature and none was worth stopping Batch 1 for; all of them are load-bearing for **Batch 2, which is
where Saral starts writing to Jira**. A tool that reads can be wrong and merely unhelpful. A tool that
writes and is wrong about which project it is in, or about which fields it actually fetched, damages
someone's ticket.

The claims being made good on: that CI keeps tests off the network; that the layer diagram is
enforced; that `Capabilities` gives a reason for every negative; that an `Issue` means what its zero
values say; that the session knows its project; that the fixture server replays the right shape; and
that the number keys have exactly one owner.

**PC.1 lands first and alone** — it amends the port, which `docs/PARALLEL.md` makes a serial change
because it unblocks or blocks everyone. The other four run in parallel behind it.

- [x] **PC.1 — Port amendments** · [#46](https://github.com/varijkapil13/saral/issues/46), [#37](https://github.com/varijkapil13/saral/issues/37) · **owns** `pkg/jira/{port,types}.go`, `pkg/jira/cloud/{caps,search}.go`, `pkg/jira/jiratest/{jiratest,fake}.go`, `internal/ui/onboarding/render.go`, `docs/{ARCHITECTURE,API-NOTES}.md`
  Two additive amendments, one PR, because two agents editing the frozen port is the one guaranteed
  conflict. Both landed in `types.go`: `port.go` needed no edit at all, since neither amendment
  touches a method signature. `Capabilities` gained `TimeZoneReason` and `Zone()`, so a probe that
  could not establish the account's zone says which of its three ways it failed instead of leaving
  `TimeZone` nil and rendering every date in UTC with nothing on screen to explain it. `Issue` gained
  `Requested`, a `FieldMask` naming the fields it was read with, so that P2.3's fetch-edit-PUT cycle
  cannot blank a field nobody touched. The owned paths grew by the fake's field masking and the one
  line in onboarding that names a timezone, which are the only consumers either amendment has:
  without them the packet adds two things nobody uses.
- [ ] **PC.2 — One owner for the number keys** · [#49](https://github.com/varijkapil13/saral/issues/49) · **owns** `internal/ui/kernel/{view,keys}.go`, `internal/ui/list/register.go`, `internal/app/search.go`, `docs/UX.md`
  Three claimants on `1`–`9`: kernel view slots, the six root views `docs/UX.md` promises, and
  `SavedQuery.Slot`, which P1.2 built and tested and nothing calls. Pick one, give the others a
  prefix, and write down the slot allocation so six later packets do not each guess.
  **A product decision, not a defect — it wants a human answer.**
- [ ] **PC.3 — Session project scope** · [#50](https://github.com/varijkapil13/saral/issues/50) · **owns** `cmd/saral/main.go`, `internal/ui/kernel/kernel.go`, `internal/ui/list/list.go`
  Onboarding asks which project you work in, validates it, writes it to the profile, and nothing ever
  reads it back: `deps.Project` comes only from `--project`. So the capability probe resolves
  per-project permissions against an empty key and the list opens unscoped over the whole site.
  Includes deciding what no-project-at-all means and how a project is changed mid-session.
- [x] **PC.4 — Make the test rules enforceable** · [#33](https://github.com/varijkapil13/saral/issues/33), [#35](https://github.com/varijkapil13/saral/issues/35) · **owns** `.github/workflows/ci.yml`, `internal/arch/**`
  `AGENTS.md` and `docs/TESTING.md` both said CI fails a test that opens a non-loopback connection,
  and it did not. Now it does: the race suite runs inside a network namespace with only loopback up,
  behind a warm-up that compiles the test binaries while the network is still there, and the step
  proves the namespace isolates before it trusts it. `internal/arch` already enforced five of the six
  rules the layer diagram implies — P1.2 landed the `internal/app` one — so what was left there was
  three documents miscounting them and a rule table that could hold a rule unable to fire. Both docs
  now describe the mechanism instead of the honour system.
- [ ] **PC.5 — Fixture gaps** · [#34](https://github.com/varijkapil13/saral/issues/34) · **owns** `pkg/jira/jiratest/{fixtures/**,server.go}`
  `GET /task/{id}` replays the bulk-move body, which is the wrong shape and would let an adapter
  decode it wrongly and still pass; the `createmeta` issue-type list page is missing entirely. Landed
  here rather than in P2.2 and P7.1 because the fixture tree is shared and both would edit the same
  hardcoded manifest, two batches apart.

## Batch 2 — Change your work · parallel ×4

**Gated on Batch 1.5.** Do not open a Batch 2 packet while the corrections milestone has an open
issue. This is the first batch that mutates a real instance, and every one of PC.1, PC.3 and PC.5 is
a thing Batch 2 would otherwise get quietly wrong.

- [ ] **P2.1 — Markdown to ADF** · [#8](https://github.com/varijkapil13/saral/issues/8) · **owns** `pkg/adf/parse*.go`
  The inverse of P1.4, with round-trip property tests asserting byte-stability on untouched docs.
- [ ] **P2.2 — Schema-driven forms** · [#9](https://github.com/varijkapil13/saral/issues/9) · **owns** `internal/ui/form/**`, `pkg/jira/cloud/meta.go`
  `createmeta` → widgets, required-field validation, `ValidationError` mapped to fields.
  Needs PC.3 (`createmeta` is keyed by project and issue type) and PC.5 (the issue-type list page).
- [ ] **P2.3 — Create, edit, transition** · [#10](https://github.com/varijkapil13/saral/issues/10) · **owns** `internal/ui/issue/edit*.go`, `pkg/jira/cloud/issue.go`
  `$EDITOR` handoff for long text, transition picker with per-issue transitions.
  Needs PC.1: an edit must send only fields it actually fetched, or it blanks the rest.
- [ ] **P2.4 — Comments CRUD** · [#11](https://github.com/varijkapil13/saral/issues/11) · **owns** `internal/ui/comment/**`, `pkg/jira/cloud/comment.go`

## Batch 3 — Make it a daily driver · parallel ×5

This is the batch that earns the habit. Deliberately ahead of the remaining features.

- [ ] **P3.1 — Command palette** · [#12](https://github.com/varijkapil13/saral/issues/12) · **owns** `internal/ui/palette/**`
  `ctrl+k`, fuzzy over the command registry, frecency ranking, shows the keybinding for what you ran.
  Wires up `app.SavedQuery`, whose number-key binding PC.2 settles.
- [ ] **P3.2 — Cache and offline** · [#13](https://github.com/varijkapil13/saral/issues/13) · **owns** `internal/store/**`, `go.mod`
  Adds the bbolt dependency, in its own commit ahead of the code that needs it. bbolt buckets, TTLs,
  stale-while-revalidate, cursor-preserving row patching, stale badge. Row patching is the other
  consumer of PC.1's field-presence answer. **Adds the `internal/store` must-not-import-`internal/ui`
  rule to `internal/arch` in the same PR** — PC.4 adds its sibling and cannot add this one, because
  the package does not exist yet.
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
  **Every part of a board config is optional and the absences are not exotic.** A Kanban board sends
  no estimation object at all, which is why `BoardConfig.Estimation` is a pointer; a board may expose
  no rank field, so drag-to-reorder has to be a capability and not an assumption; and a board may be
  ordered by priority rather than by rank. Match everything by id or `untranslatedName`, never by
  display name — on a German instance the field, status and priority names all arrive translated, and
  `clauseNames` follows the translation too.
- [ ] **P6.2 — Sprint lifecycle** · [#23](https://github.com/varijkapil13/saral/issues/23) · **owns** `pkg/jira/cloud/sprint.go`
  `UpdateSprint` over the partial-update `POST`; `StartSprint`/`CompleteSprint` validate state
  locally first. **The raw `PUT` must never be reachable from the port** — it nulls omitted fields.
- [ ] **P6.3 — Board and backlog views** · [#24](https://github.com/varijkapil13/saral/issues/24) · **owns** `internal/ui/board/**`, `internal/ui/backlog/**`
  Column view, drag or key to move between sprint and backlog (50-issue cap per call), rank-aware
  reorder when the board exposes a rank field. Takes the footer slot PC.2 assigns it; the kernel
  rejects a duplicate at startup, so this cannot be settled by guessing.

## Batch 7 — Cross-project move · parallel ×1

- [ ] **P7.1 — Move wizard** · [#25](https://github.com/varijkapil13/saral/issues/25) · **owns** `pkg/jira/cloud/bulkmove.go`, `internal/ui/move/**`
  Target project and issue type, status remap, mandatory-field resolution, a confirm screen showing
  the full mapping, submit, then poll the task. Hidden with a reason when `BULK_CHANGE` is absent.
  Polls `/bulk/queue/{taskId}`, not `/task/{taskId}` — different shapes, both fixtured by PC.5.

## Batch 8 — Timeline and plans · parallel ×3

- [ ] **P8.1 — Date resolution** · [#26](https://github.com/varijkapil13/saral/issues/26) · **owns** `internal/app/dates.go`
  The cascade that gives every issue a start and an end (see below). Reports provenance per bar.
  Rule 4 needs [#38](https://github.com/varijkapil13/saral/issues/38) first: an issue's sprint value
  carries `{id, name}` and no dates, and the timeline has no board id to look them up with.
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
