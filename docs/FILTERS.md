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
- **`×` removes a facet's whole clause**; clicking a value name inside a chip removes that value. Both
  go through `Terms.Toggle`, so the keyboard, the chips and the picker cannot disagree.
- **`ctrl+g` clears everything**, which is the stroke the issue list already binds for exactly this.
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

**Issue type is resolved by the site's own type, never by name.** A type's name is localised and a
team-managed project mints its own, so the icon comes from `jira.IssueType.Hierarchy` and the
subtask flag — epic above story, story, task, subtask below — with `Bug` recognised only through
whatever the site marks as a bug-shaped type. **A hardcoded `"Bug"` string is the failure mode this
repo rejects most often**; if the site gives nothing to resolve an icon from, the type falls back to
its first letter, which is always available and never wrong.

| Meaning | nerd | unicode | ascii |
|---|---|---|---|
| epic | `` | `◆` | `<>` |
| story | `` | `●` | `*` |
| task | `` | `■` | `#` |
| bug | `` | `▲` | `!` |
| subtask | `` | `▪` | `-` |

Priority and status category get the same treatment where a column already spends cells on their
names, and the icon replaces the word only where the word was being truncated anyway — an icon beside
a full name is two spellings of one thing.

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

## Consumers

| Changed | Who must adopt it |
|---|---|
| the filter bar widget | `list` (replacing `terms.go`'s own line), `board`, `backlog`, `timeline` |
| multi-select picker | `list` and `board` already handle `ChosenMsg`; both must stop assuming the picker closed |
| `kernel.Glyphs` gains fields | every view that draws a type, a priority or a status category |
| `GlyphsFor` gains `"nerd"` | `config.Profile.Glyphs` validation, the settings screen's Glyphs row, `cmd/saral` |
| `s` moves to `S` in `list` | `list/keys.go`, its key golden, `docs/UX.md` |

## Definition of done, beyond `docs/PARALLEL.md`

- The filter bar has a golden at 80 and 120 columns, with one facet in force and with three, in both
  the nerd and the ascii glyph sets.
- A test asserts every view that can filter draws the bar when something is in force — walking the
  registry rather than naming views, so a fifth view cannot be added without it.
- The multi-select picker has a test that two values of one facet are both in force afterwards, and
  that the rows behind it narrowed after the first without waiting for the close.
- The glyph table has a test that every tier defines every icon — a missing nerd glyph must not draw
  an empty cell — and that an unknown issue type falls back to its letter rather than to a wrong icon.
- Sort re-runs the query: a test asserts the JQL changed, not just the on-screen order.
- No hardcoded issue type, status or priority **name** anywhere in the diff.
