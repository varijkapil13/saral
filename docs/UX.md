# Interaction design

The goal is a tool that gets faster to use the longer you use it, without ever being confusing the
first time. Two audiences, same binary: someone who opened it today, and someone who has lived in it
for six months.

## Principles

1. **First paint is instant, always.** Cached data appears before the network is touched. A spinner
   is a failure of caching, not a loading state.
2. **The footer only shows keys that work right now.** No greyed-out lists of everything. Context
   determines the hints, and the hints come from the key registry so they can never drift from
   reality.
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
| First minute | Onboarding writes the profile, then drops you on your own issues. `?` explains the current view only. |
| First hour | The footer teaches the six keys that matter here. Mouse works, so nothing is blocked on learning. |
| First week | Frecency: projects, assignees, versions and labels reorder so your usual choices are first. |
| | Hints: after you reach an action through the palette three times, the status line notes its key. Built in P3.1 ([#12](https://github.com/varijkapil13/saral/issues/12)) rather than with the footer: the count, the call site and the frecency table are one piece of data, and P3.1 already owns it. `kernel.CommandRanMsg{ID, Keys}` is the signal it hangs on, and `Command.Keys` is the key it names — a command nothing binds is never given one, and the line is said once rather than on every run after the third. |
| Ongoing | JQL history with fuzzy recall; saved queries bound to `1`–`9` and kept in the profile. |
| | Local fuzzy index — typing a key or a few words of a summary finds it with no round trip. |
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

The theme is switched from the palette — *use the dark theme*, *follow the terminal's own colours* —
and the choice is written back into the profile it came from. There is no key for it: every letter
left is one somebody types into a field.

## Navigation model

A stack, not a graph — so "back" always means something.

```
run a saved query      1 – 9          in a root view; the profile's own searches
switch view            g then 1–9     from anywhere, including a pushed view
push                   enter          open the thing under the cursor
pop                    esc            back, never quits from a pushed view
quit                   q / ctrl+c     only from a root view, and only when nothing on the stack
                                      is holding a draft
palette                ctrl+k         everything, fuzzy; opens over what you were in, esc returns.
                                      the one global a view taking typing cannot swallow
search in view         /              filter rows live
save this search       s              bind the query on screen to a number key
refresh                r / R          current view / purge and refetch
```

Vim keys and arrows are both always bound. `j/k` and `↑/↓` are not a preference to configure.

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
query, and the view slots as `g1`–`g9`.

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

Two things this table does not promise. There is no `tab`: no view has two panes yet, and the gesture
that moves focus between them belongs to the first one that grows a second pane. And jumping to an
issue by key — typing `PROJ-142`, or pasting a Jira URL — is not built; it is
[#62](https://github.com/varijkapil13/saral/issues/62).

## Mouse

Bubble Tea v2 declares mouse mode in the view; hit-testing is by zone lookup, not coordinate
arithmetic (see `docs/ARCHITECTURE.md`). This table is what the program does.

| Gesture | Result |
|---|---|
| click a row | select it |
| double-click a row | open it, same as `enter` — and only when both clicks are one gesture |
| click a status, type or assignee cell | show only the rows with that value; click it again to show them all |
| wheel | scroll the pane under the pointer, not the focused one |
| click a footer entry | switch view |
| click anything else a view draws | do what it says — write, send, delete, confirm, put aside, pick a value, go back to an onboarding step |

**A double-click is timed, because nothing else can time it.** `tea.MouseClickMsg` carries a
position, a button and a modifier: no click count and no instant. Every view here first reached for
"a second click on the row that is already selected", which cannot tell a double-click from two
deliberate clicks a minute apart, so pointing at an issue and then pointing at it again opened it.
`widget.Clicks` times the pair against `Deps.Now` — the clock a test injects — over a 400 ms window,
and a slower second click only re-selects.

**Narrowing by a cell is the one filter with no key**, and that is deliberate: every letter left is
one somebody types into `/`, and `esc` in a root view never reaches the view at all. So the rows the
narrowing leaves out are named in a line under the list, the same cell clears it, and the palette
carries *show only rows with this row's status / type / assignee* and *show every row again* for
anyone without a pointer. It composes with `/`: both are things being left out, and a row has to
survive both.

Two gestures this table used to promise are not built, and are not being built here:

- **Drag a divider to resize panes** — [#75](https://github.com/varijkapil13/saral/issues/75). There
  is no divider: no view has two panes, which is also why there is no `tab`. `widget.Drag` is the
  press-move-release machine, tested and bound to nothing; P6.3 binds it to the first pane divider,
  along with persisting the ratio.
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
- **Inline graphics are optional.** Kitty protocol, then iTerm2, then chafa half-blocks, then text.
  Detect once at startup and cache the answer.

## Status and feedback

- Long operations show progress with a real number when the API gives one (attachment bytes, bulk
  move task percentage) and elapsed time when it does not.
- Rate limiting is shown as a countdown, not an error, and any poller pauses itself.
- Stale data is badged rather than hidden. Seeing yesterday's board beats seeing nothing.
- Errors state what failed and what to do. `403` becomes "You need the Bulk Change permission to move
  issues between projects", which is the capability `Reason` verbatim.
