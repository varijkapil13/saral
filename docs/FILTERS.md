# Filtering, sorting and the glyph tier

Six things asked for after a week of using the program. Read together they are not six features: they
are one absence repeated. **Filtering and sorting were built per view, and only two views built
them.** This document is what they become instead.

Read it with `docs/UX.md` (the key allocation and the footer), `docs/ARCHITECTURE.md` (the layers)
and `docs/SETTINGS.md` (the registry pattern this borrows).

## What is actually there now

Checkable against the tree as of `de099eb`.

| View | Can filter | Draws what is in force | Can sort |
|---|---|---|---|
| issues (`list`) | yes, `f` | yes — a clickable chip line, `list/terms.go` | no |
| board | yes, `f` | **no** — it holds `filter.Terms` and renders none of it | no |
| backlog | no | — | no |
| timeline | no | — | no |
| plan, release, sprint | no | — | no |

**Nothing sorts.** Order is baked into each saved search's JQL — `ORDER BY updated DESC` in
`list/search.go` — and no view offers to change it.

**The board holds a filter it never shows.** `board.terms` narrows the cards and the only trace on
screen is a `filteredOut` count. Applying a filter and then not knowing what is in force is the
everyday complaint, and it is a rendering gap rather than a model one.

**Multi-select is already modelled and not reachable.** `filter.Terms` is documented as *"two facets
narrow together; two values of one facet widen it"*, and `Terms.Toggle` maintains it. But
`filter.chooseValue` ends with `tea.Sequence(kernel.Pop(), kernel.Broadcast(ChosenMsg{Term:…}))` — the
picker closes on the first value, so a second assignee costs a fresh trip through the facet menu.

**Issue type is already a facet.** `FacetType` is in `filter.Facets` and the picker offers it. "Type
in every filter" is not a new facet; it is the four views that cannot filter at all.

**Type is drawn as text.** `list/render.go` writes `iss.Type.Name` padded into a column. Nothing
carries an icon.

## The decisions

Taken rather than derived, so they are written down once:

1. **Sort applies to row-shaped views only** — issues and the backlog. A board is ordered by column
   and by rank inside it, and a timeline by date; sorting either would mean discarding the order that
   makes it that view. Neither gets a sort control, and this sentence is why.
2. **A Nerd Font may be assumed, as a tier and not as a floor.** This reverses the rule stated in
   `kernel/theme.go` — *"Nothing here may assume a Nerd Font"* — deliberately and on request. Three
   tiers now: `nerd` → `unicode` → `ascii`, `nerd` the default, all three switchable from the
   settings screen's existing Glyphs row. A terminal without the font shows tofu, which is why the
   row exists and why the tier below is kept whole rather than deleted.
3. **The filter bar is lifted into one widget and adopted by every list-shaped view**, rather than
   copied a third time.

## The filter bar

One widget, `internal/ui/widget/filterbar`, drawn under the rows by whichever view is filtering.

```
 Issues                                                    PROJ | example.atlassian.net
   PROJ-9   ▲  Login fails on Safari                      Ada Lovelace    In Progress
   PROJ-12  ●  As a user I want to stay signed in         Ben Adams       To Do
 ─────────────────────────────────────────────────────────────────────────────────────
  assignee: Ada Lovelace, Ben Adams ×   type: Bug ×                      ctrl+g clear all
```

- **One chip per facet, listing its values.** Not one chip per value: the grouping is what
  `filter.Terms` already promises, and a facet with three assignees on it is one narrowing, not three.
- **`×` removes a facet's whole clause**, through the new `Terms.Without`; clicking a value name inside
  a chip removes that value, through `Terms.Toggle`, the same one the keyboard uses — so the keyboard,
  the chips and the picker cannot disagree. `×` is drawn from the glyph tier (`Glyphs.Cross`) rather
  than the literal character, so an ASCII terminal gets `x` and not tofu.
- **`ctrl+g` clears everything.** Checked against the tree: the issue list's `ctrl+g` only cleared the
  typed `/` filter before this packet — a term set by the picker survived it, silently, because nothing
  had ever asked it to clear one too. `clearFilter` now drops both.
- **The bar is drawn only when something is in force**, so a view with no filter loses no rows to it.
- Every chip is a mouse zone, per principle 3 in `docs/UX.md`.

### The picker becomes multi-select

