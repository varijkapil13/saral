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

## Batch 1.5 — Corrections · **PC.1 serial, then parallel ×4, then PC.6 serial** · blocks Batch 2

Batch 1 shipped and left the project asserting several things that are not true. None of them is a
feature and none was worth stopping Batch 1 for; all of them are load-bearing for **Batch 2, which is
where Saral starts writing to Jira**. A tool that reads can be wrong and merely unhelpful. A tool that
writes and is wrong about which project it is in, or about which fields it actually fetched, damages
someone's ticket.

The claims being made good on: that CI keeps tests off the network; that the layer diagram is
enforced; that `Capabilities` gives a reason for every negative; that an `Issue` means what its zero
values say; that the session knows its project; that the fixture server replays the right shape; that
the number keys have exactly one owner; and — the largest of them, found while doing PC.6 and settled
on [#55](https://github.com/varijkapil13/saral/issues/55) — that the shipped binary can talk to a Jira
site at all.

**PC.1 lands first and alone** — it amends the port, which `docs/PARALLEL.md` makes a serial change
because it unblocks or blocks everyone. PC.2 to PC.5 run in parallel behind it. **PC.6 lands last and
alone**, because it narrows what every view asks of the port and holds the composition root, which
PC.2 and PC.3 also edit.

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
- [x] **PC.2 — One owner for the number keys** · [#49](https://github.com/varijkapil13/saral/issues/49) · **owns** `internal/ui/kernel/**`, `internal/ui/list/**`, `internal/app/search.go`, `internal/config/**`, `cmd/saral/main.go`, `docs/{UX,ARCHITECTURE,ROADMAP}.md`
  Three claimants on `1`–`9`, settled by the owner on the issue: **the digits are contextual.** A
  bare `1`–`9` in a root view runs the saved query bound to it; a view is reached with `g` and its
  digit from anywhere. The kernel *buffers* that `g` rather than forwarding it, so the `gg` and `ge`
  that two views already hardcode keep working and no view is left holding half a gesture the kernel
  finished. The slot allocation is written down in `docs/UX.md` and the registry rejects a second
  claim on one, so six later packets no longer each guess.
  Saved queries were built, tested and unreachable, so making the digits alive on day one was the
  other half: a `[[profiles.x.queries]]` schema held to `app.SavedQueries`' own rules rather than a
  second copy of them, `s` in the issue list — and a palette command — to bind the query on screen to
  a key with a confirmation when it is taking one, and the injection through `kernel.Deps` and
  `cmd/saral/main.go`. The owned paths grew to match: the digit dispatch, the footer and the memo key
  are all in `kernel.go`, and saved-query persistence was owned by nobody at all.
- [x] **PC.3 — Session project scope** · [#50](https://github.com/varijkapil13/saral/issues/50) · **owns** `cmd/saral/main.go`, `internal/ui/kernel/kernel.go`, `internal/ui/list/list.go` and their tests, `internal/ui/list/testdata/**`, `docs/ROADMAP.md`
  Onboarding asked which project you work in, validated it, wrote it to the profile, and nothing ever
  read it back: `deps.Project` came only from `--project`, which was itself validated nowhere, so
  `--project "two words"` reached JQL. The flag now overrides the profile rather than replacing it,
  and is held to the rule `internal/config` already enforces on a stored key.
  Half of the issue's premise was stale. `cloud.Capabilities("")` was already right — it skips the
  per-project probes and writes three sentences saying why those answers are unknown. The untested
  denials were the kernel's *zero* `jira.Capabilities`, which is indistinguishable from a token that
  may do nothing, so a session that had probed nothing still answered `open()` with "is not available
  on this site" for a question never asked. The kernel now probes on `Init`, remembers whether a
  probe has answered, and says which of the two it is.
  A project can be changed mid-session with `kernel.SetProject`, which is as far into the gesture as
  the kernel goes — the palette command belongs to P3.1. The kernel re-probes under a sequence
  number so two switches cannot land out of order, tells every view including the roots parked off
  screen, keeps the project being left answering until the new answers arrive, and names the scope in
  the header. A view already open when a switch takes its capability away stays on screen and lets
  its own actions refuse with the capability's `Reason`, per `docs/UX.md`. The list follows a switch
  only while the search on screen is still the one it derived from the project, so a query the user
  ran is never silently discarded.
- [x] **PC.4 — Make the test rules enforceable** · [#33](https://github.com/varijkapil13/saral/issues/33), [#35](https://github.com/varijkapil13/saral/issues/35) · **owns** `.github/workflows/ci.yml`, `internal/arch/**`
  `AGENTS.md` and `docs/TESTING.md` both said CI fails a test that opens a non-loopback connection,
  and it did not. Now it does: the race suite runs inside a network namespace with only loopback up,
  behind a warm-up that compiles the test binaries while the network is still there, and the step
  proves the namespace isolates before it trusts it. `internal/arch` already enforced five of the six
  rules the layer diagram implies — P1.2 landed the `internal/app` one — so what was left there was
  three documents miscounting them and a rule table that could hold a rule unable to fire. Both docs
  now describe the mechanism instead of the honour system.
- [x] **PC.5 — Fixture gaps** · [#34](https://github.com/varijkapil13/saral/issues/34) · **owns** `pkg/jira/jiratest/{fixtures/**,server.go,fixtures_test.go,server_test.go}`, `docs/{API-NOTES,ROADMAP}.md`
  `GET /task/{id}` replayed the bulk-move body, which is the wrong shape and would let an adapter
  decode it wrongly and still pass. It now serves `task_{enqueued,running,complete,failed}.json`,
  four states so a later packet needing a fifth does not conflict on the shared manifest. The
  `createmeta` issue-type list page turned out to be done already — `createmeta_issuetypes.json`
  landed with the localisation fixes in #41, after #34 was filed. Auditing the rest of the routes
  found one more: the paged `versions.json` was served at `/project/{key}/versions`, which answers a
  bare array, so the route moved to the paged `/project/{key}/version` the capture script already
  used.
- [x] **PC.6 — Make the Cloud adapter constructible, and construct it** · [#55](https://github.com/varijkapil13/saral/issues/55), [#52](https://github.com/varijkapil13/saral/issues/52) · **owns** `pkg/jira/roles.go`, `pkg/jira/port.go` (package doc), `pkg/jira/cloud/{me,field,assert}.go` and their tests, `pkg/jira/jiratest/jiratest.go` (assertions), `internal/ui/kernel/view.go`, `internal/ui/onboarding/{onboarding,commands}.go` and their tests, `internal/app/search.go`, `internal/ui/{issue,form,comment}/*cmds.go` (parameter types), `cmd/saral/main.go`, `internal/arch/**`, `docs/{ARCHITECTURE,ROADMAP,TESTING}.md`
  Nothing in the shipped binary ever built a Cloud client, and it could not have: `pkg/jira/cloud`
  implemented 12 of the port's 34 methods, so `*cloud.Client` was not a `jira.Client` and nothing that
  wanted one — `kernel.Deps.Jira`, `onboarding.Connector`, `app.NewSearch` — could hold it. The only
  type in the tree that satisfied the port was `jiratest.Fake`. Everything Batches 1 and 2 shipped
  worked, against the fake.
  Settled on #55 as **role interfaces**, generalising `app.Counter`'s existing "a client that cannot
  make it should not have to pretend". `jira.Client` is untouched and stays the whole port; callers
  now take a role named for their job — `Prober`, `Identifier`, `Searcher`, `FieldCatalogue`,
  `SchemaReader`, `IssueWriter`, `Mover`, `CommentReader`, `Commenter` — and `Deps.Jira` takes
  `SessionClient`, the union of exactly what the views in this build call. Later batches widen that
  union as they land the adapter methods their own views need, which is additive.
  Two of the thirteen methods in the union were missing, not one. `Me` was known. `Fields` was not:
  `app.Search` has called it since P1.2 to resolve a field named in configuration to this site's ID
  for it, and no packet owned the adapter half. Both are implemented here rather than stubbed.
  Both adapters now assert what they satisfy in a non-test file, and `internal/arch` fails an adapter
  package under `pkg/jira/**` that never says. That absence is why a package implementing a minority
  of the port passed CI, lint, race and a cross-build for two whole batches.
  `cmd/saral` registers the connector unconditionally — a first run is exactly the path with no token
  — and resolves the profile's token under a 20s bound when there is one. A resolution failure is not
  fatal: `deps.Jira` stays nil, the program opens, and the resolver's own sentence reaches the status
  line through `kernel.StatusMsg`, applied to the model before the program starts because the alt
  screen wipes anything printed ahead of it. No probe runs before the first frame; PC.3's `Init`
  probe does it once a client exists.
  Verification then found the thing the role assertions cannot catch: two types satisfying one
  interface and disagreeing about the answer. `cloud.Me` refuses a 200 that names nobody, because
  onboarding reads a success there as proof the three fields go together; `jiratest.Fake.Me` returned
  it as an account, so the guard was unreachable from a suite that runs on the fake. The fake now
  refuses the same state — `WithMe(jira.User{})` is how a test asks for it — and `fakeDefaultMe`
  carries the avatar `cloud.Me` populates. `pkg/jira/cloud/conformance_test.go` is the durable half:
  one table of cases run over both adapters through `jira.Identifier`, so the next divergence in `Me`
  fails on the adapter that has it. Generalising that to the rest of the port is
  [#74](https://github.com/varijkapil13/saral/issues/74).

## Batch 2 — Change your work · parallel ×4

**Gated on Batch 1.5.** Do not open a Batch 2 packet while the corrections milestone has an open
issue. This is the first batch that mutates a real instance, and every one of PC.1, PC.3 and PC.5 is
a thing Batch 2 would otherwise get quietly wrong.

- [x] **P2.1 — Markdown to ADF** · [#8](https://github.com/varijkapil13/saral/issues/8) · **owns** `pkg/adf/parse*.go` and their tests, `pkg/adf/testdata/**` (new files only), `docs/API-NOTES.md`
  The inverse of P1.4. `ParseMarkdownInto` reconciles edited markdown against the document it was
  rendered from and reuses the original node for every block nobody touched, which is what makes an
  untouched document come back byte-identical — markdown alone carries no account id, no lozenge
  colour and none of an unknown node's attributes. Landed ahead of the Batch 1.5 gate on purpose:
  PC.1, PC.3 and PC.5 are all outside `pkg/adf`.
- [x] **P2.2 — Schema-driven forms** · [#9](https://github.com/varijkapil13/saral/issues/9) · **owns** `internal/ui/form/**`, `pkg/jira/cloud/meta.go`, its blank import in `internal/ui/views.go`, `docs/{API-NOTES,ROADMAP}.md`
  `createmeta` → widgets, required-field validation, `ValidationError` mapped to fields. The screen is
  read through the pair that replaced the deprecated endpoint — the issue types a project offers, then
  one type's fields — both paginated, and cached per project and issue type for 24h. Every widget is
  chosen from the schema and never from a display name, and a field Jira will not let a create set is
  kept off the form with the reason attached. Long text goes through `ParseMarkdownInto`, so an edit to
  one paragraph does not rewrite the mentions in another. The project comes from `Deps.Project`, which
  is what PC.3 makes non-empty. Two port gaps found and left as issues rather than widened into:
  no way to list a project's issue types or to search users ([#69](https://github.com/varijkapil13/saral/issues/69)),
  and no slot for the default value createmeta states ([#68](https://github.com/varijkapil13/saral/issues/68)).
- [x] **P2.3 — Create, edit, transition** · [#10](https://github.com/varijkapil13/saral/issues/10) · **owns** `internal/ui/issue/edit*.go`, `pkg/jira/cloud/issue.go`, `docs/API-NOTES.md`, plus the two lines each in `internal/ui/issue/keys.go` and `internal/ui/issue/issue.go` that hang the new keys and the palette's messages off the detail pane
  `$EDITOR` handoff for long text, transition picker with per-issue transitions.
  Needs PC.1: an edit must send only fields it actually fetched, or it blanks the rest. The editor
  builds its patch from `Issue.Requested` and refuses a field outside it, saying so on the row.
  Create landed as the adapter method only — the create *form* is P2.2's schema-driven form, which is
  what will call it.
- [x] **P2.4 — Comments CRUD** · [#11](https://github.com/varijkapil13/saral/issues/11) · **owns** `internal/ui/comment/**`, `pkg/jira/cloud/comment.go`, its blank import line in `internal/ui/views.go`, `docs/API-NOTES.md`
  A virtualized thread on one issue, with an editor for writing and editing a comment and a named
  confirmation for deleting one. A new comment is parsed with `adf.ParseMarkdown`; an edit goes
  through `adf.ParseMarkdownInto`, which is the only way a mention keeps its account id. `EditComment`
  reads the comment before writing it so that a restriction the port cannot carry is echoed back
  rather than dropped. Unsent text is kept on disk per issue and per comment being edited.
  The thread is reached by being pushed with an issue; the detail pane cannot push it yet
  ([#67](https://github.com/varijkapil13/saral/issues/67)).

## Batch 3 — Make it a daily driver · parallel ×5

This is the batch that earns the habit. Deliberately ahead of the remaining features.

- [x] **W0-b — The kernel seams this batch codes against** · **owns** `internal/ui/kernel/**`,
  `internal/ui/keys_test.go`, the `Keys:` lines in the five `register.go` files, the `internal/ui`
  import rule in `internal/arch`, `docs/{UX,ARCHITECTURE,ROADMAP}.md`
  Four of the five packets below need something from the kernel, so all of it lands once and the
  kernel is then closed for the batch. `ctrl+k` pushes the palette instead of switching root view and
  reaches it from a view that is taking typing; `kernel.RunCommandMsg` is the one way a command runs,
  so the deps it gets are the kernel's, the capability check happens once, the palette closes itself,
  and a `CommandRanMsg` says what ran; `kernel.Command` grows `Keys`, spelt the way the view's footer
  spells them; and `mouse = false` disables the zone manager, so a view's markers stop being written
  into frames nothing scans.
  **The cache interface was withdrawn from this packet.** It was frozen with no implementation and no
  consumer, and verification found the shape already wrong for one of the two packets meant to use
  it: P3.4 needs to know when the cache changed, and `Get`/`Put`/`Each`/`Purge` offers no generation,
  no watch and no message, so an index built from `Each` goes stale the moment anything writes. Three
  interfaces have already shipped here that nobody implemented. P3.2 defines this one alongside the
  code that exercises it.
- [ ] **P3.1 — Command palette** · [#12](https://github.com/varijkapil13/saral/issues/12) · **owns** `internal/ui/palette/**`
  `ctrl+k`, fuzzy over the command registry, frecency ranking, shows the keybinding for what you ran.
  Wires up `app.SavedQuery`, whose number-key binding PC.2 settles.
- [ ] **P3.2 — Cache and offline** · [#13](https://github.com/varijkapil13/saral/issues/13) · **owns** `internal/store/**`, `internal/app/cache.go`, `go.mod`, `cmd/saral/main.go` and `internal/ui/kernel/view.go` (opening the cache and adding the one `Deps` field that carries it — nothing else)
  Adds the bbolt dependency, in its own commit ahead of the code that needs it. bbolt buckets, TTLs,
  stale-while-revalidate, cursor-preserving row patching, stale badge. Row patching is the other
  consumer of PC.1's field-presence answer. **Adds the `internal/store` must-not-import-`internal/ui`
  rule to `internal/arch` in the same PR** — PC.4 adds its sibling and cannot add this one, because
  the package does not exist yet.
  The dependency landed first and on its own, alongside the smallest `internal/store` that keeps it —
  opening the file, closing it, naming buckets — and the import rule. CI runs `go mod tidy` before
  anything else and that strips a `require` line nothing imports, so the package is what makes the
  dependency real. The cache is what is left: `internal/app/cache.go` declaring the interface —
  declared here because a view may not import the store — the bbolt implementation of it, the
  `kernel.Deps` field that carries it, and `build()` opening it from `config.CacheDir()` and the
  profile. **Interface and implementation land in the same PR**, and the interface answers to what
  P3.4 needs to read as well as to what a view needs: knowing that the cache changed, not only what
  is in it. A session with nowhere to cache carries a nil one and every caller copes.
- [ ] **P3.3 — Mouse** · [#14](https://github.com/varijkapil13/saral/issues/14) · **owns** `internal/ui/widget/zone*.go` + zone wiring in own files
  Click, double-click, wheel-under-pointer, drag-to-resize, clickable chips and footer.
- [ ] **P3.4 — Local fuzzy index** · [#15](https://github.com/varijkapil13/saral/issues/15) · **owns** `internal/app/index.go` · **after P3.2**
  Instant search over cached issues with no round trip; the thing that makes it feel faster than the web.
  It reads through the cache P3.2 defines, so it follows P3.2 rather than running beside it: two
  packets agreeing out of band on the kinds, the key format and the value codec is how the shape
  comes out wrong.
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
