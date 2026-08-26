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
| First week | Frecency: projects, assignees, versions and labels reorder so your usual choices are first. |
| | Hints: after you reach an action through the palette three times, the status line notes its key. Built in P3.1 ([#12](https://github.com/varijkapil13/saral/issues/12)) rather than with the footer: the count, the call site and the frecency table are one piece of data, and P3.1 already owns it. `kernel.CommandRanMsg{ID, Keys}` is the signal it hangs on, and `Command.Keys` is the key it names — a command nothing binds is never given one, and the line is said once rather than on every run after the third. |
| Ongoing | JQL history with fuzzy recall; saved queries bound to `1`–`9` and kept in the profile. |
| | Local fuzzy index — typing a key or a few words of a summary finds it with no round trip. Reached from the palette: `ctrl+k` and a few letters offers the issues already on disk beside the commands, ranked by `app.Index` over `app.Cache`. |
| | Session resume: reopening lands exactly where you left, including scroll and filter. |
| Fluent | `saral PROJ-142`, `saral board PROJ`, piping a JQL query in, scriptable subcommands for the rest. |

Frecency is a plain local table of `(item, count, lastUsed)` scored `count * decay(lastUsed)`. No
telemetry leaves the machine, ever.

The palette keeps its own table in a file of the palette's under the cache directory, beside where a
comment draft goes: command IDs from this build and two numbers each, nothing from any site. It is
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
| globals | `? ctrl+k esc`, or `q` at the bottom of the stack — bare keys, and never given up |

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

**The motions are not on the row.** The issue pane answers sixteen strokes and thirteen of them only
move the cursor; a row that lists them in the order they were declared spends itself on scrolling.
`?` lists them, and it leads with the actions — spelt out, because the overlay has room for *edit
fields* where the row had room for *edit*. Every key appears there exactly once.

**Every entry on the row is clickable**, and a click is delivered to the view as the first stroke of
the binding it advertises — so the key, the palette entry and the pointer are one implementation and
cannot drift. `+N` opens `?`, which is where what it stands for is listed.

The digits keep their place on the row where they work: in a root view the bound saved queries are
the first entry in the actions cell, named after the query. **The view slots are not on the row.**
One row cannot hold nine destinations and the actions as well, the destinations are the half needed
least often, and the header already says what is on top — so `g1`–`g9` are taught by `?` and by the
palette's *Go to* rows, and the row names only the root you are in.

The theme is switched from the palette — *use the dark theme*, *follow the terminal's own colours* —
and the choice is written back into the profile it came from. There is no key for it: every letter
left is one somebody types into a field.

## Navigation model

A stack, not a graph — so "back" always means something.

```
run a saved query      1 – 9          in a root view; the profile's own searches
switch view            g then 1–9     from anywhere, including a pushed view
push                   enter          open the thing under the cursor
switch pane            tab            in a view with more than one; shift+tab goes back round
pop                    esc            back, never quits from a pushed view
quit                   q / ctrl+c     only from a root view, and only when nothing on the stack
                                      is holding a draft
palette                ctrl+k         everything, fuzzy; opens over what you were in, esc returns.
                                      the one global a view taking typing cannot swallow
search in view         /              filter rows live
clear that filter      ctrl+g         from the browsing state; esc does it while still typing
filter by a value      f              pick a facet, then one of the values this site holds
every issue here       a              widen the search to the whole of the session's project;
                                      also the state with no filter values in force
edit this search       e              show the JQL on screen and run an edited one
save this search       s              bind the query on screen to a number key
refresh                r / R          current view / purge and refetch. both say what came back
```

Vim keys and arrows are both always bound. `j/k` and `↑/↓` are not a preference to configure.

### What happens to a view you leave

Three of these gestures take a view off the screen and they do not mean the same thing, which is
visible in what happens to whatever it was fetching.

| Gesture | The view you were in | Its in-flight read |
|---|---|---|
| `ctrl+k`, or anything else pushed over it | still there, underneath | carries on |
| `g` and a digit, to another root | kept, and comes back on its own digit with the same row under the cursor | carries on |
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
allocated here rather than picked, and `?` and the palette are where its digit is taught.

| Slot | View | Arrives with |
|---|---|---|
| 1 | issues | P1.5 — shipped |
| 2 | board | P6.3 ([#24](https://github.com/varijkapil13/saral/issues/24)) |
| 3 | backlog | P6.3 |
| 4 | sprints | P6.3 |
| 5 | releases | P5.1 ([#20](https://github.com/varijkapil13/saral/issues/20)) |
| 6 | timeline | P8.2 ([#27](https://github.com/varijkapil13/saral/issues/27)) |
| 7 | plans | P8.3 ([#28](https://github.com/varijkapil13/saral/issues/28)) |
| 8, 9 | free | — |

Everything else registers `Slot: 0` and is reached by being pushed, by name or from the palette:
issue detail, onboarding, the palette itself, forms, comments, attachment preview and the move
wizard. `kernel.RegisterView` refuses a second claim on a slot at startup, so the table above is
enforced rather than merely written down — but it is written down so that six later packets do not
each pick a number.

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
line, and 34 for the sidebar, below which a label and its value stop fitting side by side. At 90
columns those two meet, so there is exactly one legal split there and the gesture says so rather than
pretending to move — as it does below 90, where the regions take the screen in turn and there is no
boundary on it at all.

The three strokes are on the `?` overlay and not on the footer's action row. The row names what can
be done to the issue in front of you, the split is a property of the window rather than of the issue,
and below the breakpoint the keys have nothing to move — a row naming them there would name keys that
answer with a refusal, which is the failure principle 2 describes rather than a smaller version of it.

**The ratio is kept per machine, not per profile.** It goes in `ui.toml` under the cache directory,
beside where a comment draft goes and where the palette's own frecency table is to go, for the
reasons that directory already holds them: a pane width belongs to the terminal it was chosen in and not to a Jira
account, so two profiles on one machine want one answer and a `config.toml` handed to somebody else
should not carry your proportions. It is also what onboarding would have eaten — setup rebuilds
a profile from a zero value and drops everything it did not collect
([#63](https://github.com/varijkapil13/saral/issues/63)), so a number kept there would go the next
time anybody re-checked a token. That bug is neither fixed nor made worse here; the split is simply
not in its way.

The one thing this table still does not promise is jumping to an issue by key — typing `PROJ-142`, or
pasting a Jira URL. That is not built; it is
[#62](https://github.com/varijkapil13/saral/issues/62).

## Mouse

Bubble Tea v2 declares mouse mode in the view; hit-testing is by zone lookup, not coordinate
arithmetic (see `docs/ARCHITECTURE.md`). This table is what the program does.

| Gesture | Result |
|---|---|
| click a row | select it |
| double-click a row | open it, same as `enter` — and only when both clicks are one gesture |
| click a status, type or assignee cell | filter by that value; click it again to drop it |
| click one of the chips under the rows | drop that filter |
| wheel | scroll the pane under the pointer, not the focused one |
| drag the column between two panes | move the boundary; the panes follow the pointer and the ratio is kept |
| click the line that names the search | show its JQL and offer to change it, the same as `e` |
| click the footer's root cell | go back to that root, the same as `esc` from a pushed view |
| click a footer action | do it — the view is handed the first stroke of the key that entry names |
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
cell again takes it off; the chips under the list say what is in force and a click on one drops it.
The palette carries *filter by this row's status / type / assignee* and *drop every filter on these
issues* for anyone without a pointer, and `f` reaches every value rather than only the ones a loaded
row happens to carry. It composes with `/`: a term is what the site was asked and the filter is what
is kept of the answer, so a row has to survive both.

**And `/`'s own filter is named the same way once it has been accepted.** `esc` closes the prompt and
keeps the filter, and after that `esc` belongs to the kernel, so the count reading `1 of 3` was the
only trace a filter was on at all and the only way off it was to open it again. So a kept filter gets
the line under the rows too, `ctrl+g` clears it — the stroke `esc` already answers to while the
prompt is open, which is why it is not a new key to learn — and the palette carries *clear the filter
on these rows*. The footer offers `ctrl+g` only while there is a filter to clear.

**The divider is a column of blank, and it is deliberate that it stays blank.** The boundary between
the issue pane's description and its sidebar is one column wide and carries no rule, because the
sidebar's own gutter is the column next to it and two vertical lines side by side say less than one
does. What tells a reader the boundary moves is `?`, the palette and the row in the table above, not
a glyph.
Everything a pointer can do to it, `<`, `>` and `=` do without one.

One gesture this table used to promise is not built, and is not being built here:

- **Right-click for a context menu of the actions valid for a row** —
  [#76](https://github.com/varijkapil13/saral/issues/76). `kernel.Command` has no notion of what a
  command applies to, so "the actions valid for this row" has no data source, and the menu itself
  would be a second copy of the palette overlay.

**Labels are clickable nowhere**, because no view that can filter draws them: the issue list's
columns are key, summary, type, status, assignee and updated, and the detail pane that does list
labels has nothing of its own to narrow. When a view draws a label it can narrow by, it marks the
label the way the list marks its cells.

Mouse mode must be disableable (`mouse = false` in config) for people who rely on terminal text
selection. Off means off all the way down: the zone manager is disabled with it, so a view's markers
are never written into the frame in the first place and there is nothing left for a selection to
pick up — and a view asks `Zones.Enabled()` before telling anybody to click something. Nothing from
the mouse — click, wheel, drag or release — reaches the view while the help overlay is covering it.

## Filtering by a person, without writing JQL

The owner, after a week against a real site: *"From all views there is no easy way to go back to
filter by person. I do not want to write JQL queries — this should also belong to the speed and ease
of use."*

`f` in the issue list opens a picker in two states: **choose a facet, then choose a value.** `esc`
goes value → facet → closed. The facets are assignee, reporter, status, type, priority and label.
`ctrl+k` reaches it from anywhere, including the issue pane, where `f` belongs to the viewport and is
not taken away from it.

**Version, component and sprint are not offered.** Not an oversight: none of the three can be read
through the port role a session holds, so a row for one would be a facet with nowhere to get its
values from. When the port grows a way to read them, they join the list.

**A value is a query, not a pass over the rows in hand.** A chosen value composes into the JQL and
the search runs again, so it reaches an issue this session never fetched — which a local narrow
cannot — and it matches on the id the site gave the value rather than on a display name, which is
localised, is not unique on one site, and which one account answered to two of within a minute.

**Terms compose, and the screen says what is in force.** Two facets narrow together; two values of
one facet widen it — a person *and* a status, either of two people. Every term is a chip under the
rows and a click on one drops it. `a` — every issue in this project — is the no-terms state, so
dropping the last term lands exactly on it and there is no second way to clear a filter to learn.
A project switch takes the terms with it and says so: a status and an issue type are minted per
project, so the ids in force name values the new project has never heard of.

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
- **Never assume a Nerd Font.** Icons come from a glyph set with an ASCII fallback selected by
  config or capability detection; the default set is plain Unicode box-drawing and geometric shapes.
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