`filter.Model` stops popping on the first value. `enter` toggles the value under the cursor and the
list stays up with the chosen ones marked; `esc` closes. The view is told as each toggle happens, so
the rows behind the picker narrow live and the picker is not a modal you commit at the end.

`ChosenMsg` keeps its shape — one term per toggle — because a broadcast per toggle is what lets the
rows follow along. What changes is that the picker no longer sends `kernel.Pop()` with it.

## The glyph tier

`kernel.Glyphs` gains a third constructor beside `UnicodeGlyphs` and `ASCIIGlyphs`, and gains the
fields the icons need. `GlyphsFor` resolves `"nerd"`, `"unicode"`, `"ascii"`, defaulting to nerd.

**Checked against the payload rather than the port, on the second pass.** The first pass found no
hierarchy on `pkg/jira.IssueType` and fell back to the type's first letter — which, drawn as a board
card's marker flush against the key, turned `TR-3322` into `STR-3322`, a key on another project. The
port was the thing that was short, not the API: `fields.issuetype` carries `hierarchyLevel` on every
issue, and its `iconUrl` carries the type's avatar id in its path. Both are decoded now. The level
resolves a subtask (below zero) and an epic or anything above one (one and up); the avatar id resolves
a bug, a story or a task, because a built-in type keeps its default avatar on every site — `10303`,
`10315`, `10318`, product constants the way a plugin key is. A standard type with an image of its own
resolves to `TypeOther`, one neutral shape for all of them. **Nothing resolves from the name**, and a
test feeds the resolver types named `Bug` and `Story` with no other data to prove it answers a shape
and never a letter.

| Meaning | nerd | unicode | ascii |
|---|---|---|---|
| epic | `` (nf-fa-bolt) | `◆` | `<>` |
| story | `` (nf-fa-bookmark) | `●` | `*` |
| task | `` (nf-fa-tasks) | `■` | `#` |
| bug | `` (nf-fa-bug) | `▲` | `!` |
| subtask | `` (nf-fa-level_down) | `▪` | `-` |

