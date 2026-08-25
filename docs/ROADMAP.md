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
  packet beside this one rather than a corner of it. It is
  [#87](https://github.com/varijkapil13/saral/issues/87), filed with the three places the project keys
  could come from and how each behaves on a first run.
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
  and is gone; persisting it needs the kernel, which is
  [#81](https://github.com/varijkapil13/saral/issues/81).
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
  nothing for P6.3 — and the right-click context menu is
  [#76](https://github.com/varijkapil13/saral/issues/76), which needs `kernel.Command` to know what a
  command applies to.
- [x] **P3.4 — Local fuzzy index** · [#15](https://github.com/varijkapil13/saral/issues/15) · **owns** `internal/app/{index,match}.go`, their tests and benchmarks, `docs/{PERFORMANCE,ROADMAP}.md` · **after P3.2**
  **This row used to promise "instant search over cached issues with no round trip".** Half of that
  already shipped in P1.5: `internal/ui/list` filters as you type with no round trip and no
  allocation — `refilter` reuses its slice over needles built once per page, and
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
  **The list's filter was deliberately left alone.** It is the budgeted keystroke path at 1 alloc/op
  and another packet in this wave is in that directory; ranking it is
  [#84](https://github.com/varijkapil13/saral/issues/84).
  **Its consumer is P3.1** ([#12](https://github.com/varijkapil13/saral/issues/12)): the palette
  ranks commands with `app.Pattern`, and the signature was published on #12 and #15 before this
  packet was finished so it could be coded against. `app.Index` itself has no view yet —
  [#85](https://github.com/varijkapil13/saral/issues/85) reaches it from the palette and
  [#62](https://github.com/varijkapil13/saral/issues/62) from a key jump.
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
  four palette commands and the choice is written back without dropping the rest of the profile
  ([#63](https://github.com/varijkapil13/saral/issues/63)). Colour stepping down to 256 and 16 was
  never missing — bubbletea's renderer does it — so that was a doc correction.
  **The hints bullet moved to P3.1** ([#12](https://github.com/varijkapil13/saral/issues/12)): "after
  you reach an action through the palette three times, the status line notes its key" needs the
  counter, the call site and the frecency table that packet already owns, and W0-b landed
  `CommandRanMsg{ID, Keys}` as the signal. A second counter in the chrome would be a second answer.

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
