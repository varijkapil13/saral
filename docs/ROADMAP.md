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
  **Every search this view offered narrowed by who you are.** Found against a real site: a project of
  nineteen issues, three of them the owner's, and no gesture anywhere that could ask for the other
  sixteen — the palette's three searches all carry `currentUser()`, and P3.3's facets filter rows
  already loaded rather than widening the query. There is now a fourth search with no predicate at all (`a`, *Every issue in this project*),
  the default falls back to it once when nothing in the project is assigned to the account rather than
  opening on an empty screen, and `e` shows the JQL actually running and takes an edited one, which is
  also what clicking the line that names it does. A `search` value composes the predicate and the
  ordering apart, because `scoped()` had nothing to put an `AND` between for the one search that is
  only an ordering.
  `r` and `R` now say what came back — *nothing has changed, still 19 issues* as much as *2 new, 1
  changed* — and the summary line keeps the time the rows last came from the site. Pressing `r` on a
  query whose answer had not moved was the other half of the same afternoon: correct behaviour,
  reported as nothing, read as a broken key.
  **What this did not fix:** every one of these searches, and the default, is only meaningful for a
  token that is a person. `currentUser()` for a service account resolves to an account nobody assigns
  anything to, so *My issues* is reliably empty and the fallback fires on every project — which is the
  right behaviour and the wrong default. Filed as
  [#102](https://github.com/varijkapil13/saral/issues/102).
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
  The thread is reached by being pushed with an issue. The detail pane does that on `c`
  ([#67](https://github.com/varijkapil13/saral/issues/67)), which is what makes the whole packet
  reachable: until then nothing in the program handed the view an issue, so writing, editing and
  deleting a comment were all dead code.

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
- [x] **P3.1 — Command palette** · [#12](https://github.com/varijkapil13/saral/issues/12) · **owns** `internal/ui/palette/**` including its `testdata/**`, this packet's one blank import line in `internal/ui/views.go`, `docs/{UX,ROADMAP}.md`
  `ctrl+k`, fuzzy over the command registry *and* over every issue the cache already holds, frecency
  ranking, the keybinding for what you ran, and the hints P3.5a moved here.
  **This row's `owns` line named only the package.** It is corrected above to the import line every
  view packet adds — the one shared edit `docs/ARCHITECTURE.md` allows, one line, alphabetical — and
  to the two docs this packet had to correct.
  **The claim about `app.SavedQuery` was stale and is gone.** PC.2 settled the digits and W0-b left the
  kernel owning `BindQueryMsg`, `Deps.Saved` and the footer's account of what a digit runs; the palette
  neither binds a query nor reads one. What the row did not say is what this packet actually owns:
  filtering on capability, because the registry deliberately does none and letting it would make the
  registry its own client, and the frecency table, which is the same `(id, count, lastUsed)` data the
  hints count from.
  Reachability was already W0-b's: registering under `kernel.PaletteViewID` with `Slot: 0` is the whole
  of it. `enter` sends `kernel.RunCommand(id)` and nothing else — no `Run` call, no `Pop`, no
  capability check before running — so a search reaches `Run` with the kernel's live deps and is scoped
  to the project the session is on now. There is a test for exactly that, because a stale copy searches
  the whole site and looks like it worked.
  It registers **no keys**. `RegisterKeys` records a view's resting state and the palette has none: it
  takes typing from the moment it opens, so `kernel.KeyReporter` answers for each of its states — one
  with a command to run, one with an issue to open, one with nothing — and a registry entry would
  never be read. That also leaves `internal/ui/livekeys_test.go`, which is P3.5a's file, correct as it
  stands.
  The frecency table is a file of the palette's own under `config.CacheDir()`, beside where
  `internal/ui/comment` keeps drafts. `Deps.Cache` was the other candidate and cannot hold it:
  `app.Cache` is `Rows`/`PutRows`/`Forget`/`EachIssue`/`Generation`, so a `(id, count, lastUsed)` table
  there means widening P3.2's interface, and it is nil exactly when a first run would most want to
  start learning. `config.toml` was the third and must stay safe to share. A session with nowhere to
  write ranks in memory for as long as it runs.
  **Not built here:** the "Switch project" command from the PC.3 note on
  [#12](https://github.com/varijkapil13/saral/issues/12). It needs a source of project keys — a fetch,
  or a walk over the cached issues — and a second pane with a frecency table of its own, which is a
  packet beside this one rather than a corner of it. It was
  [#87](https://github.com/varijkapil13/saral/issues/87), and it is built: a pane of its own in this
  package, its keys from a narrow read over recent issues, its frecency the same table code over a
  file of its own, and `kernel.SetProject` finally has a caller.
  **There is one fuzzy scorer, and it is `app.Pattern`.** This packet wrote its own because P3.4's
  ([#86](https://github.com/varijkapil13/saral/pull/86)) did not exist on any commit it could branch
  from, and `internal/app` must not import `internal/ui`, so neither packet could consume the other.
  `app.Pattern` is on `main` now, `internal/ui/palette/score.go` and its tests are gone, and
  [#88](https://github.com/varijkapil13/saral/issues/88) is closed. What stayed here is the arithmetic
  that is the palette's own and not a scorer's: score the title, the group and the ID, penalise the two
  that are not on the row, take the best. The penalty is calibrated rather than carried over, because a
  command ID is dotted — almost any short pattern is a prefix of one, and `app.Pattern` pays a prefix
  twice what it pays a word start further into a title, so unpenalised the text nobody can see would
  order the rows they can. One ranking moved: `iss` now offers *My issues* above *Edit this issue*,
  which is where its title matches rather than where its ID does.
  **`app.Index` has a consumer, and it is the second half of this list.** P3.4 filed
  [#85](https://github.com/varijkapil13/saral/issues/85) against itself because nothing constructed
  one, which left "instant search over cached issues" shipping as no behaviour at all. Anything typed
  is now ranked against the commands and against every issue on disk, commands first, one cursor
  through both, `enter` running or opening depending on the row it is over and the footer saying which.
  No prefix and no second keystroke: `docs/UX.md` asks the palette to be everything and fuzzy, and
  typing `PROJ-142` matches no command anyway. The rows are honest about being copies — the age of
  each, badged past the age the cache stops calling an issue current, and *(no title stored)* where a
  narrow read never asked for one, which PC.1's field mask makes different from an empty title. A nil
  `Deps.Cache` is a first run, another copy holding the file or an unwritable home: the half is absent,
  the filter does not offer to search issues in the first place, and the empty state says which of the
  two it is. Bounded to twenty hits, so a keystroke over a full cache is 1.2 ms of the 16 ms budget at
  66 allocations.
- [x] **P3.2 — Cache and offline** · [#13](https://github.com/varijkapil13/saral/issues/13) · **owns** `internal/store/**`, `internal/app/cache.go`, `cmd/saral/main.go`, `internal/ui/kernel/view.go` (the one `Deps` field that carries the cache — nothing else), `internal/ui/list/**` including its `testdata/**`, `internal/arch/imports_test.go`, `docs/{ARCHITECTURE,TESTING,ROADMAP}.md`
  The bbolt dependency and the smallest `internal/store` that keeps it landed first and on their own,
  in [#78](https://github.com/varijkapil13/saral/pull/78), with the `internal/store`
  must-not-import-`internal/ui` rule; W0-b added the sibling rule the other way round, so
  `internal/arch` needed nothing here beyond confirming both. CI runs `go mod tidy` before anything
  else and that strips a `require` line nothing imports, so the package is what made the dependency
  real, and `go.mod` is no longer this packet's to touch.
  The cache is the rest: `internal/app/cache.go` declaring `app.Cache` — declared there because a view
  may not import the store — with the bbolt-backed implementation beside it, the `kernel.Deps` field
  that carries it, and `build()` opening it from `config.CacheDir()` and the profile.
  **Row patching was already built:** `internal/ui/list`'s `patchedMsg`/`reload`/`patch` have preserved
  the cursor across a refresh since P1.5, so this packet pointed them at the cache instead of writing a
  second one. What it did add there is the read in `list.New` — a first paint happens in the
  constructor, because `kernel.FirstPaint` never calls `Init` — the stale badge, the paging that
  follows rows off disk which carry no cursor, and the optional per-view poller.
  `app.MergeIssue` is the other consumer of PC.1's field-presence answer: issues are stored once each
  and a narrow refresh merges into the copy already held, so a list row cannot unassign an issue a
  wider read filled in. The interface also answers what P3.4 needs and not only what a view needs —
  `EachIssue` for the corpus and `Generation()` for knowing the corpus moved. A session with nowhere to
  cache carries a nil one and every caller copes.
  The clause in `docs/ARCHITECTURE.md` saying the capability probe "is cached in the store" was false
  and was removed here, because persisting it honestly needed the kernel. K4 below did it, and the
  clause is back with the code under it.
- [x] **P3.3 — Mouse** · [#14](https://github.com/varijkapil13/saral/issues/14) · **owns** `internal/ui/widget/**`, the zone and click wiring plus tests and `testdata/**` in `internal/ui/{list,issue,form,comment,onboarding}`, `docs/{UX,ARCHITECTURE,ROADMAP}.md`
  **The `owns` line above is a correction.** It used to read `internal/ui/widget/zone*.go` + zone
  wiring in own files, and `internal/ui/widget/` did not exist — so on day one this packet owned
  nothing that renders, while every shipped view already marked zones and handled clicks.
  **Most of this row was already built, and the audit is on the issue.** Click-to-select, the wheel
  and the footer click were shipped by P1.5, P2.x and W0-b. What was actually missing: the
  double-click, the chips, and the wheel over two panes that could not scroll at all.
  `internal/ui/widget` is the copy-paste in all five view packages made one thing: `Zoner` (the prefix
  plus `Mark`/`MarkLines`/`Hit`, where a nil or disabled manager needs no branch of its own),
  `Clicks` (the double-click, timed against `Deps.Now` — `tea.MouseClickMsg` has neither a click count
  nor a timestamp, and the "second click on the already-selected row" rule every view had reached for
  fires on two deliberate clicks a minute apart), `Window` (the scrolled window) and `Drag`.
  Clickable status, type and assignee cells narrow the list to that value and clear it on a second
  click, composing with `/`, named in a line under the rows and reachable from the palette because
  `esc` in a root view never reaches the view. **Labels are clickable nowhere:** no view that can
  filter draws them, so `docs/UX.md` says that rather than promising it.
  `internal/ui/issue`'s edit pane and move pane now scroll: both drew every line and let the frame
  clip, so the last field was unreachable by wheel *or* cursor on a short terminal.
  **Two gestures were cut and filed:** drag-to-resize is [#75](https://github.com/varijkapil13/saral/issues/75)
  — there is no divider to drag until a view has two panes, so `widget.Drag` ships tested and bound to
  nothing for P6.3 — and the right-click context menu was
  [#76](https://github.com/varijkapil13/saral/issues/76), cut here because `kernel.Command` cannot say
  what a command applies to. K3 below built it without teaching it to: the focused view's `Acts`
  already answer that question, and the kernel already reads them for the footer.
- [x] **P3.4 — Local fuzzy index** · [#15](https://github.com/varijkapil13/saral/issues/15) · **owns** `internal/app/{index,match}.go`, their tests and benchmarks, `docs/{PERFORMANCE,ROADMAP}.md` · **after P3.2**
  **This row used to promise "instant search over cached issues with no round trip".** Half of that
  already shipped in P1.5: `internal/ui/list` filters as you type with no round trip and no
  allocation — `refilter` reuses the index slice it appends into, and
  `BenchmarkFilterKeystroke10k` guards it. What was missing is **ranking**, and **reach past the rows
  on screen** — the second being the cache, which is why this followed P3.2 rather than running
  beside it.
  So this packet is two things. `internal/app/match.go` is the scorer: `app.Pattern`, a subsequence
  match over **any string**, ranked whole candidate → prefix → word start → the initials of several
  words → inside a word → scattered, ties going to the earlier match and then the shorter candidate.
  It folds case without copying either side and allocates nothing, which is what let Batch 3 keep its
  decision not to add `github.com/sahilm/fuzzy`: that API materialises a `[]string` of every target
  and a `[]int` per hit, against a 16 ms keystroke budget.
  `internal/app/index.go` is the corpus: `app.Index` over `IssueCorpus`, the two-method narrow
  interface `app.Cache` already satisfies, rebuilt when `Generation()` moves and not otherwise. A
  walk is a whole rebuild, because a counter says that something changed and not what — 795 µs and
  one allocation at ten thousand issues, so the honest answer was to rebuild rather than invent a
  delta the cache cannot express. `Hit` carries `HasSummary`, because PC.1's mask means a narrow read
  stores an issue whose title nothing asked for, and `StoredAt`, so a caller badges age against
  `Deps.Now` and the index needs no clock.
  **The list's filter was deliberately left alone**, and is not any more:
  [#84](https://github.com/varijkapil13/saral/issues/84) ranks it with `app.Pattern`, scoring the key,
  the summary and each of the row's other fields on its own rather than one concatenated haystack, and
  typing lands the cursor on the best match while every other rebuild keeps the row it was on. The
  keystroke path still allocates nothing of its own: the scored run and the index slice are both the
  model's.
  **Its consumer is P3.1** ([#12](https://github.com/varijkapil13/saral/issues/12)): the palette
  ranks commands with `app.Pattern`, and the signature was published on #12 and #15 before this
  packet was finished so it could be coded against. `app.Index` has a view as well —
  [#85](https://github.com/varijkapil13/saral/issues/85) reached it from the palette, which searches
  every cached issue inside the keystroke; [#62](https://github.com/varijkapil13/saral/issues/62) is
  the key jump, whose command-line half landed in K5 below and whose in-session gesture is still
  undecided — it reuses that index rather than opening a second one.
- [x] **P3.5a — The contextual footer, the `?` overlay and the theme switch** · [#16](https://github.com/varijkapil13/saral/issues/16) · **owns** `internal/ui/kernel/{theme,chrome_test,theme_test}.go`, the chrome functions in `internal/ui/kernel/kernel.go`, the `KeyReporter` lines in `internal/ui/kernel/{view,registry}.go`, `internal/ui/kernel/testdata/**`, the `keys*.go` and `testdata/keys.golden` of `internal/ui/{list,issue,form,comment,onboarding}`, `internal/ui/livekeys_test.go`, `docs/{UX,ARCHITECTURE,ROADMAP}.md`
  Contextual footer showing only valid keys, `?` overlay from the key registry, light/dark/no-color
  themes switchable while the program runs.
  **This row used to claim `internal/ui/help/**` and `internal/ui/theme/**`.** Neither directory
  exists and neither was created: the footer and the `?` overlay are chrome functions in `kernel.go`,
  and the themes are `internal/ui/kernel/theme.go`. Lifting theming out of the kernel would touch ~25
  files across six packages and is a refactor packet, not this one.
  What was missing was not the rendering. `RegisterKeys` is init-time and refuses a second call, so
  `KeysFor` returned one static set however the view's state changed, and six views were advertising
  keys that do nothing in the state the user was in. `kernel.KeyReporter` is how a view answers for
  the state it is in, and all six implement it. `ThemeMsg` existed and nothing sent it; there are now
  four palette commands and the choice is written back without dropping the rest of the profile —
  which onboarding then dropped on its next run ([#63](https://github.com/varijkapil13/saral/issues/63)),
  fixed in K6 below. Colour stepping down to 256 and 16 was
  never missing — bubbletea's renderer does it — so that was a doc correction.
  **The hints bullet moved to P3.1** ([#12](https://github.com/varijkapil13/saral/issues/12)): "after
  you reach an action through the palette three times, the status line notes its key" needs the
  counter, the call site and the frecency table that packet already owns, and W0-b landed
  `CommandRanMsg{ID, Keys}` as the signal. A second counter in the chrome would be a second answer.
- [x] **P3.6 — An inventory of what you can do to the thing in front of you** · [#96](https://github.com/varijkapil13/saral/issues/96), [#16](https://github.com/varijkapil13/saral/issues/16) · **owns** `internal/ui/kernel/{view,kernel,keys,external}.go`, `internal/ui/kernel/{chrome_test,kernel_test,mouse_test,footer_test,external_test}.go` and `internal/ui/kernel/testdata/**`, `internal/ui/{keys_test,livekeys_test,footer_test}.go` and `internal/ui/testdata/**`, the `keys*.go` and `testdata/keys.golden` of `internal/ui/{list,issue,comment,form,onboarding,palette}`, `docs/{UX,ARCHITECTURE,ROADMAP}.md`
  The footer was over capacity and dropped the ways out. Seven reserved view slots cost 81 columns
  against the 80 this program documents as its minimum, so `? help`, `esc back` and `ctrl+k commands`
  were past the ellipsis — and at 100 columns the help component overflowed by one column and the
  kernel threw the whole row away, which a committed golden had encoded as twelve bytes. The row is now
  three cells — the root you are in, what can be done here, and the globals — given up in a fixed order
  that never reaches the globals: actions fold into a `+N` from the right, then the root cell goes, then
  the actions lose their descriptions.
  `KeySet` gains `Acts`, filled in all seven key scopes with the actions those views already had, terse,
  most-used first; the motions moved to `?`, which now leads with the actions, spelt out, and lists
  every key exactly once. **No new keys** — a pure re-partition. Every entry is a zone and a click on one is fed back through the
  kernel's own key handling as the binding's first stroke, so key, palette and pointer are one
  implementation.
  **Two claims the pane made were false, in opposite directions.** `ctrl+f` and `ctrl+b` were
  advertised on the issue detail pane and bound by nothing — `m.key()` never matched its own
  PageUp/PageDown, which worked only because the widget happened to bind the same strokes — while the
  viewport's bare `f`, `b`, `u` and `d` worked and were named nowhere at all. The keymap now says what
  the widget answers to.
  Also landed for P3.7–P3.11: `kernel.{Copy,OpenURL,IssueURL}`. An OSC 52 write cannot be confirmed, so
  `Copy` names what it copied rather than trusting a caller to; and `Deps.Site` may carry a scheme, a
  port or a context path, so `IssueURL` parses rather than concatenating and refuses what is not a site.

## First-run corrections · found by running against a site

The first end-to-end run — the real Cloud adapter against a Jira on loopback, driving the real kernel
and views — found the spine works and turned up defects that would dead-end a testing session. They
are filed and fixed here rather than in a batch, because Batch 3 is closed and none of them is a
feature. Each is one PR, small and tested, in the paths it names.

- [x] **F1 — A failed search must not look like a hang** · [#94](https://github.com/varijkapil13/saral/issues/94) · **owns** `internal/ui/list/**` including its `testdata/**`, the transport sentence in `pkg/jira/cloud/client.go`, `docs/{UX,ROADMAP}.md`
  `appendEmpty` drew "Searching…" whenever `!m.loaded` and `failed()` never touched `loaded`, so a
  wrong project key, a bad JQL, a dead host and a rate limit were one screen that looked hung — and
  the status line carrying the reason was wiped by the next keypress. An empty pane now names which
  of five kinds of empty it is and keeps naming it; the failed one carries the reason in the error's
  own words, the JQL it was asked about, and the key that runs it again. Both ways in are the same
  line: a first load, and any retarget, which drops the rows before the new search is issued.
  The transport sentence lost the URL it repeated after the method and path `TransportError.Op`
  already carries, which is what truncated it before "connection refused". No error type and no
  classification moved; the cause still satisfies `errors.Is`.
  Adjacent, in the same path: a filter accepted with `enter` had the count reading `1 of 3` as its
  only trace and no way off it, since `esc` in a root view belongs to the kernel. It is named under
  the rows the way a clicked cell is, `ctrl+g` clears it, and the palette carries the same gesture.
- [x] **F2 — What a real Jira answers, and what the fixtures invented** ·
  [#98](https://github.com/varijkapil13/saral/issues/98),
  [#99](https://github.com/varijkapil13/saral/issues/99),
  [#100](https://github.com/varijkapil13/saral/issues/100),
  [#101](https://github.com/varijkapil13/saral/issues/101) · **owns**
  `pkg/jira/{types,errors}.go`, `pkg/jira/cloud/{client,paginate,field}.go`,
  `pkg/jira/jiratest/{fixtures/**,server.go}` and the tests for all of those, `docs/ROADMAP.md`
  A disposable Cloud site built to cover every shape this client claims to handle — three project
  types, five boards, sprints in all three states, 15 custom field types and an en_US↔de_DE flip —
  measured four things the fixtures had invented their way around. Every one of them got through
  because the invented fixture was self-consistent, so the fixture shapes are corrected here as well.
  A field name resolved through two spellings and a site sends three: `untranslatedName` is neither
  the English display name nor the translated one, so a profile naming what an English screen shows
  matched nothing on a German site. `jira.ResolveField` reaches the third spelling and says whether a
  name is unknown or shared, because two distinct fields can answer to one string and returning
  either is a value read from a field nobody named. `FieldByName` keeps its signature and refuses an
  ambiguous name instead of taking the first.
  The Agile API writes its sentence into `errors` under a **URL parameter** name, and routing-level
  failures answer RFC 7807 instead of Jira's own envelope: `parseErrorBody` reads all three, a 404
  now carries the site's own words, and nothing keyed like a parameter becomes a message on an input
  the user cannot act on. No classification moved — the status stays the authority.
  `/board/{id}/issue` and `/backlog` name their array `issues`, which the offset decoder did not read,
  so a board read decoded to no rows and reported no error. Three envelopes exist, one per shape, and
  there is now a fixture and a terminating walk for each.

## Rendering wave · found by reading an issue against a site

An issue's description came out as raw markdown — `##`, `**`, `[text](url)`, backticks, fences, pipe
tables — because `internal/ui/issue` writes `adf.MarkdownWith` straight into its pager. That markdown
is a serialisation for editing: it backs the `$EDITOR` handoff and P2.1's byte-stable round trip,
`pkg/adf` is public API that must not import a UI library, and it deliberately does not escape prose.
It was never a display format. Handing it to a markdown renderer would re-parse text that was never
markdown and would arrive after the information a display needs is already gone — by then an error
panel and a plain quote are the same node — and `chroma` alone is 5.11 MiB stripped against 4.57 MiB
of headroom under the 15 MiB binary gate. So the display renderer is written here instead.

- [x] **R1 — The display renderer** · **owns** `internal/ui/richtext/**` including its `testdata/**`,
  `docs/PERFORMANCE.md`, this row
  `internal/ui/richtext` walks an `adf.Doc` straight to styled terminal lines, importing only
  `pkg/adf`, `charm.land/lipgloss/v2` and `x/ansi` — never the kernel, because the caller passes the
  theme in as tokens, which is also what keeps the goldens a property of the document and lets the
  issue and comment views share one renderer. It covers the 31 node types and 11 mark types a real
  site stored, so a panel keeps its own marker and colour rather than becoming a quote, a status
  lozenge keeps the colour enum it arrives with, an unknown node stays visibly unknown, and a media
  node stays a placeholder with the id P4.3 will resolve.
  **Styling happens after the break, never before.** `ansi.Wrap` does not re-open an SGR sequence on
  a continuation line: a bold run wrapped at 20 columns opens bold on line 0, leaves lines 1 and 2
  with no sequence at all and puts the reset on line 3, so a pane showing a window into the middle of
  it draws the run unstyled and one starting at the reset opens with a stray reset. Every line is
  built from independently painted spans and a test asserts that no line inherits or leaks a style.
  Lines are not padded to `Width` — padding belongs to the pane — and `Widths` is reported so a pane
  can clamp panning without measuring. Code is neither wrapped nor cut and a grid wraps inside its
  columns, so the two constructs allowed past the width are the only ones that go past it.
- [x] **R2 — The custom fields the detail read never asked for** · **owns** `internal/app/search.go`,
  `internal/app/search_test.go`, this row
  `app.DetailProjection()` asked for twenty platform fields and none of the site's own, so story
  points, the sprint, the epic link and the acceptance criteria were missing from the answer before
  any view had the chance to draw them. `Projection.Custom` expands into every `Field.Custom` ID in
  the catalogue `Search` already fetches and caches, and the answer carries `app.FieldLabels` — field
  ID to the name this site displays — because `customfield_13401` is not a name a person can read and
  the label is translated, so it has to be resolved at runtime rather than written down.
  Deliberately not `jira.FieldsNavigable`: a wildcard returns a value per field per issue with
  nothing to label any of them by, and it reports itself as a read of everything, which is how a
  narrow cached row starts looking wide. `Issue.Requested` still names exactly what was asked for.
- [x] **R3 — One thread and one composer, in whatever box it is given** · **owns**
  `internal/ui/comment/**` including its `testdata/**`, this row
  The owner asked for comments beside the issue rather than over it, and for writing one not to take
  the screen away from what it answers. Both are the same requirement: one `*comment.Model`, embedded
  as a child by a sidebar and pushed whole-screen by the same instance, so there is one fetch, one
  draft, one cursor and one composer with the text still in it. `Thread` therefore returns `*Model`
  rather than `kernel.View`, and `Init` reads nothing until an issue is named, because a pane builds
  the thread before it knows which issue it is showing.
  **One layout, not two.** The thread is on top and the composer under it, at
  `clamp(1+rows, 3, max(3, boxH/2))` — a ceiling of half the box, so a comment is never written on a
  screen that hides what it is replying to, at any size. The composer spends one row on chrome and
  not two (which comment, and the keys that finish it, on one line that degrades through terser
  forms) so that `1+rows` is the draft's own rows rather than one short of them. `rows` comes from
  the widget's `DynamicHeight`, because a textarea soft-wraps on words and dividing cells by the
  width is a row out at every width but one.
  Bodies are drawn through R1 instead of `adf.MarkdownWith`, which is what put `**` and `## ` on
  screen, and the delete confirmation quotes its opening words through `richtext.Summary`.
  **A line wider than the box is cut where it says so, and panned to.** R1 leaves code and grid rows
  at their own width by design — wrapping code corrupts what a reader is about to copy — so at 34
  columns most code blocks are wider than the pane. The pane marks the cut and binds `←/h` and `→/l`
  to reach the rest; only the over-wide lines slide, because sliding the rest would take the author
  and the date of the comment being read off the left edge. Truncating silently is the one answer
  that was not available.
- [x] **R4 — The two-pane issue view** · **owns** `internal/ui/issue/{issue,render,keys,cmds,register,comments,layout,sidebar,fields}.go`,
  `internal/ui/issue/**_test.go`, `internal/ui/issue/testdata/**`, this row
  The fields were at the bottom of a single scrolling column, under the description and above the
  thread, so an issue screen answered *what is this about* and buried *what is it*. The pane is three
  regions now: the description on the left, and the fields and the comment thread stacked in a sidebar
  beside it at 90 columns and up. Below that there is room for one at a time and `tab` brings up the
  next — the gesture P3.5a deleted from `docs/UX.md` because no view had two panes.
  **The thread in the sidebar is the comment view itself**, sized with its own `SizeMsg` for its box,
  and `C` hands the kernel that same model for the whole screen: the footer and `?` are then the
  thread's own keys, and `esc` comes back to the issue with the thread still on the comment it was
  left on and the draft still in it.
  It draws what the screen was missing: every custom field the site defines, named from
  `app.FieldLabels` because `customfield_13401` is not a name and the label is translated; `Resolved`,
  which R2 fetched and nothing drew; the status category on the identity line, because "Building" says
  nothing about whether that counts as started; and the parent, subtasks and links as `KEY status
  summary` rather than the comma-joined bare keys they were, since an `IssueRef` already carries both.
  A field the read never asked for says so rather than drawing blank, which is what PC.1's mask is for.
  **The description goes through R1's renderer**, which is the report this wave started from.
  **Every region's leftmost column is its gutter**: the focus rail and the scrollbar in one, so the
  pane spends a column instead of three title bars, and in `no-color` the thumb's position carries
  what a hue cannot. And because the renderer never wraps code, `h` and `l` pan: a realistic Go
  signature is 79 cells and the widest description box at 120 columns is 78, so a code block is cut at
  every width the sidebar leaves and something had to reach the rest of it.
- [x] **R5 — Let the reader choose the split** · **owns** `internal/ui/issue/{layout,split}.go`,
  `internal/ui/issue/**_test.go`, `internal/ui/issue/testdata/**`, `internal/config/**`, this row
  R4's split was computed and could not be changed — `clamp(bodyW/3, 35, 45)` — so a reader who
  wanted more prose, or a wider sidebar for one long custom field, had no way to ask.
  **`widget.Drag` finally has something to drag.** P3.3 shipped the press-move-release machine bound
  to nothing because no view had two panes; the column between the description and the sidebar is
  marked as a zone and is now what it was waiting for. The press decides what is being held, so a
  release outside the column still lands on the divider, and a resize, a key, a view switch or a
  press elsewhere cancels the gesture rather than applying a delta measured against a pane that has
  gone. That last one is not theoretical: the help overlay swallows every mouse message while it is
  up, so a release really can go missing.
  `<`, `>` and `=` are the keyboard route and three palette commands are the third, because a drag is
  mouse-only and principle 3 asks for all three. They are on the `?` overlay and not the footer row:
  the row names what can be done to the *issue*, and below 90 columns there is no boundary for them
  to move — where they say so rather than doing nothing, as they do at 90 itself, where the two
  floors meet and leave exactly one legal split.
  **The floors are measured, not chosen**: 53 cells of prose, below which the same paragraph loses
  about two words a line, and 34 for the sidebar, below which a label and its value stop fitting side
  by side. `layout_test.go` measures both rather than trusting them.
  **The ratio is per machine, in `ui.toml` under the cache directory, and deliberately not in the
  profile.** A pane width belongs to the terminal it was chosen in rather than to a Jira account,
  `config.toml` is a file people hand each other, and at the time onboarding rebuilt a profile from a
  zero value and dropped what it did not collect ([#63](https://github.com/varijkapil13/saral/issues/63),
  since fixed in K6 below) — so a field added there would have been lost the next time anybody
  re-checked a token. The split was kept out of its way either way, which `internal/config` asserts by
  running that same overwrite over a profile and finding the split still there.
  **A drag is a motion stream, so it was measured.** A frame drawn while the boundary is held costs
  what a frame at rest costs — two allocations at 120×40, unchanged. A motion that moves the boundary
  nowhere costs no render. A motion that does move it is a resize of two regions and costs slightly
  less than one: 767 allocations against 807, because the panes have a width they have never been
  rendered at and lines are what a width means. Closes
  [#75](https://github.com/varijkapil13/saral/issues/75).

## Filtering by a person · the port amendment first

The owner, after a session with the issue list: *"there is no easy way to filter by person. I do not
want to write JQL queries — this should be accessible on the UI."* The port had `Me` and nothing else
about accounts, so a person could not be chosen at all, and the only person-ish filter narrowed
already-fetched rows by **display name**. The picker is a later packet; the contract it codes against
lands first, because `pkg/jira/port.go` blocks everyone while it is open.

- [x] **FP.1 — The port amendment a filter picker needs** ·
  [#69](https://github.com/varijkapil13/saral/issues/69) · `contract` · **owns**
  `pkg/jira/{port,types,roles}.go`, `pkg/jira/cloud/{people,vocab,caps,assert}.go`,
  `pkg/jira/jiratest/**` including `fixtures/**`, the tests for all of those,
  `docs/{API-NOTES,ARCHITECTURE,ROADMAP}.md`
  **One amendment covering every facet a filter narrows by, rather than five.** Five methods appended
  to the port — `FindPeople`, `People`, `IssueTypeStatuses`, `Priorities`, `Labels` — plus
  `jira.AccountKind` on `User`, `jira.PeopleQuery`, `jira.IssueTypeStatuses` and `CapPeople`. No
  existing signature moved; `SessionClient` gains `PeopleFinder` and `FilterVocabulary`, which is
  additive, and both adapters plus `pkg/jira/cloud/assert.go` pin the two roles at build time.
  **The wire is not shaped like the rest of the API and the signatures are built around that.**
  `/user/search` answers a **bare JSON array** that neither paginator here reads, 400s without a
  `query` and lists everybody with an empty one. Its matching is undocumented and is neither substring
  nor fuzzy — word prefixes, initials and email tokens — so `PeopleQuery.Match` says in the port's own
  documentation that the caller ranks the answer and never presents Jira's order as its own.
  `/user/bulk` defaults to **ten per page** whatever it is asked for and answers **JSON `null` inside
  `values`** for an id the site does not know, which decodes into a blank row; those are counted for
  the walk and then dropped, because a blank row is worse than an absence.
  `AccountKind` **labels and never filters**: an app account is assigned work like a person, and the
  measured site was ten apps and one human. `IssueTypeStatuses` carries **ids**, per issue type,
  because a name is localised and a team-managed project mints project-scoped statuses reusing the
  stock names. `Labels` cannot be narrowed — the endpoint ignores a `query`, measured byte-identical.
  `CapPeople` probes *Browse users and groups* by **calling the endpoint** rather than by asking
  `mypermissions` for a fifth key: one unrecognised key fails that whole request, so the existing four
  would have gone behind whether a site knows the new one.
  **A conformance table per role, run over both adapters**, because [#74](https://github.com/varijkapil13/saral/issues/74)
  exists and five more methods must not be five more chances to drift: an unknown id is absent rather
  than blank, a `Limit` is a ceiling, a refusal names `CapPeople`, a status is identified by its id,
  and both sites hold two ids under one display name.

- [x] **FP.2 — The filter picker** · **owns** `internal/ui/filter/**` including `testdata/**`,
  `internal/ui/list/**` including `testdata/**`, the blank import in `internal/ui/views.go`,
  `internal/ui/{keys_test,livekeys_test}.go`, `internal/ui/testdata/{footer,overlay}_*.golden`,
  `internal/ui/palette/testdata/session_120x30.golden`, the JQL subset in
  `pkg/jira/jiratest/fake.go` and its tests, `docs/{UX,ROADMAP}.md`
  **The consumer that makes FP.1 real.** `f` in the issue list opens a picker: choose a facet, then
  choose one of the values this site actually holds. All five port methods are called — `FindPeople`,
  `People`, `IssueTypeStatuses`, `Priorities` and `Labels` — along with `PeopleQuery`, `CapPeople` and
  `AccountKind`.
  **A chosen value is a query, not a pass over the rows in hand.** `internal/ui/list/facet.go`
  narrowed loaded rows by **display name**, which could not reach an unfetched issue and matched a
  localised string two statuses on one project can share. That is gone: clicking a cell and choosing
  in the picker are one term model, keyed by id, composed into the JQL and run against the site. Two
  facets AND, two values of one facet OR, `assignee IS EMPTY` for nobody, and the clause is written in
  a fixed facet order so two routes to one filter ask one question and store under one cache key.
  Three of eleven account ids on the measured site carry a colon, so every value is quoted and the
  quoting is tested.
  **The terms are on screen as chips and a click drops one**, because [#105](https://github.com/varijkapil13/saral/issues/105)
  settled that a filter you cannot see is one you cannot escape. `a` is the no-terms state rather than
  a second clear. A project switch takes the terms with it: statuses and types are minted per project.
  **Where the values come from is what makes it fast.** Statuses, types, priorities and labels are
  read once when the facet opens and a keystroke then ranks what is held with `app.Pattern` — no round
  trip, no allocation (`BenchmarkRankValues`, 0 allocs/op over two thousand values). Accounts cannot
  be ranked locally, because Jira's matching is neither substring nor fuzzy and is undocumented, so
  the site is asked once on open and again only when what is held runs thin — never twice for one
  needle, and never at all while the first search returned the whole directory. The assignee search is
  project-scoped so Jira drops the app accounts; the reporter search is not, because a reporter need
  not be assignable, and what it drags in is badged and sunk instead. An account already in force that
  the search does not answer with is drawn back by id through `People`, which is the one way a filter
  on a robot can be taken off again.
  **Version, component and sprint are not offered** and the docs say why: none can be read through
  `jira.SessionClient`, so a row for one would be a facet with nowhere to get its values from.
  **A late answer does not move the cursor.** `shown` holds indices into `all`, and an account search
  landing appends to `all` and sorts it — so reading the row under the cursor *after* that read a
  different value, slid the highlight one row, and left `enter` filtering by somebody the user never
  chose, silently. The row is now named before anything reorders the set and restored by id, which is
  what `docs/UX.md` principle 5 asks for.
  **The fake can run what the picker writes.** `jiratest`'s JQL knew `project`, `key`, `status`,
  `assignee` and `labels` compared with `=`, `IS EMPTY` and `IS NOT EMPTY` joined by `AND` — so a
  clause on a reporter, a type or a priority, and every multi-value `IN`, was refused by the fake and
  could only be tested as a string. It also reads `reporter`, `issuetype`/`type`, `priority`,
  `IN (a, b)`, `currentUser()` on either account field, and an `OR` **inside one pair of brackets**,
  which is the whole of what `Terms.Clause()` composes. Nothing else moved: an unbracketed `OR`, `~`,
  an inequality, `NOT` and a date function are still refused, because a query the fake cannot honour
  must never pass as one that matched everything. Every facet is now asserted on the issues it
  selects rather than on the clause it emits.
  Budgets held: the list's steady scroll is 1 allocation a frame with terms in force, `BenchmarkFrame`
  is unchanged at 297, and the picker is virtualized and memoized — 3 allocations a frame scrolling
  two thousand labels, 94µs from keystroke to frame.

## Kernel · corrections to the root model

K1 and K2 were found while building the comment thread: the kernel said one thing —
`FocusMsg{Focused: false}` — in three situations that mean three different things, and every view had
to guess which it was in. K3 to K6 are the backlog this section grew afterwards, each one a promise a
doc made that the code did not keep. None of them changes the `View`, `Deps` or `Command` contract:
eight views are built against it and it is closed.

- [x] **K1 — A view learns when it is being discarded** · [#124](https://github.com/varijkapil13/saral/issues/124) ·
  **owns** `internal/ui/kernel/**`, the focus and fetch handling of
  `internal/ui/{list,issue,comment,form,filter,onboarding}`, `internal/ui/livekeys_test.go`,
  `docs/{ARCHITECTURE,UX,ROADMAP}.md`
  A view pushed over is still there, a root switched away from is parked and comes back on its digit,
  and a popped view is gone — and one message covered all three. `internal/ui/comment` had cancelled
  its read on blur, so **opening the palette over a loading thread cancelled the load**; the fix was
  to stop cancelling on blur at all, which left every discarded view fetching for an answer nothing
  would draw. `internal/ui/issue`'s detail pane, field editor and transition picker and
  `internal/ui/onboarding` all still had the original bug, and the editor and the picker never
  re-read on coming back, so `ctrl+k` over a loading one left it loading for as long as it was open.
  **`kernel.Closer` is the fourth optional interface and not a fourth shape**, next to `KeyCapturer`,
  `Blocker` and `KeyReporter`. `Blocker` refuses a close; `Closer` is told about the one that
  happened. A call and not a message, because `Update` hands back a `View` the kernel is about to drop
  — so anything a discarded view records there is thrown away with it — and because a message is
  broadcastable, which would let any view tell every other one it was finished.
  **Two paths call it and no others**: the entry a pop takes off, and everything above entry zero
  that a root switch throws away. A parked root is never closed, a project switch discards nothing —
  every view hears `ProjectMsg` and stays — and nothing evicts a view from `live` to rebuild it.
  Quitting discards everything and tells nobody: the process is ending, so every context it would
  cancel dies with it and no command it returned would run.
  **`kernel.Lend` is the push that keeps the view.** The issue pane hands the kernel the very thread
  its sidebar draws, so closing it on `esc` would cancel a read the sidebar is still waiting for. The
  kernel drops a lent entry without closing it and leaves that to the lender, which does it in its own
  `Close`. Six adopters: the thread, the detail pane, the field editor, the transition picker, the
  create form and the filter picker. The issue list and onboarding are listed as exempt with the
  reason — nothing pushes either, so a discard never reaches them — and
  `internal/ui/livekeys_test.go` fails on a seventh view that is in neither half.
  **Necessary and not sufficient**: not cancelling the read only helps if the answer then reaches the
  view that asked, which it did not — see K2 below, which ships in the same pull request because
  without it this packet is a regression.

- [x] **K2 — A view's own answer is delivered to the view that asked for it** ·
  [#125](https://github.com/varijkapil13/saral/issues/125) ·
  **owns** `internal/ui/kernel/**`, the fetch handling of
  `internal/ui/{list,issue,comment,form,filter,onboarding}`, `internal/ui/*_test.go`,
  `docs/{ARCHITECTURE,UX,ROADMAP}.md`
  `Model.route`'s default is `forwardTop`, so a message the kernel does not recognise goes to
  whatever is on top **when it lands** — and a view is blurred by exactly the thing that makes it not
  the top. So every answer arriving while the palette was up was delivered to the palette and
  dropped. Cancelling on blur had been hiding it: the pane cancelled, and on regaining focus
  `!m.loadedIssue` sent it to read again, which recovered the answer. K1 removed the cancel and
  guarded that recovery with `!m.loadingIssue` — a flag only `loadedMsg` and `failedMsg` clear, which
  are the very messages the palette ate. **The pane never asked again.**
  **`kernel.Reply` and `kernel.Addressed` are the fifth optional interface and its command wrapper.**
  A view mints a `kernel.Addr` for itself and wraps its own commands in it; the kernel takes the
  envelope off and delivers to the view holding that address, wherever it is on the stack or parked
  in `live`, and drops it when no address resolves. **Opt-in on purpose**: everything unaddressed
  still goes to the top, which is where a `bubbles` cursor blink and a spinner tick belong, and
  addressing those would blink every input in the program and walk the session under every tick.
  The address is a number rather than the `View` because a view need not be a pointer — onboarding is
  a struct the kernel copies on every `Update` — and `==` on two interfaces holding the same
  non-comparable type panics rather than answering.
  **`To` is a list, most particular first.** The comment thread in the issue pane's sidebar is a
  model inside a view and never an entry, so the kernel cannot deliver to it at all: its answer names
  itself and then the pane, and the kernel takes the first address it can see — the thread while `C`
  has it lent to the whole screen, the pane otherwise, which forwards what it does not recognise.
  **The recovery is gone rather than renamed.** The detail pane no longer re-reads on regaining
  focus and `loadingIssue` is deleted; the editor, the transition picker, the create form and the
  filter picker never had a recovery and no longer need one. Eight adopters, one per key scope, and
  `internal/ui/livekeys_test.go` fails on a ninth view in neither half of the table. The list's poll
  tick is addressed too — `pollArmed` is cleared by the tick and by nothing else, so one eaten by the
  palette stopped the poller for the rest of the session.
  Held to it by `internal/ui/reply_test.go`, which drives the whole program — the kernel, the real
  palette on `ctrl+k` — for each of the list, the detail pane, the field editor, the transition
  picker, the filter picker, the create form and the thread in both the places it is drawn, holding
  each answer back until the palette is up so that the delivery decision is really made with the view
  underneath. Every guard was checked by mutation: removing the kernel's routing, and dropping the
  address from any one view, turns exactly that view's case red.

- [x] **K3 — The right-click menu, built from what the focused view says applies** ·
  [#76](https://github.com/varijkapil13/saral/issues/76) ·
  **owns** `internal/ui/kernel/{menu.go,menu_test.go}`, the mouse, key and chrome hooks in
  `internal/ui/kernel/kernel.go`, `internal/ui/kernel/testdata/menu_*.golden`,
  `internal/ui/{menu_test.go,testdata/menu_120x38.golden}`, `docs/{UX,ROADMAP}.md`
  `docs/UX.md`'s mouse table promised *right-click a row → the actions valid for it* and P3.3 cut it,
  because `kernel.Command` has `Requires` — a capability key — and nothing that says what a command
  applies to. The recommendation on the issue was a `Command.Scope` field plus an adoption in each of
  the five `register.go` files. **That is not what this does.** The kernel already knows the focused
  view and the view already publishes its `Acts`, which is by definition the inventory of what can be
  done to the thing on screen and already moves with the view's state through `KeyReporter`. So the
  menu is the footer's middle cell in full, and **the kernel contract does not move**: no field on
  `Command`, no method on `View`, no registrar swept.
  **Choosing an entry delivers the binding's first stroke** through `handleKey`, which is the rule a
  footer-action click already follows, so key, palette and pointer cannot drift into three
  implementations of one action.
  **Row granularity, and the row is the focused one** — recorded in `docs/UX.md` before any code:
  only the view can turn a coordinate into a row, so a kernel that guessed would offer *delete*
  against the wrong issue. The right-click is forwarded to the focused view first, which is the seam
  for a view to adopt *right-click selects this row*; no view does yet, and that is a per-view
  follow-up rather than a kernel change.
  **A view with no `Acts` opens no menu and says so.** There is no keybinding for the menu and none is
  invented: `g` is the slot prefix, and every entry in the menu is already a key and a palette command.
  **The menu takes the body, like `?`, and not a floating box at the pointer.** Splicing one into the
  view's own lines means cutting strings that carry the zone markers a click is resolved through, and
  half the frame's mouse targets would stop answering — the boundary the overlay already respects.
  Held over the real views rather than a stub: `internal/ui/menu_test.go` opens the menu on every
  registered view and keeps a golden of all seventeen, asserts that every action a view publishes is
  named in it — the row folds what does not fit into a `+N` and the menu does not — and drives
  right-click-then-enter on the issue list end to end. The one view that publishes nothing, the
  attachment pane with nothing attached and no permission to attach, is asserted to explain itself
  instead. Every guard checked by mutation: dropping the right-click branch, the key interception,
  the capturing guard, the forwarded click, the stroke delivery or the footer's own row each turns
  exactly the case that covers it red.

- [x] **K4 — What the token can do, kept between runs** ·
  [#81](https://github.com/varijkapil13/saral/issues/81) ·
  **owns** `internal/app/{cache.go,caps_test.go}`, the capability handling in
  `internal/ui/kernel/kernel.go`, `internal/ui/kernel/caps_test.go`, the capability half of
  `cmd/saral/main_test.go`, `docs/{ARCHITECTURE,UX,ROADMAP}.md`
  `deps.Caps` was the zero `jira.Capabilities` for the whole first frame, so every gated view was
  hidden with nothing to say about why and `saral board` fell through to the first footer slot without
  a word. `app.KindCaps` keeps the last answer per project — `*` for a session scoped to none, which
  is a real answer about the site rather than the absence of one — under a **one-hour TTL**, and
  `kernel.New` installs it before the first frame.
  **A stored answer gates, and does not merely pre-fill.** Pre-filling without gating is exactly what
  the zero value already did. The cost of a stale positive is a request that comes back 403 with the
  site's own sentence, which is the same failure as a permission revoked a second after a live probe
  and one the kernel already survives.
  **Three things make that safe.** `cloud.capsVoid` returns an error rather than an all-negative
  answer, so an expired token, a rate limit or one dropped packet leaves the stored answer standing
  instead of writing five denials to disk where they would outlive the minute that caused them. `Init`
  re-asks unconditionally whatever the entry's age, reaching views through `CapabilitiesMsg` exactly
  as `R` and a project switch do. And only the kernel's own probe is written: a `CapabilitiesMsg` from
  a view is applied and not stored, because onboarding probes the site being set up.
  **Past the TTL the answer is still served, with one line saying when it was last checked**, taken
  down by the probe that settles it and only if it is still the line there — the composition root's
  startup notice lands on the same row.
  **`Graphics` is deliberately not stored** (it is what this terminal can draw, and a stored `kitty`
  answer in a terminal that cannot speak it prints escape bytes over the frame), and `TimeZone` is
  stored by name, with the sentence that says so when this machine cannot load it.
  Also fixes a latent state this made visible: a build whose every view is gated had an empty stack
  until an answer arrived and nothing re-ran the choice, so the frame said no views were registered
  for the rest of the run.

- [x] **K5 — `saral PROJ-142`, and an argument that names nothing** ·
  [#62](https://github.com/varijkapil13/saral/issues/62) ·
  **owns** `internal/app/{issuekey.go,issuekey_test.go}`, the argument handling in
  `cmd/saral/{main.go,main_test.go}`, `kernel.WithInitialPush` in `internal/ui/kernel/kernel.go`,
  `docs/{UX,ROADMAP}.md`
  **Half of #62, and the half that was determined.** `app.ParseKey` reads the shape of an issue key —
  a **shape**, never a claim the issue exists, because the project key charset is per-instance and a
  project can be renamed — and `app.ParseIssueURL` reads the key out of a pasted browse, board or
  backlog URL and hands back the host, so a URL for another site is named rather than read against
  this one. `cmd/saral` resolves the positional argument in that order and then as a view ID, and
  **errors naming the views it does know** when it is none of them: an unrecognised argument used to be
  handed to the kernel as a view ID and dropped, so `saral PROJ-142` and `saral bord` both opened the
  issue list. A second positional argument is refused with a pointer at `--project`.
  `kernel.WithInitialPush` is how the pane reaches the stack: an issue key names something no registry
  can build, so the composition root supplies the constructor and the kernel calls it at `Init`, with
  the complete `Deps` and after the root under it has initialised.
  **The in-session gesture is deliberately not here.** `g` is the view-slot prefix and what completes
  it for a key is a decision nobody has taken; #62 stays open holding that half and the palette entry
  that goes with it, and `docs/UX.md` says which half is built.

- [x] **K6 — Re-running setup updates the profile it finds** ·
  [#63](https://github.com/varijkapil13/saral/issues/63) ·
  **owns** `internal/ui/onboarding/**`, `docs/{ARCHITECTURE,UX,ROADMAP}.md`
  The symptom on the issue is real and its mechanism is not: onboarding never overwrote anything.
  `profileName()` skips every taken name, so re-running setup for a site somebody already had wrote a
  **second** profile — `example-2`, built from a zero value and the four fields the wizard collects —
  and made it active, leaving the first intact and unreachable. Either way the user has no theme, no
  glyph set, no timeline fields and no saved queries, so the number keys stop working.
  **The profile a run is re-running over is matched on site *and* account email**: the same account on
  one site is one profile, a second account on it is legitimately a second profile. Sites are compared
  normalised, because the file holds a bare lowercase host and the field holds whatever was pasted,
  and email case is folded. The four collected fields are the only ones replaced.
  **The review screen tells rather than asks**, naming the profile being updated and what survives:
  there is no second thing to choose between, and somebody who is not told has every reason to assume
  their theme is about to go.

## W1 · the seam Batches 4 to 8 code against

Three batches reach for the same three things — attachments, versions, sprints — and eight packets
would each have invented their own fixtures, their own role interface and their own guess at a body
for them. This wave lands the seam once so a packet arrives to something it can test against on a
machine with no Jira site, and so the guesses are written down in one place with a marker saying they
are guesses.

- [x] **W1 — Role interfaces, one sprint read, and the fixtures for three batches** ·
  [#38](https://github.com/varijkapil13/saral/issues/38) ·
  [#59](https://github.com/varijkapil13/saral/issues/59) ·
  [#60](https://github.com/varijkapil13/saral/issues/60) ·
  **owns** `pkg/jira/{port,roles,types}.go`, `pkg/jira/types_test.go`, `pkg/jira/jiratest/**`,
  `docs/{API-NOTES,ROADMAP}.md`, `.gitignore`
  Ten role interfaces — `AttachmentReader`/`Attacher`, `VersionReader`/`Releaser`, `BoardReader`,
  `SprintReader`/`SprintManager`, `TaskWatcher`, `Relocator`, `PlanReader` — so a view holds the
  narrowest thing it needs rather than the whole `Client`, and `*Fake` asserts every one of them at
  compile time. `Client` grows `Sprint(ctx, id)`
  ([#38](https://github.com/varijkapil13/saral/issues/38)): an issue's sprint value carries an id and
  a name and no dates, so a timeline has no board to reach the sprint through and walking every board
  of the project was the alternative. `jira.TaskState` gains the seventh state the schema has always
  had, `TaskCancelRequested` ([#59](https://github.com/varijkapil13/saral/issues/59)), and `Done()`
  deliberately reports it as **still running** — a poller sees it several times before `CANCELLED`.
  The fake's `BulkMove` now hands back a `/rest/api/3/bulk/queue/{id}` URL
  ([#60](https://github.com/varijkapil13/saral/issues/60)); it pointed at `/rest/api/3/task/{id}`,
  which is a different registry answering a body that does not decode as the queue's.
  Eleven fixtures and fourteen routes for the three batches: the three shapes an attachment answers
  in and its content as streamed bytes over a `/media/` route standing in for the host the redirect
  points at, the version read / create / release / unresolved-count set, and one sprint per state a
  write can leave. **There is deliberately no `PUT /rest/agile/1.0/sprint/{id}` route** and a test
  holds it that way, so P6.2 cannot reach the full replace even by accident.
  Every guess is an `assumed` or `schema` row in `docs/API-NOTES.md` under *Attachments, versions and
  sprint writes*, with the legend saying what those markers are worth; the first capture that touches
  one of those endpoints promotes the row or deletes it.
  **The capture ignore rule had a hole and it is closed here.** `/testdata/live/` was root-anchored
  while an unignored `pkg/jira/jiratest/fixtures/testdata/live/` chain existed **inside the
  `//go:embed fixtures` tree**, so a capture written there was both committable by `git add -A` and
  compiled into the binary, with every half of `checkleak.py` that CI runs reporting clean. The rule
  now reads `/testdata/live/` **and** `**/testdata/live/`, and the empty chain is gone. That is why
  `.gitignore` is in this packet's owned paths.

## Batch 4 — Attachments · parallel ×3

- [x] **P4.1 — List and download** · [#17](https://github.com/varijkapil13/saral/issues/17) · **owns** `pkg/jira/cloud/attachment.go`
  Ranged download with progress and resume, temp-file-then-rename.
- [x] **P4.2 — Upload** · [#18](https://github.com/varijkapil13/saral/issues/18) · same file, second PR (sequential with P4.1)
  Multipart part named `file`, `X-Atlassian-Token: no-check`, multi-file, delete.
- [x] **P4.3 — Preview** · [#19](https://github.com/varijkapil13/saral/issues/19) · **owns** `internal/ui/attach/**`
  Inline images via kitty/iTerm2 graphics, chafa half-blocks fallback, name+size last resort;
  system handler for everything else.
  **The bytes decide the protocol, not the name.** `MimeType` and the extension are both whatever the
  uploader's machine said, and a graphics escape claiming a format the bytes are not paints itself
  over the frame — so the file is sniffed before either protocol is claimed, and kitty takes only a
  PNG (`f=100` is its one encoded format), which is why a JPEG on kitty falls to chafa. Every rung
  falls to the next carrying the reason it could not be taken, because a pane showing a filename
  where a picture was expected is otherwise indistinguishable from a broken one.
  **The signed media URL never leaves the adapter.** The port takes an id and a writer, so this pane
  has no URL to leak; what it hands chafa and the desktop handler is a file it wrote itself under the
  cache directory, and a test drives every state asserting the URL reaches no frame, no status line,
  no argument list and no hand-off. The temp-file-then-rename is here because the port hands over a
  writer: a cancelled or refused download leaves nothing rather than a file of the right name and the
  wrong length, and the partial is discarded rather than resumed because nothing here can prove a
  file left behind is a prefix of this attachment.
  Reading attachments needs no capability beyond seeing the issue; adding and removing them do, so
  `CapAttachments` hides `u` and `d` with the site's own reason rather than hiding the pane. The pane
  cannot pre-empt an oversized upload: `attachment/meta`'s `uploadLimit` is read inside the adapter
  and is not on `jira.Capabilities`, so the site's number reaches the user only as a refusal.

## Batch 5 — Releases · parallel ×2

  CRUD, archive, unresolved counts, bulk fix-version assignment from the list.
  - The adapter half landed: `Versions`, `SaveVersion`, `UnresolvedCount` and `ReleaseVersion` in
    `pkg/jira/cloud/version.go`, with the release sweeping the open issues itself because
    `moveUnfixedIssuesTo` is a key nobody has watched work. Still open: `internal/ui/release/list*.go`
    and bulk fix-version assignment, so the box stays unticked.
  - **Two divergences the fake has to be corrected for**, both failing in
    `pkg/jira/cloud/conformance_version_test.go` on purpose until somebody who owns
    `pkg/jira/jiratest/**` fixes them: `Fake.ReleaseVersion`'s policy switch has no `default`, so an
    `UnresolvedPolicy` it does not know releases the version instead of being refused; and
    `fakeUnresolvedOn` counts by status category where the port and the site count by resolution,
    so an issue that is Done with no resolution is invisible to the fake and stripped by the client.
- [x] **P5.1 — Versions** · [#20](https://github.com/varijkapil13/saral/issues/20) · **owns** `pkg/jira/cloud/version.go`, `internal/ui/release/list*.go`
  CRUD, archive, unresolved counts, bulk fix-version assignment from the list. The counts are read one
  version at a time, when a release screen needs one, and the column says nobody has asked rather than
  drawing a zero. Bulk fix-version assignment is the release flow's move and strip policies: the port
  exposes no other way to change an issue's fix versions without replacing the whole array, and the
  fake cannot be asked at all — see the fix-versions row in `docs/API-NOTES.md`.
- [x] **P5.2 — The release flow** · [#21](https://github.com/varijkapil13/saral/issues/21) · **owns** `internal/ui/release/flow*.go`
  Check `unresolvedIssueCount`, then offer the same three choices the web app does (move to another
  version / strip the version / release anyway), confirm, then `PUT released: true`.

## Batch 6 — Sprints and boards · parallel ×3

- [x] **P6.1 — Board configuration** · [#22](https://github.com/varijkapil13/saral/issues/22) · **owns** `pkg/jira/cloud/board.go`
  Columns by `statusCategory`, estimation field and rank field read from board config — never guessed.
  **Every part of a board config is optional and the absences are not exotic.** A Kanban board sends
  no estimation object at all, which is why `BoardConfig.Estimation` is a pointer; a board may expose
  no rank field, so drag-to-reorder has to be a capability and not an assumption; and a board may be
  ordered by priority rather than by rank. Match everything by id or `untranslatedName`, never by
  display name — on a German instance the field, status and priority names all arrive translated, and
  `clauseNames` follows the translation too.
  **One conformance case fails on purpose** until `jiratest.Fake.Boards` trims a project key and
  refuses a blank one the way `Fake.IssueTypeStatuses` does: the cloud adapter refuses a blank key
  with a `*jira.ValidationError` naming `projectKey` and the fake answers a `*jira.NotFoundError`, so
  the rule is one nothing above the port meets.
- [x] **P6.2 — Sprint lifecycle** · [#23](https://github.com/varijkapil13/saral/issues/23) · **owns** `pkg/jira/cloud/sprint.go`
  `UpdateSprint` over the partial-update `POST`; `StartSprint`/`CompleteSprint` validate state
  locally first. **The raw `PUT` must never be reachable from the port** — it nulls omitted fields.
  Left two things it could not fix from inside its own paths, both of which P6.3 runs into:
  [#128](https://github.com/varijkapil13/saral/issues/128), six sprint rules the fake does not hold —
  including a >50-issue move it refuses and the adapter chunks, so the cap is the endpoint's and not
  the port's — which `TestSprintLifecycle_RulesTheFakeDoesNotHold` fails on rather than skipping; and
  [#129](https://github.com/varijkapil13/saral/issues/129), `cloud.PartialMoveError` living in the one
  package the views may not import.
- [ ] **P6.3 — Board and backlog views** · [#24](https://github.com/varijkapil13/saral/issues/24) · **owns** `internal/ui/board/**`, `internal/ui/backlog/**`
  Column view, drag or key to move between sprint and backlog (50-issue cap per call), rank-aware
  reorder when the board exposes a rank field. Takes the footer slot PC.2 assigns it; the kernel
  rejects a duplicate at startup, so this cannot be settled by guessing.

## Batch 7 — Cross-project move · parallel ×1

- [x] **P7.1 — Move wizard** · [#25](https://github.com/varijkapil13/saral/issues/25) · **owns** `pkg/jira/cloud/bulkmove.go`, `internal/ui/move/**`
  Target project and issue type, status remap, mandatory-field resolution, a confirm screen showing
  the full mapping, submit, then poll the task. Hidden with a reason when `BULK_CHANGE` is absent.
  Polls `/bulk/queue/{taskId}`, not `/task/{taskId}` — different shapes, both fixtured by PC.5.
  **The adapter half has landed:** `BulkMove` and `Task` on `pkg/jira/cloud/bulkmove.go`, both
  progress registries, the `{retain, type, value}` mandatory-field wrapper, and a both-adapters
  conformance table. **`internal/ui/move/**` has landed too**, and with it the two things the adapter
  cannot do for it: it resolves the target's mandatory field set from createmeta and sends the whole
  group or none of it — any value in `MoveRequest.Fields` opts the whole group out of retaining the
  rest from source — and its confirm screen says subtasks travel and are retyped. It also maps every
  source status by id, and refuses a subtask target type because a move cannot name a parent over
  there.

  **Four gates in `pkg/jira/cloud` fail on purpose**, each on a defect outside this packet's owned
  paths. `pkg/jira/jiratest/fake.go`: `Task` looks a task up by `ref.ID` and never reads `ref.URL`,
  so a view that keeps the id and drops the endpoint is green against the fake for its whole life;
  `TaskStatus.Failed` is filled with issue **keys** while the queue body keys its failures by numeric
  issue **id**, so one list renders as `EX-1` and the other as `10002`, and `pkg/jira/types.go:1055`
  documents the wrong one of the two; and an empty `TargetIssueTypeID` is a `NotFoundError` with an
  empty ID rather than a `ValidationError`. `pkg/jira/cloud/client.go`: `parseErrorBody` reads three
  refusal envelopes and `/bulk/**` answers a fourth, so a documented 400 from either bulk endpoint
  reaches the user in this client's words instead of the site's.

## Batch 8 — Timeline and plans · parallel ×3

- [x] **P8.1 — Date resolution** · [#26](https://github.com/varijkapil13/saral/issues/26) · **owns** `internal/app/dates.go`
  The cascade that gives every issue a start and an end (see below). Reports provenance per bar.
  Rule 4 reads a sprint's dates through `Sprint(ctx, id)`, which W1 landed on the port and on the
  fake ([#38](https://github.com/varijkapil13/saral/issues/38)): an issue's sprint value carries
  `{id, name}` and no dates, and the timeline has no board id to look them up with. Two divergences
  are open against it and each has a failing test in `internal/app/conformance_dates_test.go` rather
  than a table written around it. **`pkg/jira/cloud` has no `Sprint` method**, so rule 4 answers
  against the fake and falls through against a site —
  `TestConformance_RuleFourHasAnImplementationOnBothSidesOfThePort`. And the fake sends a sprint
  value as options whatever the field list said, where the schema-expanded read a timeline issues
  gets the raw JSON — `TestConformance_ASprintValueArrivesInTheShapeASchemaExpandedReadSends`. The
  cascade reads both shapes, so the second costs correctness nothing here and would cost the view
  everything.
  Rule 4 needs [#38](https://github.com/varijkapil13/saral/issues/38) first: an issue's sprint value
  carries `{id, name}` and no dates, and the timeline has no board id to look them up with.
- [x] **P8.2 — Timeline view** · [#27](https://github.com/varijkapil13/saral/issues/27) · **owns** `internal/ui/timeline/**`
  Horizontal bars, zoom by day/week/month/quarter, today marker, version and sprint markers,
  milestone diamonds where only one date resolves. Virtualized like every other list.
- [ ] **P8.3 — Plans** · [#28](https://github.com/varijkapil13/saral/issues/28) · **owns** `pkg/jira/cloud/plan.go`, `internal/ui/plan/**`
  Real plans where the token has Administer Jira; locally defined plans (projects/filters + date
  mapping from config) everywhere else, with the reason shown.
  `pkg/jira/cloud/plan.go` has landed; the checkbox waits on `internal/ui/plan/**`. Before the view
  is written, the fake has to stop putting a project key where the API puts a project id — the
  divergence is a red case in `TestPlans_BothAdaptersAnswerTheSameWay` and a row in
  [`docs/API-NOTES.md`](API-NOTES.md).

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
