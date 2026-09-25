# Interaction design

The goal is a tool that gets faster to use the longer you use it, without ever being confusing the
first time. Two audiences, same binary: someone who opened it today, and someone who has lived in it
for six months.

## Principles

1. **First paint is instant, always.** Cached data appears before the network is touched. A spinner
   is a failure of caching, not a loading state.
2. **The footer only shows keys that work right now**, and it shows what you can *do* before where
   you can go. No greyed-out lists of everything. Context determines the hints, and the hints come
   from the key registry so they can never drift from reality.
3. **Every action is reachable three ways** — a key, the command palette, and the mouse. Nothing is
   keyboard-only, and nothing is mouse-only.
4. **Nothing destructive without a named confirmation.** The confirm shows what will change, in
   words, including counts ("release 2.4.0 with 12 unresolved issues?").
5. **Never lose the user's place.** A background refresh patches rows; it does not reset the cursor,
   scroll offset, filter or focus.
6. **Never lose the user's text.** Anything typed survives a failed request, a 409, and a crash —
   drafts are persisted per issue.

## Progressive mastery

The mechanisms that make familiarity pay off, in the order a user meets them:

| Stage | Mechanism |
|---|---|
| First minute | Onboarding writes the profile, then drops you on your own issues — or on the project itself, where nothing in it is assigned to you, because an empty first screen reads as a broken program. `?` explains the current view only. |
| First hour | The footer teaches what can be done to the thing in front of you, most-used first. Mouse works, so nothing is blocked on learning. |
| First week | Frecency: the palette's commands and the project picker reorder so your usual choices are first. Assignees, versions and labels do **not** yet — a value picker ranks with the same subsequence scorer the palette filters by, and nothing counts what you pick. |
| | Hints: after you reach an action through the palette three times, the status line notes its key. Built in P3.1 ([#12](https://github.com/varijkapil13/saral/issues/12)) rather than with the footer: the count, the call site and the frecency table are one piece of data, and P3.1 already owns it. `kernel.CommandRanMsg{ID, Keys}` is the signal it hangs on, and `Command.Keys` is the key it names — a command nothing binds is never given one, and the line is said once rather than on every run after the third. |
| Ongoing | Saved queries bound to `1`–`9` and kept in the profile, validated by `app.SavedQueries` so a file and a keypress cannot disagree. **JQL history is not built** — `e` shows the search on screen and runs an edited one, and nothing keeps the ones run before it. |
| | Local fuzzy index — typing a key or a few words of a summary finds it with no round trip. Reached from the palette: `ctrl+k` and a few letters offers the issues already on disk beside the commands, ranked by `app.Index` over `app.Cache`. |
| | **Session resume is partly built.** A restart lands on the root view the last session had open, and `list`, `board`, `backlog` and `timeline` reopen with the same filter terms in force (`board` its active quick filters too) — scoped by site and account (`docs/FILTERS.md`), so this is what a *token* remembers rather than what a machine does. The rows themselves still come out of the cache and badged for age, and the pane split out of `ui.toml`; the cursor and the scroll offset are still not written anywhere. |
| Fluent | `saral PROJ-142` and `saral https://…/browse/PROJ-142` open that issue; `saral board` opens a view by name; `--project` scopes the session. An argument that is none of those is named as the mistake it is rather than silently opening the default view. Scripts use the non-interactive subcommands instead (`saral search`, `saral issue view --json` and the rest; see [CLI.md](CLI.md)). |

Frecency is a plain local table of `(item, count, lastUsed)` scored `count * decay(lastUsed)`. No
telemetry leaves the machine, ever.

The palette keeps its own table in a file of the palette's under the cache directory: command IDs from this build and two numbers each, nothing from any site. It is
not the profile, because `config.toml` has to stay safe to hand somebody, and a list of what you
personally run most is not that; and it is not the issue cache, which has no record API and is
absent exactly when a first run would most want to start learning. Half a use is worth what it was
after a week, so yesterday's habit beats last month's. A session with nowhere to write it ranks by
the registry's own order rather than refusing to open.

### What "only the keys that work" costs

Principle 2 is not satisfied by a view registering its keys once. `kernel.RegisterKeys` runs in an
`init()` and refuses a second call, so what it holds is the view's **resting state** — a list nobody
is typing into, a thread being read. Half of what a user actually meets is some other state: a filter
open, an editor over the field list, a deletion waiting for a `y`, an onboarding step with nothing
behind it. Advertising the resting keys there names strokes that do nothing, which is the failure
principle 2 describes rather than a smaller version of it.

**The one thing the row can be a run behind on is what the token may do.** The probe's answer is kept
between runs so that the first frame is drawn knowing it, rather than hiding every gated view until a
round trip lands — a stored answer gates a view exactly as a probed one does, and `Init` re-asks
every time and reaches the footer through the same message `R` does. Inside the answer's hour that is
the same situation as a second after a live probe and says nothing about itself; past it, the status
line says when it was last checked until the fresh answer arrives. What the row will not do is show a
key on an answer that failed: an expired token, a rate limit or an unreachable host leave the last
real answer standing rather than becoming five denials.

So a view whose keys move with its state implements `kernel.KeyReporter` and answers for the state it
is in. `esc` is called *clear filter*, *put it aside*, *do not save yet* and *keep it* in four
different places, and each of those is the one on screen at the time. A state with nothing of its own
to offer — a save in flight, a site being asked — says so by advertising nothing, and the footer falls
back to the globals rather than naming a key that is being refused. `docs/ARCHITECTURE.md` has the
interface and the generation counter the memoized chrome needs.

### The row at the bottom

One row, three cells, at every width. It is never two rows, because the constraint is width and not
height: a second row would be truncated the same way and would cost a view a line it needs more —
at 80×20 the body is 17 rows with a status line up, and the issue list has as few as 13 live rows in it.

| cell | what it holds |
|---|---|
| root | the **root** view's title — where `esc` lands, and what a click there goes back to |
| actions | what can be done to the thing on screen, most-used first, terse; whatever is left over becomes `+N` |
| globals | `? ctrl+k esc`, or `q` at the bottom of the stack — bare keys, never given up to make room. A view taking typing swallows all but `ctrl+k`, and the cell drops what will not arrive |

```
 Issues  tab pane  e edit  t status  C comment                     ? ctrl+k esc
 Issues  enter open  c comment  e edit  t status  / filter  +3    ? ctrl+k q
```

**The order things are given up in is the point.** Actions fold into a `+N` from the right, then the
root cell goes, then the actions lose their descriptions and keep their keys. The globals never go.
That order exists because the previous row gave up exactly the wrong end first: seven view slots cost
81 columns against the 80 this program documents as its minimum, so `? help`, `esc back` and
`ctrl+k commands` were all past the ellipsis, and at some widths the row overflowed by one column and
was dropped whole. Somebody ran the program at 80 columns for a week and was never told the command
palette existed ([#96](https://github.com/varijkapil13/saral/issues/96)).

**The motions are not on the row.** The issue pane answers twenty strokes and eleven of them only
move the cursor or pan the document; a row that lists them in the order they were declared spends
itself on scrolling.
`?` lists them, and it leads with the actions — spelt out, because the overlay has room for *edit
fields* where the row had room for *edit*. Every key appears there exactly once.

**Every entry on the row is clickable**, and a click is delivered to the view as the first stroke of
the binding it advertises — so the key, the palette entry and the pointer are one implementation and
cannot drift. `+N` opens `?`, which is where what it stands for is listed.

The digits keep their place on the row where they work: in a root view the bound saved queries are
the first entry in the actions cell, named after the query. **The view slots are not on the row, and
they are not going on it.** One row cannot hold nine destinations and the actions as well, the
destinations are the half needed least often, and the header already says what is on top. What was
wrong with that was not the trade but the consequence: at rest the row said `? ctrl+k esc` and
nothing anywhere said the other views existed, so somebody using the built binary asked how to open
the board and was right to. **The destinations are now taught at the moment of asking** — pressing
`g` draws them, see *Behind the prefix* below — as well as by `?` and by the palette's *Go to* rows.
The row itself still names only the root you are in, at every width.

While the prefix is latched the row says what that overlay answers to and nothing else, which is what
it already does under `?` and under the right-click menu:

```
 Issues  1-9 switch view  i jump to an issue  s settings  up/down choose  enter go there  esc cancel
```

The theme is a setting rather than a command, reached with `ctrl+,` or `g s`, and the choice is
written back into the profile it came from — see [`docs/SETTINGS.md`](SETTINGS.md). It was nine
palette rows once, one per mode and per colour scheme, with nothing saying which was in force.

## Navigation model

A stack, not a graph — so "back" always means something.

```
run a saved query      1 – 9          in a root view; the profile's own searches
where you can go       g              from anywhere; the destinations, and what this view does
                                      with the same prefix. esc throws it away
switch view            g then 1–9     from anywhere, including a pushed view
push                   enter          open the thing under the cursor
switch pane            tab            in a view with more than one; shift+tab goes back round
pop                    esc            back, never quits from a pushed view
quit                   q / ctrl+c     only from a root view, and only when nothing on the stack
                                      is holding a draft
palette                ctrl+k         everything, fuzzy; opens over what you were in, esc returns.
                                      the one global a view taking typing cannot swallow
settings               ctrl+, / g s   opens over what you were in, esc returns — the state the palette
                                      used to carry as nine rows, now one. docs/SETTINGS.md.
                                      g s reaches it the way g i reaches the palette
search in view         /              filter rows live
clear everything       ctrl+g / esc   a term the picker set and a typed filter both, from the browsing
                                      state; esc clears the typed one while still typing
filter by a value      f              pick a facet, then one of the values this site holds
every issue here       a              widen the search to the whole of the session's project;
                                      also the state with no filter values in force
edit this search       e              show the JQL on screen and run an edited one
sort                   s              in issues and the backlog; pick a field and a direction
save this search       S              bind the query on screen to a number key
refresh                r / R          current view / purge and refetch. both say what came back
kill to end of line    alt+k          in any text field. ctrl+k is the palette and never reaches one,
                                      so bubbles' own binding for this is rebound in one place
```

Vim keys and arrows are both always bound. `j/k` and `↑/↓` are not a preference to configure.

### What happens to a view you leave

Three of these gestures take a view off the screen and they do not mean the same thing, which is
visible in what happens to whatever it was fetching.

| Gesture | The view you were in | Its in-flight read |
|---|---|---|
| `ctrl+k`, or anything else pushed over it | still there, underneath | carries on, and lands in it |
| `g` and a digit, to another root | kept, and comes back on its own digit with the same row under the cursor | carries on, and lands in it |
| `esc` from a pushed view | gone | given up |

The first row is the one worth stating out loud: **opening the palette over something that is still
loading must not cancel the load.** It used to, because the only thing a view was told was that it
had lost the keyboard, and a thread in a sidebar loses that whenever the pane beside it takes it.
Nothing about losing the keys means nobody wants the answer.

The last row is the other half. A pushed view that `esc` takes away is not coming back — the next
`enter` builds a new one — so the request it is waiting on is one nobody will ever draw, and it is
cut short rather than left to finish into nothing. The same is true of everything stacked over a root
when you switch away with `g`: that stack is gone, and only the root underneath is kept.

`q` and `ctrl+c` end the program, which throws every view away and stops nothing on the way out:
there is no next frame for an answer to land in.

And what a view was waiting for arrives in the view that asked for it, whatever is on screen by then.
Open the palette over a loading issue, read a command, press `esc`, and the issue is there — it
finished loading while you were reading, underneath. Nothing is asked for twice on the way back, so
coming out of the palette never costs a second round trip to the site.

### Inside the palette

Every letter is the filter's, which is the one place `j` and `k` do not move a selection: `↑`/`↓` and
`ctrl+p`/`ctrl+n` do, `enter` runs what is under the cursor and `esc` puts the palette away. A click
selects a row and a second click on the selected one runs it, the same gesture the issue list uses.

It offers what you can actually run. A command whose capability this site or token does not allow is
not in the list — the registry deliberately filters nothing, because a registry that consulted the
probe would be its own client — and when the filter matches only refused commands, their reason is
what the palette says instead of "nothing matches". Filtering there and refusing in the kernel are
the same sentence twice on purpose: the list is what you can see, and the kernel's refusal is what
happens if the answer changes between opening the palette and pressing `enter`.

The palette never runs a command itself. It names one, `kernel.RunCommandMsg` carries the name, and
the kernel runs it against the deps it holds — see `docs/ARCHITECTURE.md`, which is also why a search
run from the palette is scoped to the project the session is on now rather than the one it opened in.

**It is everything, so it is also the issues.** Anything typed is ranked against the commands this
build registered *and* against every issue already on disk, and both are offered in the same list:
the commands first, then the issues under them. There is no prefix and no second keystroke to learn,
because a mode is a thing to remember and principle 3 asks for one gesture that reaches everything —
and because typing `PROJ-142` matches no command, so the half you meant is the only half that
answers. `enter` runs the command under the cursor or opens the issue under it, and the footer says
which of the two it is doing: the same stroke, named for the row it is on.

The issue half is honest about being a copy on disk. Each row says how old the copy is — *just now*,
*9m old*, *1d old* — badged past the age the cache itself stops calling an issue current, because a
title from last week is worth showing and worth doubting. An issue read by a search too narrow to
have asked for its title says so rather than drawing a blank, which is a different answer from an
issue whose title is empty. Choosing one opens the detail pane over whatever the palette was opened
from, and not over the palette: the stack is what `esc` walks back, and a filter you have finished
with is not a place to land.

Only twenty issues are offered at once. Past that the answer is a longer filter rather than a longer
scroll, and the count says `20+` because the index was asked for twenty and stopped, so there is no
honest total to give.

**A session with nowhere to cache is normal** — a first run, another copy of Saral holding the file,
an unwritable home — and the palette says so rather than looking half built. The filter's own
placeholder is the tell: it offers to find an issue only where there is a cache to find one in.

### Who owns the number keys

Settled in PC.2 ([#49](https://github.com/varijkapil13/saral/issues/49)): **the digits are
contextual.** In a root view a bare digit runs the saved query bound to it — the searches the profile
keeps, which is what makes the keys worth having on the first day rather than the first month. A view
is reached with `g` and its digit, from anywhere. `ctrl+k` still reaches everything.

`g` is a prefix the kernel **buffers**: it forwards nothing when you press it, and on the next key
either takes a digit for itself or hands the view both keys in the order they were typed. That is why
`gg` and `ge` inside a list still go to the first and last row. `esc` throws a half-typed gesture
away, and a view that is taking typing gets the keys before any of this happens.

The footer advertises only what works: the digits that actually have a query bound, named after the
query. The slots themselves are not on the row — see *The row at the bottom* — so a slot is still
allocated here rather than picked, and *Behind the prefix*, `?` and the palette are where its digit is
taught.

| Slot | View | Arrives with |
|---|---|---|
| 1 | issues | P1.5 — shipped |
| 2 | board | P6.3 ([#24](https://github.com/varijkapil13/saral/issues/24)) — shipped |
| 3 | backlog | P6.3 — shipped |
| 4 | sprints | P6.3 — shipped |
| 5 | releases | P5.1 ([#20](https://github.com/varijkapil13/saral/issues/20)) — shipped |
| 6 | timeline | P8.2 ([#27](https://github.com/varijkapil13/saral/issues/27)) — shipped |
| 7 | plans | P8.3 ([#28](https://github.com/varijkapil13/saral/issues/28)) — shipped |
| 8, 9 | free | — |

Four views register a `ViewSpec` with `Slot: 0`, and are reached by being pushed, by name or from the
palette: onboarding, the palette itself, the new-issue form and the comment thread. **Four packages go further
and register no `ViewSpec` at all**, because a registry constructor has nothing to open them over —
`internal/ui/issue` is pushed with the issue it draws, `internal/ui/attach` with the issue whose files
it lists, `internal/ui/filter` with the terms it narrows, and `internal/ui/move` with the issues it is
moving. So is the release screen inside `internal/ui/release`, whose package does register one for the
version list. Each of them still registers its keys, so the footer, the `?` overlay and the sweeps in
`internal/ui` find it exactly as they find a slotted view. `kernel.RegisterView` refuses a second
claim on a slot at startup, so the table above is enforced rather than merely written down — but it is
written down so that six later packets do not each pick a number.

### Behind the prefix

**Pressing `g` draws the destinations.** The program was already sitting there waiting for the key
that completes the gesture and saying nothing about it, so this costs no width at rest, changes no
gesture, and `g2` typed fast behaves exactly as it always did. The palette's *switch view* opens the
same overlay for somebody who has not learnt the prefix — except over a view that is taking typing,
where the keys the box advertises are that view's: it says so on the status line instead. A gesture
already waiting is thrown away the moment a view takes the keyboard, for the same reason.

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ Where g goes                                                                 │
│ > g1   Issues                  on screen                                     │
│   g2   Board                   Boards need a Jira Software project, and t... │
│   g3   Backlog                 Boards need a Jira Software project, and t... │
│   g5   Releases                                                              │
│   g i  An issue by key or URL                                                │
│   g s  Settings                                                              │
│                                                                              │
│ In this view                                                                 │
│   g g  first row                                                             │
│   g e  last row                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

What it holds, and why each half is there:

- **Every slot a view claimed, in slot order, from the registry.** Not a list written down: a view
  moved to another digit cannot leave this teaching the old one.
- **Every gesture the prefix completes on its own** — `g i` to an issue by key or URL, `g s` to
  settings. These are globals rather than slots, and they come from `Model.prefixGestures`, which is
  the same table `resolvePrefix` dispatches from and the footer advertises. One table, three readers:
  a gesture cannot be added to the dispatcher and missed by the two surfaces that teach it, which is
  what happened to `g s` — it worked from the day it was bound and appeared nowhere. A row whose view
  this build does not have is dimmed with the reason, exactly as an unreachable slot is.
- **A view out of reach keeps its row and carries the probe's own sentence**, because a negative
  capability is an answer with a reason attached and not an omission — the rule the rest of this
  document already follows. Plans is the usual case and the boards are the other. The cursor skips
  such a row, and its digit still answers with the reason on the status line, where the sentence is
  not cut off. At 80 columns something has to give and the order is fixed: a destination is never
  dropped to fit a reason, and a reason is cut rather than wrapped, because the box is sized by its
  widest row and a wrapped row's second line would be outside the zone a click resolves through.
- **What the focused view spends the same prefix on**, so that `g g` and the rest are taught by the
  overlay rather than shadowed by it. They are read off the view's own key set — a two-stroke gesture
  is spelt as the label of the binding it lands on, `g g` on a binding whose stroke is `home` — so a
  view teaches this by registering its keys and by nothing else. A binding that answers to a stroke
  and to a gesture lists both, the way a binding on two strokes already does: `G / g e`, `↑/k`. The
  block names the half that is a gesture on this prefix, and `?` shows the whole label. Where the
  terminal is too short for all of them the block folds to a `+N`; the destinations do not fold.
  **`ge` was the gap this exposed**: the issue list, the detail pane, the comment thread and the
  board all answer it in the branch behind their own `g`, and none of them registered a binding that
  said so, so neither this overlay nor `?` could teach it. All four now spell it on the binding it
  lands on. The backlog and the version list answer `gg` only, and say only that.
- **The row you are on**, marked, so the overlay teaches its own gesture by example: the cursor opens
  on the view that is up.

The digit still switches on its own, `enter` goes to the row under the cursor, `↑`/`↓` and `k`/`j`
move it, and `esc` throws the gesture away and draws nothing. **Every other key resolves exactly as
it did before the overlay existed** — the view gets both strokes in the order they were typed — which
is what keeps this a hint over a pass-through rather than a mode to get out of. A click on a row is
the same as pressing its digit; a click anywhere else cancels.

A view that cannot be built without knowing what it is about is reached from the view that knows.
The comment thread is the case: the issue detail pane draws it in its own sidebar and `C` hands the
kernel **that same model** for the whole screen, so `esc` lands back on the issue with the thread on
the comment it was left on and the draft still in it; the palette's *comment on this issue* is the
same gesture reached without the key — it is a broadcast, because the palette knows which command was
run and never which issue is on screen. Nothing offers to open the thread with no issue behind it. A
pane that has to say "open an issue and come back" is a dead end when nothing can come back to it,
and `kernel.Open` on such a view is how one gets built.

**`tab` moves the keyboard between panes**, in the one view that has more than one: the issue detail
pane, whose description, fields and thread each take it in turn. At 90 columns and up all three are
on screen and `tab` moves which one a motion is aimed at; below that there is room for one at a time
and `tab` is what brings the next one up. `shift+tab` goes back round. The gesture belongs to the
view rather than to the kernel, because what a pane is differs per view — and it is on the footer's
action row rather than among the motions, because in the narrow mode it is the only way to reach two
of the three regions at all.

**And `<`, `>` and `=` move the boundary between them**, in the same view. The split used to be
computed — a third of the pane, held between 35 and 45 cells — so a reader who wanted more prose, or a
wider sidebar for a long custom field, had no way to ask. Now the boundary can be dragged and the
three strokes move it four cells at a time, `=` gives it back to the width, and the choice is kept as
a **share of the pane rather than a column count**, so it means the same thing in a window of another
size. Two floors hold it: 53 cells of prose, below which the same paragraph loses about two words a
line, and 35 for the sidebar, which is the label column plus a value somebody can read. At 90
columns those two meet, so there is exactly one legal split there and the gesture says so rather than
pretending to move — as it does below 90, where the regions take the screen in turn and there is no
boundary on it at all.

The three strokes are on the `?` overlay and not on the footer's action row. The row names what can
be done to the issue in front of you, the split is a property of the window rather than of the issue,
and below the breakpoint the keys have nothing to move — a row naming them there would name keys that
answer with a refusal, which is the failure principle 2 describes rather than a smaller version of it.

**The ratio is kept per machine, not per profile.** It goes in `ui.toml` under the cache directory,
beside the palette's own frecency table, for the reasons that directory already holds it: a pane width belongs to the terminal it was chosen in and not to a Jira
account, so two profiles on one machine want one answer and a `config.toml` handed to somebody else
should not carry your proportions.

Jumping to an issue by key has all three of principle 3's routes now. **From the command line**:
`saral PROJ-142` opens that issue over whichever root would have opened, and so does a pasted browse,
board or backlog URL — a URL for another site is named as a mistake rather than read against this
one, because the same key usually exists on both. **`g` then `i`, from anywhere in a running
session**, opens the palette already armed to read what gets typed next as a key or a URL, the same
way `ctrl+k` does — `g` is the prefix the view slots sit behind, and `i` is what completes it for a
key. Not `k` or `j`: the destinations overlay this same prefix opens already spends both, and
`up`/`down`, moving its own cursor. **From the palette itself**, typing or pasting either is read the
same way as it is typed, whether or not the issue has ever been cached: a key already on disk is
found by the fuzzy index as before, and a key or a URL that parses but is not cached is offered
anyway, seeded with nothing but its key, so opening it fetches it fresh the way the CLI argument
does. `app.ParseKey` and `app.ParseIssueURL` are what all three routes read with, and the parse is a
**shape** and never a claim the issue exists — the project key charset is per-instance — so what is
not there comes back from the site, in the site's words.

## Mouse

Bubble Tea v2 declares mouse mode in the view; hit-testing is by zone lookup, not coordinate
arithmetic (see `docs/ARCHITECTURE.md`). This table is what the program does.

| Gesture | Result |
|---|---|
| click a row | select it |
| double-click a row | open it, same as `enter` — and only when both clicks are one gesture |
| click a status, type or assignee cell | filter by that value; click it again to drop it |
| click a value's name inside a chip under the rows | drop just that value |
| click a chip's `×` | drop the whole facet the chip names |
| wheel | scroll the pane under the pointer, not the focused one |
| drag a card within its column, or a backlog issue within its section | rank it beside the card it was dropped on, the same as `K`/`J` |
| drag a card to another column | move it there, the same as `m` and `enter` |
| click a swimlane's header | fold or unfold that lane, the same as `z` |
| drag the column between two panes | move the boundary; the panes follow the pointer and the ratio is kept |
| click the line that names the search | show its JQL and offer to change it, the same as `e` |
| click the footer's root cell | go back to that root, the same as `esc` from a pushed view |
| click a footer action | do it — the view is handed the first stroke of the key that entry names |
| right-click the body | open the menu of what can be done to what is in front of you: the footer's action cell, in full, with the descriptions the row had no room for |
| click the footer's `+N` | open `?`, which lists what did not fit |
| click the footer while `?` is up | close the overlay — the one entry the row has there |
| click anything else a view draws | do what it says — write, send, delete, confirm, put aside, pick a value, go back to an onboarding step |

**A double-click is timed, because nothing else can time it.** `tea.MouseClickMsg` carries a
position, a button and a modifier: no click count and no instant. Every view here first reached for
"a second click on the row that is already selected", which cannot tell a double-click from two
deliberate clicks a minute apart, so pointing at an issue and then pointing at it again opened it.
`widget.Clicks` times the pair against `Deps.Now` — the clock a test injects — over a 400 ms window,
and a slower second click only re-selects.

**Narrowing by a cell is one gesture of the filter picker below**, not a mechanism of its own. A
click on a status, type or assignee cell puts that value in force by the id the row carries; the same
cell again takes it off. The bar under the list draws one chip per facet, its values comma-joined: a
click on a value's name drops that value and a click on the chip's `×` drops the whole facet, both
through the id the row or the picker carried rather than the name on screen. The palette carries
*filter by this row's status / type / assignee* and *drop every filter on these issues* for anyone
without a pointer, and `f` reaches every value rather than only the ones a loaded row happens to
carry. It composes with `/`: a term is what the site was asked and the filter is what is kept of the
answer, so a row has to survive both.

**And `/`'s own filter is named the same way once it has been accepted.** `esc` closes the prompt and
keeps the filter, so a kept filter gets its own line under the rows. `ctrl+g` clears both it and any
terms in force — one key rather than two to learn — and the palette carries *clear the filter on these
rows*. The footer offers `ctrl+g` whenever there is a term or a filter to clear. `esc` does the same:
in a root view it is the kernel's, and clears only the status line, unless the view implements
`kernel.BackClaimer` and its `WantsBack()` says yes — which the list does exactly while something is
narrowing its rows, the board and the backlog while a term is in force, and the board while cards
are picked, which esc lets go of before it clears a term (not while a card is in hand or a move is
being chosen).

**The divider is a column of blank, and it is deliberate that it stays blank.** The boundary between
the issue pane's description and its sidebar is one column wide and carries no rule, because the
sidebar's own gutter is the column next to it and two vertical lines side by side say less than one
does. What tells a reader the boundary moves is `?`, the palette and the row in the table above, not
a glyph.
Everything a pointer can do to it, `<`, `>` and `=` do without one.

**The context menu asks the focused view what applies, and does not invent a scope.** The gesture
used to be described here as *right-click a row → the actions valid for it*, which had no data source:
`kernel.Command` carries `Requires`, a capability key that answers "can this token do this on this
site?", and nothing that answers "does this apply to the thing under the pointer?" — so a menu built
from the command registry would offer *Write a comment* and *Set up a Jira profile* on an issue row
with equal confidence. Rather than adding a scope to `Command` and sweeping all five registrars, the
menu is built from the **focused view's `Acts`**: the view's own inventory of what can be done to the
thing it is showing, which is already what the footer's middle cell draws and already moves with the
view's state through `kernel.KeyReporter`. Choosing an entry delivers the first stroke of the key that
entry names, exactly as clicking a footer action does, so the key, the palette and the pointer stay
one implementation and cannot drift.

**The granularity is the row, and the row is the one the view has focused — not the one under the
pointer.** Only the view can turn a coordinate into a row: it owns the zones, and the kernel sees a
frame. A menu that guessed would offer *transition* and *delete* against the wrong issue, which is
worse than no menu at all. So the right-click is forwarded to the focused view before the menu opens —
a view that maps it to *select this row* makes the pointer and the menu agree, and none does yet — and
until then the menu is about the row the view draws highlighted. It is deliberately not per **cell**:
a cell is already a left-click gesture of its own (*filter by this value*), and a view has one focused
thing rather than one per column.

**It takes the body, the way `?` does, rather than floating at the pointer.** A box spliced into the
view's own lines would have to cut strings carrying the zone markers every click is resolved through,
and half the frame's mouse targets would quietly stop answering — a worse trade than a menu that is
not under the pointer. The row at the bottom says what the menu answers to while it is up, and
nothing — key, wheel or drag — reaches the view underneath, for the same reason nothing reaches it
under `?`.

A view with nothing in its `Acts` — a save in flight, a site being asked, an attachment pane with
nothing attached and no permission to attach — opens no menu and says so, because a gesture that
silently does nothing reads as a broken program. A view taking typing keeps the keyboard for the same
reason: the menu spends the arrows and enter, and eating the rest of a pasted token to offer a list of
things is not a trade. There is no keybinding for the menu, and inventing one is not on the table:
every entry in it is already a key and already a palette command, which is principle 3 satisfied
before the pointer is involved.

**Labels are clickable nowhere**, because no view that can filter draws them: the issue list's
columns are key, summary, type, status, assignee and updated, and the detail pane that does list
labels has nothing of its own to narrow. When a view draws a label it can narrow by, it marks the
label the way the list marks its cells.

Mouse mode must be disableable (`mouse = false` in config) for people who rely on terminal text
selection. Off means off all the way down: the zone manager is disabled with it, so a view's markers
are never written into the frame in the first place and there is nothing left for a selection to
pick up — and a view asks `Zones.Enabled()` before telling anybody to click something. The kernel
scans every frame either way, because a disabled manager strips the markers a row memoized while the
mouse was on, and it broadcasts `kernel.SetMouseMsg` when the setting flips so that a view whose memo
key does not already carry the mouse bit can drop what it drew. Nothing from the mouse — click, wheel,
drag or release — reaches the view while the help overlay is covering it, or at all below the minimum
size, where the frame is a sentence and every zone belongs to one no longer on screen.

## Filtering by a person, without writing JQL

The owner, after a week against a real site: *"From all views there is no easy way to go back to
filter by person. I do not want to write JQL queries — this should also belong to the speed and ease
of use."*

`f` opens a picker in two states: **choose a facet, then toggle any number of its values.** `enter`
puts a value in force or takes it off again without closing the picker, so a second assignee costs
another `enter` rather than a fresh trip through the facet menu; `esc` goes value → facet → closed.
The facets are assignee, reporter, status, type, priority and label. `ctrl+k` reaches it from
anywhere, including the issue pane, where `f` belongs to the viewport and is not taken away from it.

**Every list-shaped view answers `f`**: the issue list, the board, the backlog and the timeline. The
issue list sends a chosen value to the site as JQL, which is the next paragraph; the other three
narrow what they already hold, in memory, the way `board.terms` did since the board first grew
filtering — a board, a backlog and a timeline are already a whole read's worth of issues on screen,
and asking the site the same question again would be a second answer to compare against the one
already drawn. `ctrl+g` clears every term at once in all four, and the bar under what each view draws
is the same widget (`internal/ui/widget/filterbar`) reading the same `filter.Terms`, so the chips, the
key and the picker never disagree about what a term means.

**Version, component and sprint are not offered.** Not an oversight: none of the three can be read
through the port role a session holds, so a row for one would be a facet with nowhere to get its
values from. When the port grows a way to read them, they join the list.

**In the issue list, a value is a query, not a pass over the rows in hand.** A chosen value composes
into the JQL and the search runs again, so it reaches an issue this session never fetched, and it
matches on the id the site gave the value rather than on a display name, which is localised, is not
unique on one site, and which one account answered to two of within a minute. The board, the backlog
and the timeline match the same id against the issues already loaded instead, which is the local
narrow the issue list's own query cannot be — see *Every list-shaped view answers `f`* above for why
that split is the right one rather than a shortcut: a board's own read is already whole, and the three
views cannot see an issue the read never brought back, which is what the quick-filter warning on the
board says when more is loaded than is on screen.

**Terms compose, and the screen says what is in force.** Two facets narrow together; two values of
one facet widen it — a person *and* a status, either of two people. The bar under the rows draws one
chip per facet, its values comma-joined rather than one chip per value — a facet with three assignees
on it is one narrowing, not three — with `×` to drop the whole facet and a click on a value's name to
drop just that one. In the issue list, `a` — every issue in this project — is the no-terms state, so
dropping the last term lands exactly on it, and a project switch takes the terms with it and says so:
a status and an issue type are minted per project, so the ids in force name values the new project has
never heard of. `ctrl+g` clears every term at once in every view that filters, the same key that
clears a typed filter in the issue list.
**Checked against the tree**: the board, the backlog and the timeline do not drop a term on a project
switch the way the issue list does — a term stays in force naming a status or a type id the new
project may not use, silently narrowing to nothing rather than saying so. Filed as
[#141](https://github.com/varijkapil13/saral/issues/141) rather than widened into here, because the
fix is one shared behaviour across three views the issue list's own `reproject` does not have to
answer for — F3 only added the picker and the bar to all three.

**Where the values come from decides whether it feels fast.** Statuses and types come from the
project's workflows, priorities and labels from the site. All four are read once when the facet is
opened, and typing then ranks what is held with no round trip at all.

Accounts are the exception, and the port explains why: Jira's user matching is neither substring nor
fuzzy and is documented nowhere — `ap` finds nobody though a surname on the site contains it, and
`vk` finds a person by initials — so a needle two letters longer can match what a shorter one did
not. The picker therefore asks the site once when the facet opens, ranks what came back locally with
the same scorer the palette uses, and goes back to the site only when what it holds stops answering:
never while the first search returned the whole directory, and never twice for one needle. A request
per keystroke is slower than typing the JQL this replaces.

The assignee search is scoped to the project, which switches Jira to the assignable endpoint and
drops the app accounts for free — ten of the eleven accounts on the measured site. A **reporter** need
not be assignable, so that search is site-wide, and what it drags in is badged (*app*, *customer*,
*inactive*) and sunk below the people rather than hidden: an app account is assigned work and reports
issues exactly as a person does, so hiding one loses rows.

**Nobody is a value like any other.** *unassigned* is the first row of the assignee facet and
composes with the rest — it is the empty account id, so `assignee IS EMPTY` is what it writes and it
ORs with a named person rather than needing a filter of its own.

**A facet the site refuses says so and points at the way round.** *Browse users and groups* is
site-wide and an ordinary token can lack it; a session with no project cannot be told what statuses
exist. Either way the facet stays on the list with the reason beside it in the site's own words, and
choosing it repeats the reason and names `e`, which shows the search on screen and runs an edited
one. A facet that disappeared would be one nobody could find out about.

## Editing in place

The owner, on the separate edit screen P2.3 shipped: *"Why open a different view when I can edit the
thing directly in the same view?"* The Details sidebar is now the editing surface, and the pushed
field editor is gone.

**The sidebar has a row cursor.** `j`/`k`, the wheel and a click all move it, and every row the
sidebar draws — a platform fact, a related issue, a custom field, editable or not — is one of its
stops. `enter` or `e` acts on the row under it: an editable row opens in place, and a read-only one
answers with the one word that is true of it, `read-only`, rather than doing nothing. A row is
editable when the issue was read with its field (`Issue.Requested`, the same mask P2.3's fetch-edit-PUT
cycle already answers to) *and* editmeta lists it *and* its kind is one this build can edit — see
`docs/FIELDS.md`. This build's kinds are text (the summary), labels, date (the due date), the document
the description is, opened from the description region itself rather than from a sidebar row, single
choice (priority), person (the assignee), status, through the issue's own transitions — see
"Assigning, and changing status" below — and the custom fields the issue's screen lists, below. Status answers for itself rather than through editmeta, since a
transition is a workflow action and never a field a screen lists.

**Committing a row is not sending it.** Enter on a text, labels or date row swaps its value for a
`bubbles/textinput` seeded with what is there now; enter again keeps the typed value on the row and
closes the field, `esc` leaves it alone. The description opens the same `bubbles/textarea` the comment
composer uses, inline in the description region below the header, closed with `ctrl+s` to keep it or
`esc` to put it aside: the text stays on the row and in the draft (written a moment after typing
pauses), `e` reopens the editor on it, and `x` throws it away. Its first line names what editing this
document as markdown costs (`adf.LossyConstructs`) before a key is typed. `E` still hands the same
document to `$EDITOR`, whose value is split the way a shell would, so a quoted path with a space in it
works; the file opens with an HTML comment naming the same costs, taken off again on the way back.
None of this writes to Jira — it marks the row **dirty**, on the pane, and nowhere else yet.

**Dirty rows accumulate into one set**, not one request per field. A dirty row carries a bullet in the
theme's accent colour and, where the line has room, the value the site still holds beside it as
`(was …)`. The identity line under the issue's title — the one place on this pane that survives a
keypress the way `docs/UX.md`'s own status-line rule asks for — names the set: `N unsaved · s save ·
x revert this · X revert all`, each word a click target as well as a key. `s` (and `ctrl+s`, and
the palette's *Save changes*) sends every dirty row as **one** `IssuePatch`; a field this pane cannot
express — an empty summary, an unparsable date — is refused before anything is sent, and a rejection
Jira does send back is shown in that field's own words rather than as a status line nobody can act on
after the next keypress. `x` (not `u`: this pane already spends `u` and `ctrl+u` on half a page up)
reverts what the keyboard is on — the description while its region has focus, the row under the
cursor in the details region, and nothing in the comments, which say so. `X` (and the palette's
*Revert all changes*) reverts every row at once. `backspace` and `U`, the keys these used to be, still
work for one release and are named nowhere.

**Leaving with dirty rows asks, everywhere the kernel would otherwise refuse.** `kernel.CloseAsker` is
the additive interface that makes this possible: a `Blocker` that also answers to it is asked instead
of refused, at every one of the three places a view is discarded — a pop, a root switch, and quitting
from a lone root — as long as it is the view on screen. The issue pane answers by putting up its own
named prompt in place of the identity line's facts — `Save N changes to PROJ-12?  y save · n discard ·
esc stay`, each word a zone too — and resolves it itself: `y` saves and then sends `kernel.Proceed()`,
`n` discards and sends it straight away, `esc` puts the prompt away and changes nothing. The kernel
holds the gesture it asked about and `Proceed` replays it, so `g2` over a dirty pane answered `n` lands
on the second root rather than only leaving the pane. With a thread lent over the dirty pane, the pane
is not on screen to ask, so the switch is refused with its reason instead.

**The dirty set survives a crash.** Every commit to a row writes the whole set through the same
`draftStore` P2.3 built — one file per issue per site, replaced atomically — and it is picked back up
the next time this issue is opened, with the identity line saying so: `unsaved changes from earlier
restored · s save · X discard`.

**A save never overwrites what it did not see.** Jira Cloud answers a plain `PUT` with 204 whatever
changed in between, so the pane checks for itself. Each edit keeps the base it was made against — the
issue's `updated` stamp and a fingerprint of each field it writes (`app.EditBase`), in memory and in the
draft — and the save re-reads exactly those fields through the issue endpoint first (`app.SaveIssue`).
A field that moved is a `*jira.ConflictError` before anything is written, answered by reading the issue
again and rebasing the dirty set on top; every row the site changed underneath is marked in its own
words and takes the fresh read as its new base, so the next `s` is a reviewed save. Any full read does
the same — a draft restored days later, `r`, a transition carrying the dirty set. Labels are the
exception: they go out as add and remove operations diffed against the list the edit began from, so a
label somebody else added survives and cannot conflict. The read after a save goes through the issue
endpoint too, never through search, whose index trails the write.

**Custom fields the screen lists are rows too.** Every custom field editmeta names with a `set`
operation gets a row whose editor comes from its schema, never its id or name: text and URL fields
type into the row's input (a URL must be a whole address), numbers, dates (`2006-01-02`) and dates
with a time (`2006-01-02 15:04`, in the account's zone) are checked when `enter` keeps them, labels
are typed comma-separated, a paragraph field opens the description's textarea with its name above
it, and select, cascading select (`Parent / Child`), multi-select and people fields open an inline
list — the screen's own `allowedValues`, or a site search for a person. A multi-select or people list
toggles with `enter` and closes with `esc`; a single one offers `None` unless the screen marks the
field required. An empty row is sent as a clear. An editable field with no value is drawn as `not
set`, in the screen's order, so it can be filled in. A value in a shape the editor does not write back,
a field with no `set` operation and a shape with no editor stay a static line.

**Typing `@` offers people.** In the description, a paragraph field, the comment composer and a
create form's document field, `@` followed by part of a name asks the site (`FindPeople`, once typing
pauses) and lists who matches above the text; `up`/`down` move, `enter` or `tab` writes
`@[Name](accountid:…)`, which the markdown reader turns into a real mention node, and `esc` closes the
list until the next `@`. An `@` inside a word, as in an email address, opens nothing. A token without
*Browse users and groups* says so in the list and asks nothing.

**A restored comment edit is checked against the body it was written on.** An edit's draft keeps a
fingerprint of the comment it started from. When the comment has changed on the site since, opening
the draft says so, and the first `ctrl+s` only acknowledges it; the second sends. A draft with no
fingerprint, from an older build, is treated the same way.

## Assigning, and changing status

Priority, the assignee and status are rows too, and each opens an inline list directly beneath itself
rather than a text field, because none of the three is free text. The list takes the
keyboard while it is open (`esc` cancels it, `enter` chooses the row under its own cursor, a click
does the same) and filters as it is typed into.

**Priority** reads its candidates from editmeta's own `AllowedValues` for the field — the same read
`fetch()` already made, so opening this list costs nothing further. **The assignee** searches the site
as it is typed, because `jira.PeopleQuery`'s own matching cannot be reproduced locally: `Me()` (asked
for once and kept for as long as the pane is open) and "Unassigned" are offered before anything is
typed and before the site has answered anything at all, and a keystroke re-issues the search rather
than narrowing what is already held. `@` opens this list from wherever the cursor already is, and the
palette's *Assign to…*, *Assign to me* and *Unassign* reach the same three gestures without opening a
list at all for the latter two. A token without *Browse users and groups* is told why in the
capability's own words rather than shown an empty list.

**Status is a workflow action, not a value waiting on `s`.** Choosing a transition off its list asks
for any field the transition's own screen requires, and then a named confirmation — *"Move PROJ-12 to In Progress and save 2 changes?"* — naming the move and
however many other rows are dirty at the same time, because `Transition` takes fields exactly as
`UpdateIssue` does and a status change carries the rest of the dirty set in the one request rather than
two. `t` (and the palette's *Change this issue's status*) open it from wherever the cursor already is.

**A card dropped on the board whose move needs a screen** opens the issue pane on that very move:
the board pushes `issue.New(…, issue.WithTransition(id))`, which opens this same status list and, once
the issue's moves have been read, chooses that transition, so its screen is the first thing on screen.
A move the site no longer offers by then says so in the list rather than choosing another.

**The header's facts are the same three doors.** A click on the status, the priority or the assignee in
the facts line under the title opens the list its sidebar row opens. The sidebar draws the type as a
row of its own and the status and priority with their icons, and an issue opened by key alone shows
each fact's icon with a placeholder until the read lands — `unknown` if it never does.

## Working a board and a backlog

The board and the backlog answer the same few gestures with the same keys, so a hand that learnt one
has learnt the other. Every one of them is also a palette command and, where there is something to
point at, a pointer gesture.

| key | board | backlog |
|---|---|---|
| `K` / `J` (or `shift+↑` / `shift+↓`) | rank the card above the one before it, below the one after it | the same, within the section |
| `{` / `}` | rank it first or last in its column | first or last in its section |
| `H` / `L` (or `shift+←` / `shift+→`) | move the card to the previous or next column, in one stroke | — |
| `M` | only my issues | only my issues |
| `/`, then `n` / `N` | find a card by key or words of its summary, then the next and the one before | the same over the rows |
| `c` | create an issue in the column under the cursor | create an issue in the section under the cursor |
| `w`, `z`, `Z` | swimlanes: none, by assignee, by parent; fold the cursor's lane; fold or open every lane | — |
| `space`, `v`, `x` / `esc` | pick a card, pick the whole column, let go of every pick | pick an issue, pick the section, let go (`x`) |
| `@`, `+`, `m` | assign, add a label to, or move every picked card — or the card under the cursor when none is | `m` moves the picked issues |

**A rank is drawn before the site answers and taken back if it refuses.** The site's own order lags
a rank write (`docs/API-NOTES.md`), so nothing re-reads to confirm: the card moves on screen, the
write goes out naming the card now beside it, and a refusal — a token without *Schedule Issues*, a
rate limit, a transport failure, or a 207 naming the card — puts it back before the card that followed
it and says why. Steps taken while one is out are not sent beside it: the next goes once the first is
answered, naming where the card is on screen by then, so `K K K` cannot reach the site out of order.
A board with no rank field is ordered by its filter and says so rather than pretending to move a
card, and so does a backlog sorted by a field of its own: a rank there would not show where it went.
`}` waits for the rest of a board still loading, because the last card loaded is not the last card.

**`H` and `L` are `m`, an arrow and `enter` in one stroke.** They go through the same transition
read, so a move whose screen needs a field opens the issue pane on that move exactly as a drop does.

**`M` is a term, not a mode.** It puts the account this session is signed in as in force as the only
assignee — replacing any other person already there and leaving the other facets alone — so the chip
bar names it, `ctrl+g` clears it and a second `M` takes it off. The account is asked for once, the
first time.

**`/` walks what is loaded.** Typing moves the cursor to the first match from where the search
began; `enter` keeps the search for `n` and `N`, which wrap at either end, and `esc` puts the cursor
back where it was and forgets it. Like `f`, it cannot see a card the read has not brought back yet.

**A Scrum board's running sprint gets a line of its own** under the board's name: its goal, how many
days it has left in the site's time zone, and a bar of how much of it is done. Done is the board's last
mapped column, never a status category, and the bar counts the board's estimate where the board
estimates and any card carries one, cards otherwise. Each column's rule already carries its estimate
total; the backlog puts each section's total on the section's head, in the estimation field's own name.

**Swimlanes group the rows, never the columns.** `w` steps through none, by assignee and by parent,
and the choice is kept per board. The parent is the issue's own parent field, which is where both a
company-managed epic and a team-managed parent arrive, so nothing reads an epic-link custom field.
People are ordered by name and parents by the first of their cards the board ranks; the lane of
nobody's cards — *Unassigned*, *No parent* — comes last. Each lane's header names it and counts its
cards, and is a zone a click folds. A folded lane keeps counting in its column's caption, the estimate
under it and the count on the top line; its cards leave what `j`, `k` and `/` walk until it is opened
again. A lane a term empties is not drawn at all. A rank stays inside the lane: `K` on a lane's first
card says so rather than ranking it past a card of another lane, and a drag from one lane to another
is refused, because what moves a card between lanes is its assignee or its parent, not its rank. The
lane headers are drawn inside the grid's own memo, so a steady frame costs the frame string whether
lanes are on or off.

**`c` creates where the cursor is.** It opens the create form already answered: the project and issue
type of the card under the cursor (never a subtask type, which needs a parent the column cannot give),
and on a Scrum board the sprint on screen, which the form's heading names. The form reports the issue
it made back to the view that opened it. The site creates every issue in the backlog, so the board
moves it into the sprint on screen, then through the workflow move into the column when it was created
in another one — or into the issue pane when that move needs a field, as a drop does — and reads it
back. A column no move reaches is said so, with where the issue is instead. A refusal at any step says
how far the issue got. The backlog does the same for a sprint section and nothing for its own. The
site's index trails a create by seconds, so a re-read that has not caught up does not take the new
card off again.

**A bulk change is asked for, confirmed, then run one card at a time.** `space` picks the card under
the cursor and steps on, `v` picks the whole column (or lets it go when it is all picked already), and
`x` or `esc` lets every pick go. `@` asks who: nothing typed offers this session's account and nobody,
anything typed is asked of the site. `+` asks for a label, which cannot contain a space. `m` with
cards picked takes them all in hand, aimed with the same `h`/`l`. Each ends on a named confirmation —
*assign 3 cards to Grace Hopper?* — that only `enter` or `y` runs. The run shows how far it has got,
refuses every other key, and `ctrl+g` stops it after the card in flight. A move reads each card's own
transitions, so a card whose workflow does not reach the column, or whose move needs a field, is
reported rather than guessed at. An assignee is saved against the one the card was read with, so a
change somebody else made in between is a refusal and not an overwrite; a label is added, never
replaced. The report names what changed and which cards did not and why; the cards that did not stay
picked for the same gesture to try again. Quitting mid-run is held: the run stops after the card in
flight and the quit goes ahead once it has answered.

## Around an issue: sharing, links, time, watchers, copies

**Sharing is the same three keys wherever an issue is under the cursor.** `y` copies the key, `Y` the
browse link built from the profile's site, and `o` opens that link in the desktop's browser — in the
detail pane, the list, the board and the backlog alike, and from the palette as *Copy this issue's key*, *Copy the
link to this issue* and *Open this issue in the browser*. A copy names what it copied, because OSC 52
cannot confirm one landed.

**Links, time and watchers are sheets pushed over the pane**, one list with one prompt under it, so
`esc` comes back to the fields exactly as they were. Each change is written at once and the pane
underneath rereads the issue.

- `L` lists the links under the phrase that relates them. `a` asks for a phrase — either direction of
  every link type the site has, filtered as it is typed — and then the issue at the other end: a key,
  a pasted URL, or words from an issue already cached on this machine. `d` removes the link under the
  cursor after a *y*, and `enter` opens the issue it points at.
- `w` lists the time logged, newest first, under the issue's logged, remaining and estimated time —
  the same wording the sidebar's Time row uses. `a` (or `w` again) asks how long (`1h 30m`, `90m`,
  `1.5h`), when it started (a date, a date and time, or nothing for now, in the account's timezone)
  and what it was. Days and weeks are refused: their length is the site's working day, which the
  client does not read.
- `W` shows who watches, and says so when the token may see fewer people than the count. `w` watches
  or stops watching as yourself, `a` searches the site's people to add someone, `d` removes the person
  under the cursor; both of those are the Manage Watchers permission, and a refusal says so.

**Clone** is a palette command, *Clone this issue*. It reads the issue whole and its project's create
screen, carries over every field that screen takes (and lists them), and asks for the copy's summary,
`CLONE - ` and the original's to begin with. An issue with links asks whether to copy them too, each
the same way round. The copy then opens in place of the sheet.

## Rendering rules for modern terminals

- **True color when available, 256 and 16 as graceful steps down, and a real no-color mode** driven
  by `NO_COLOR` and `TERM`. **The stepping down is the library's, not ours.** Bubble Tea detects the
  terminal's colour profile at start-up with `colorprofile` and its renderer downsamples every colour
  to what the terminal can show, so a theme is written once in true colour and arrives correctly on a
  16-colour `xterm`. Nothing here quantises a colour, and a packet that adds a mechanism for it is
  adding a second answer. What *is* ours is the no-colour mode, because that is a decision rather than
  a capability: `kernel.ThemeModeFromEnv` reads `NO_COLOR` and `TERM`, both beat the configured theme
  and the runtime switch, and the resulting theme keeps bold, faint and reverse — `NO_COLOR` asks for
  colour to go away, not for emphasis to.
- **A Nerd Font is a tier to opt into, not an assumption.** Icons come from a three-tier glyph set —
  Nerd Font icons, plain Unicode box-drawing and geometric shapes, then ASCII — and `unicode` is the
  default, because no terminal reliably says whether the font is installed and a wrong guess shows
  tofu where every icon should be. `--glyphs nerd`, or the settings screen's Glyphs row
  (`docs/SETTINGS.md`), turns the icons on.
- **Grapheme-cluster-correct widths.** Emoji, CJK and combining marks must not shift columns. Use a
  width-aware truncation helper everywhere; never `len()` on a display string.
- **Resize is not a redraw hack.** Layout is computed from the current size on every `WindowSizeMsg`,
  and the minimum usable size is 80×20 with a legible message below that rather than a broken frame.
- **A region says where it is in one column, not one row.** A pane with more than one region gives
  each its leftmost column as a rail: the theme's accent where the region has the keyboard and its
  muted token where it does not, with a scrollbar thumb at the proportional position and no thumb
  when the content fits. It costs a column instead of a title bar's whole row, it says "there is more
  below" for every region at once, and in `no-color` the thumb's position carries the meaning where
  the hue cannot.
- **Inline graphics are optional.** Kitty protocol, then iTerm2, then chafa half-blocks, then text.
  Detect once at startup and cache the answer.

## Status and feedback

- Long operations show progress with a real number when the API gives one (attachment bytes, bulk
  move task percentage) and elapsed time when it does not.
- Rate limiting is shown as a countdown, not an error, and any poller pauses itself.
- Stale data is badged rather than hidden. Seeing yesterday's board beats seeing nothing.
- **A refresh says what came back, including when that is nothing.** `r` and `R` are the two keys
  whose whole job is invisible when the answer has not moved, and a refresh that reports nothing is
  indistinguishable from one that never ran — which is exactly how a working `r` was read as broken.
  So the status line names the outcome in the words of the thing asked for (*refreshed* against
  *refetched from scratch*, since `R` does strictly more), and the summary line keeps the time the
  rows last came from the site, because a status line goes away and a question about how old the
  screen is comes back.
- Errors state what failed and what to do. `403` becomes "You need the Bulk Change permission to move
  issues between projects", which is the capability `Reason` verbatim.
- **A warning and a failure differ by more than colour.** The status line prefixes the tier's `Warn`
  glyph to a warning and its `Cross` to a failure, so `NO_COLOR`, which draws both in the same bold,
  still tells them apart.
- **The status line is transient, so nothing that has to persist may live only there.** It is one
  line, it is overwritten by the next thing that happens, and a keypress clears it. Anything that is
  still true after that keypress belongs in the pane as well: a stale badge, a refusal, a count.
- **An empty pane says which kind of empty it is, in words, and keeps saying it.** There are five,
  and a user cannot act on the difference unless the screen names it: no site in this session,
  nothing asked of it yet, a search in flight, an answer with no rows in it — worth naming the JQL —
  and a search that failed, which also carries the reason and the key that tries again. All of them
  drew "Searching…" once, so a wrong project key, a bad JQL, a dead host and a rate limit were one
  screen that looked like a hang.
- **A message the user has to read must fit the terminal they have.** A sentence that leads with a
  method, a path and then the same URL again is truncated before it says what went wrong, which is
  worse than saying less: the endpoint is worth one mention, and the reason goes where it survives a
  narrow window. Where the reason cannot be shortened, the pane wraps it rather than cutting it.
