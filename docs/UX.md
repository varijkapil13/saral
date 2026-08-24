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
| | Hints: after you reach an action through the palette three times, the status line notes its key. |
| Ongoing | JQL history with fuzzy recall; saved queries bound to `1`–`9` and kept in the profile. |
| | Local fuzzy index — typing a key or a few words of a summary finds it with no round trip. |
| | Session resume: reopening lands exactly where you left, including scroll and filter. |
| Fluent | `saral PROJ-142`, `saral board PROJ`, piping a JQL query in, scriptable subcommands for the rest. |

Frecency is a plain local table of `(item, count, lastUsed)` scored `count * decay(lastUsed)`. No
telemetry leaves the machine, ever.

## Navigation model

A stack, not a graph — so "back" always means something.

```
run a saved query      1 – 9          in a root view; the profile's own searches
switch view            g then 1–9     from anywhere, including a pushed view
push                   enter          open the thing under the cursor
pop                    esc            back, never quits from a pushed view
quit                   q / ctrl+c     only from a root view, and only when no draft is open
palette                ctrl+k         everything, fuzzy
search in view         /              filter rows live
save this search       s              bind the query on screen to a number key
refresh                r / R          current view / purge and refetch
```

Vim keys and arrows are both always bound. `j/k` and `↑/↓` are not a preference to configure.

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
arithmetic (see `docs/ARCHITECTURE.md`).

| Gesture | Result |
|---|---|
| click | focus that pane, select that row |
| double-click | open (same as `enter`) |
| wheel | scroll the pane under the pointer, not the focused one |
| drag divider | resize panes; the ratio persists per view |
| click a status chip / label / assignee | filter by it; click again to clear |
| click footer entry | switch view |
| right-click a row | context menu of the actions valid for it |

Mouse mode must be disableable (`mouse = false` in config) for people who rely on terminal text
selection.

## Rendering rules for modern terminals

- **True color when available, 256 and 16 as graceful steps down, and a real no-color mode** driven
  by `NO_COLOR` and `TERM`.
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