The icon goes **first, everywhere the type or the status category is drawn**, with the name behind it
where the name fits and alone where it does not — the shape is the part a reader takes in without
reading, and Jira's own UI draws both. The first pass of this read one sentence here as *icon only where
the name would have been truncated*, so at any ordinary width the list and the issue header showed the
word and never the shape, and only the board — which never drew the name — got an icon. That sentence
is gone. The list's type and status columns are one cell wider each for it — `Sub-task` and `In
Progress` fit exactly behind their icons, and every column still fits at a hundred columns; the backlog, which drew no type
at all, draws the board card's cell — the icon, muted, and a space before the key; the issue header
carries both icons at every width and gives up only the status's parenthetical when the pane is narrow.
Priority has no icon that is not its first letter, and a letter standing alone among the facts read as
a fact of its own, so it keeps its name.

## Sort

A sort is part of the search, not a view preference: the issue list already sends `ORDER BY` and the
site orders the page. So sorting re-runs the query rather than reordering what is on screen, and a
list that has more pages behind it stays correct.

- **`s` sorts** in a row-shaped view. `s` currently saves a query in the issue list — that moves to
  `S`, and the footer and `?` carry both.
- The picker offers the fields JQL can order by that this client can name without guessing: key,
  summary, status, type, priority, assignee, created, updated, due. Each toggles between ascending
  and descending; the chosen one is drawn in the header as `sort: updated ↓`.
- **The order is remembered per view in `ui.toml`**, beside the pane split — it belongs to how this
  machine likes to look at things, not to a Jira account, which is the reason `config.UIState`
  already gives for living where it does.

**"The site orders the page" was checked against `pkg/jira.BoardQuery` while building the backlog's
own sort and was only ever true of the issue list.** `BoardQuery` reads a board through the Agile
API's own `/board/{id}/issue`, which orders by the board's rank and carries no field for anything
else — its own doc comment says so: *"there is deliberately no further narrowing invented here."*
Widening it needs a port amendment, which is out of scope here and belongs to whoever next needs Agile
ordering. The issue list's own sort re-runs the query exactly as designed, ORDER BY and all; the
backlog's reorders the issues a section already holds, locally, the same way its own rank order and
`filter.Terms` narrowing already work against what one read brought back rather than against the site.
The two are visibly the same gesture — `s`, a field, a direction, `sort: field ↓` in the header — and
differ only in what answers a page.

**A local sort over a paged list is a sort of the wrong issues, and that had to be fixed on top.**
The backlog reads fifty at a time and asks for the next page as the cursor nears the end. Ordering
what it happened to hold meant the order covered a part of the backlog, and the page landing next
dropped its rows into place *above* whatever somebody was reading — a wrong answer and the one thing
docs/UX.md principle 5 asks a background read never to do. So choosing an order the view cannot ask
the site for reads the rest of the backlog first, says so on the status line while it does, and puts
the order in force once, rather than letting it settle over several pages. Nothing changes for a
backlog that fits in one page, which is most of them. `pkg/jira/jiratest/fake.go`'s `ORDER BY` subset gained `issuetype`
and `duedate`, both real JQL fields the fake had never been asked for before this packet's own tests
needed them.

## Remembering the view and the filters

A session lands back where it was left: the root view it last had open, and — for `list`, `board`,
`backlog` and `timeline` alike — the terms that were narrowing it. `board` also remembers which of a
board's quick filters were toggled on. None of this is the pane split or the sort order: those already
live in `config.UIState` under the cache directory because they are how *this machine* likes to look at
things, and a pane width copied to another laptop should not carry another machine's proportions. A
filter is a different kind of fact — `assignee = "5f2a…"` names an account only *this site* and this
token's own visibility make sense of — so it is kept by `config.ProfileScope{Site, Account}`, the same
pair `cmd/saral/main.go`'s `openCache` scopes the on-disk cache by, and one profile's kept state never
answers for another's, not even a second account on the same site.

**`kernel.Deps.Memory`** is the seam: a small `Recall(view, key string) (string, bool)` /
`Keep(view, key, value string)` / `Forget()` interface, nil-safe throughout, because a session with
nowhere to write — no profile yet — remembers nothing and says nothing about it. The kernel package
cannot import `internal/ui/filter` (`docs/ARCHITECTURE.md`'s layering), so everything kept through it
is opaque text a view encodes and decodes for itself: `filter.Terms.Encode()` writes a small JSON
array, each facet spelled by `stableName()` rather than its own `iota` — a number a later build
reordered would otherwise be read back as the wrong facet entirely — and `filter.DecodeTerms` drops
anything it does not recognise (an older or newer build's word, hand-edited text) rather than reading
it as `FacetNone`. `internal/config` gains `RememberedState` / `RememberState` / `ForgetRemembered`
alongside `Split`/`SaveSplit` and `Sort`/`SaveSort`, and `cmd/saral`'s `profileMemory` is the only
thing that implements `kernel.Memory`, because it is the only layer that knows both the site and the
account.

Each of the four views recalls its own terms in its constructor, before it reads the cache — the
cached rows a project's last board or search left on disk are keyed by that exact narrowed query (for
`list`, the JQL; for `board`, `backlog` and `timeline`, whatever they already held), so the remembered
filter and the first frame agree from the start rather than painting unfiltered and then narrowing a
frame later. Every gesture that changes the terms — the picker, a click on a chip, `ctrl+g` — keeps
the new value in the same call, so nothing is a step behind what is on screen.

`board`, `backlog` and `timeline` never send a term to the site — `terms.go`'s own doc on each already
explains why — so a remembered id the site has since stopped knowing about is simply a term that
matches nothing, the same as one chosen fresh. `list` does send its terms, as a JQL clause, and a
remembered id can be one the site refuses outright: a status deleted since, an account that left the
project. That refusal is checked once — on the very first answer after a recalled filter is put in
force, success or failure alike — and only a `*jira.ValidationError` is read as a verdict on the ids
themselves; a rate limit or a dropped connection is an ordinary failure and leaves the remembered
filter standing for the next retry. A verdict drops the terms, forgets them so the next session does
not walk into the same refusal, and falls back to the view's own default search, saying so on the
status line.

**Forgetting it.** `session.memory`, a `KindAction` setting beside `session.profile` on the Settings
screen, calls `Memory.Forget()` and clears everything this profile has ever kept — the remembered root
view and every view's own terms — in one write. It lives in `internal/ui/kernel` rather than in a
view package because `Memory` itself does: the interface, the `Deps` field and the kernel's own use of
it (remembering which root view it opens) are one small file, and a setting that undoes all of it
belongs beside them rather than guessed at from a single view's `terms.go`.

## Consumers

| Changed | Who must adopt it |
|---|---|
| the filter bar widget | `list` (replacing `terms.go`'s own line), `board`, `backlog`, `timeline` |
| multi-select picker | `list` and `board` already handle `ChosenMsg`; both must stop assuming the picker closed |
| `kernel.Glyphs` gains fields | every view that draws a type, a priority or a status category |
| `GlyphsFor` gains `"nerd"` | `config.Profile.Glyphs` validation, the settings screen's Glyphs row, `cmd/saral` |
| `s` moves to `S` in `list` | `list/keys.go`, its key golden, `docs/UX.md`, the palette's own command id |
| `s` sorts in `list` and `backlog` | each view's own `keys.go`, `sort.go` and key golden; `internal/ui`'s footer, `?` overlay, right-click menu and `keyOwners` sweep; `internal/ui/palette`'s session golden |
| `config.UIState` gains `Sorts` | `internal/config/uistate.go` and its tests only — every reader goes through `Sort`/`SaveSort`, never the map |
| `kernel.Deps` gains `Memory` | `cmd/saral` (`profileMemory`, wired alongside `openCache`); `list`, `board`, `backlog`, `timeline` (`recallTerms`/`rememberTerms` in each `terms.go`); `board`'s `quickfilter.go` besides |
| `filter.Terms` gains `Encode`/`DecodeTerms` | every one of the above; nothing else composes `filter.Term` from stored text |
| `kernel.startView` prefers the recalled root | `kernel.New`, `open`, `openWhenNothingCould` — every place a root is put on the stack now also remembers it |

**"`s` moves to `S`" turned out to be two views' worth of key-golden fallout, not one.** `list`'s own
`keys.golden`, `bindPrompt`/`query_test.go`'s bind gesture, `poll_test.go`'s "a number key being
picked" case, and every top-level golden that renders the issue list's resting footer — three widths
of `internal/ui/testdata/footer_*.golden`, `overlay_120x38.golden` and `internal/ui/palette`'s own
`session_120x30.golden`, which fuzzy-ranks the command registry and so redrew once `issues.sort`
joined it — all carried the old binding. None of it is a second implementation of anything; it is the
same "the footer, the `?` overlay and the palette are one registry" property `docs/UX.md` states, read
backwards: change what the registry says and everything that renders it must be told, by regenerating
the golden rather than editing prose into it.

**A local matcher needs the field it matches on to actually be in the read.** Checked against the
tree while landing the backlog's and the timeline's own `f`: both asked for `app.ListProjection()`
(or its own narrower equivalent) with nothing added, which carries assignee, status, priority and
type but never reporter or labels — and the timeline's own projection carried none of the four. A
`FacetReporter` or `FacetLabel` term against either would have matched every row as "no reporter" or
"no labels" rather than the ones actually chosen, silently, because the field the picker offers real
values for was never asked of the site in the first place. The board's own earlier packet had already
found this for itself (`plan.projection` widens `ListProjection` with `"reporter", "labels"`); the
backlog's and the timeline's reads now do the same, and the timeline's needed all four widened rather
than two. Anything that narrows locally against `filter.Terms` — a fifth view, or a widened facet list
on one of these four — has to ask this same question of its own projection before trusting a local
match.

## Definition of done, beyond `docs/PARALLEL.md`

- The filter bar has a golden at 80 and 120 columns, with one facet in force and with three, in both
  the nerd and the ascii glyph sets.
- A test asserts every view that can filter draws the bar when something is in force — walking the
  registry rather than naming views, so a fifth view cannot be added without it.
- The multi-select picker has a test that two values of one facet are both in force afterwards, and
  that the rows behind it narrowed after the first without waiting for the close.
- The glyph table has a test that every tier defines every icon — a missing nerd glyph must not draw
  an empty cell — and that an unknown issue type falls back to its letter rather than to a wrong icon.
- Sort re-runs the query where a query is what answers the view: a test asserts the issue list's JQL
  changed, not just the on-screen order. The backlog has no query to re-run — see the correction under
  "## Sort" — so its own test asserts the local reorder instead, against the same compare function the
  view sorted with rather than a hardcoded expectation of the fake's own generated order.
- No hardcoded issue type, status or priority **name** anywhere in the diff.
