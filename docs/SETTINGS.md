# Settings

The command palette had become the place everything went that had nowhere else to go. This document
is the split that fixes it, and the vocabulary the settings screen is built from.

Read it with `docs/UX.md` (the palette, the footer, the key allocation) and `docs/ARCHITECTURE.md`
(the registries and the optional view interfaces). It changes one shared type — `kernel.Command`
gains a `Kind` — and adds one registry beside the three that exist.

## The rule

**The palette is for verbs. Settings are for state.**

A palette row is something you *do* once and it is over: edit this issue, go to the board, run this
search. A settings row is something that *is*, and stays that way until you change it: the theme, the
colour scheme, the project this session is scoped to.

Everything that follows is that one sentence applied.

## What was wrong

Not opinions. Each of these is checkable against the tree as of `1f54075`.

**The first screen of the palette is the colour picker.** `kernel.Commands` sorts by `Group` and then
`Title`, and `app.Pattern.Score` returns `0, true` for every candidate when nothing is typed — so an
empty `ctrl+k` falls through to frecency, and on a fresh install the frecency table is empty and the
registry's own order stands. `Appearance` sorts before `Attachments`. The first twenty rows of an
80×24 palette are nine appearance commands, four attachment ones, two backlog, two board and three
comments. You cannot see *Create an issue*, or a single *Go to*, without typing.

**A radio group is drawn as N unrelated peers.** Four theme modes (`kernel/theme.go:317`), five colour
schemes (`kernel/scheme.go:123`) and four timeline zooms (`timeline/register.go:38`) are three
settings with thirteen values between them. The palette draws thirteen rows, all captioned
`Appearance` or `Timeline`, with nothing saying they are three sets, and nothing saying which value of
each is in force. *Use the Nord colour scheme* reads exactly the same whether you are in Nord or not.

The mechanism to fix that already exists and was never applied here: `palette.projectRow` carries a
`current` field, commented *"marks the scope this session is already on, so that switching to it is
visibly a no-op rather than a mystery"*. The schemes never got one.

**Half the configuration has no interface at all.** `config.Profile` validates `Glyphs`
(`unicode`/`ascii`), `Config.Mouse`, `Profile.Timeline.Start` and `.End` — and nothing in the running
program can change any of them. They are hand-edited TOML. So the configuration surface is split
between a palette that shows nine settings and a file that holds five more, with no seam between them
and nothing that lists both.

**Verbs and nouns share a column.** *Backlog* (a destination) sits between *Delete the file you are
on* (a verb needing a cursor) and *Show the next board on this project*. The `Group` column is the
only thing distinguishing them and it is doing the work of two different distinctions at once.

### The bug the shape was hiding

`kernel.SwitchTheme` builds `NewTheme(mode, dark, glyphs)` with no `WithScheme`
(`kernel/theme.go:358`). `kernel.SwitchScheme` carries the mode along; the reverse does not carry the
scheme. So choosing *Use the dark theme* in a Nord session silently reverts every colour to the
default set — and `writeTheme` only writes the `theme` key, so `scheme = "nord"` is still in the file
and the next run comes back Nord. Nothing on screen ever says what happened.

A settings screen that draws the current value of both rows makes this visible on the frame it
happens. That is the argument for the whole change in one line: **state you cannot see is state that
drifts.**

A second one of the same family: `NewTheme` ignores the scheme entirely when the mode is
`ThemeNoColor` (`theme.go:218` chooses `plain()`), so picking a scheme with colour off does nothing
visible and still writes to the profile.

## The setting registry

Settings self-register from the package that owns the state, exactly as views, commands and keys do.
No package adds a `case` to another package's switch — the rule in `AGENTS.md` — so the settings view
never learns the name of a single concrete setting.

```go
// kernel/setting.go

// SettingKind is how a setting is drawn and what changing it means.
type SettingKind int

const (
	// KindChoice is one value out of a known set. It draws as radios when the
	// options fit the row and as a value with a picker behind it when they do not.
	KindChoice SettingKind = iota
	// KindToggle is a choice of exactly two where one is the absence of the other.
	KindToggle
	// KindInfo is state the program shows and cannot change here.
	KindInfo
	// KindAction is a button: it runs a command and holds no value.
	KindAction
)

// SettingScope is where a chosen value is kept, which is drawn beside the
// section so that nothing has to guess whether a choice survives the session.
type SettingScope int

const (
	ScopeProfile SettingScope = iota // config.toml, the active profile
	ScopeFile                        // config.toml, shared by every profile
	ScopeMachine                     // the cache directory's ui.toml
	ScopeSession                     // this run only
)

// Option is one value a KindChoice setting can take.
type Option struct {
	ID    string
	Label string
	// Note is the half-line under or beside the label: what the value means, or
	// what is different about it.
	Note string
	// Style, when non-nil, draws this option's label. It is how the colour
	// schemes preview themselves in their own colours.
	Style func(*Theme) lipgloss.Style
}

// Setting is one piece of state the settings view can show and change.
type Setting struct {
	// ID is stable and dotted, "appearance.scheme". It is what a deep link names
	// and what the frecency table would key on if settings ever rank.
	ID string
	// Section buckets settings on screen. Order within a section is Order, then
	// Title.
	Section string
	Order   int
	Title   string
	// Summary is the one line under the title. It says what the setting decides,
	// never what the current value is — Value answers that.
	Summary string
	Kind    SettingKind
	Scope   SettingScope
	// Requires names the capability this setting needs, if any, and is refused
	// with the probe's own words exactly as Command.Requires is.
	Requires jira.CapabilityKey

	// Options are what a KindChoice or KindToggle offers. It is a function
	// because the answer can come from the site: the projects are read, not
	// registered.
	Options func(Deps) []Option
	// Value is which option is in force right now, read from the live session
	// rather than from anything this registry stored. That is the whole point:
	// a mark that is computed from Deps.Theme cannot drift from what is on
	// screen, and one cached at registration can.
	Value func(Deps) string
	// Set changes it. It returns the command that both applies and persists,
	// which is what SwitchTheme and SwitchScheme already are.
	Set func(d Deps, optionID string) tea.Cmd

	// Unavailable is why this setting cannot be changed here, and "" when it
	// can. It is not the same question as Requires: NO_COLOR being exported is
	// not a capability, and neither is "colour is off, so a scheme changes
	// nothing". A setting that answers is drawn with its value and its reason,
	// never hidden — hiding it would leave a user looking for a control that is
	// there and inert.
	Unavailable func(Deps) string

	// Run is what a KindAction does.
	Run func(Deps) tea.Cmd
}

func RegisterSetting(s Setting)
func Settings() []Setting          // ordered by Section, Order, Title
func SettingSections() []string    // in a registered order, not alphabetical
```

Three things about this shape are load-bearing.

**`Value` is a function of `Deps`, not a stored string.** The theme is on `Deps.Theme`, the glyph set
is on `Deps.Theme.Glyphs`, the project is `Deps.Project`. Reading them live is what makes the
checkmark impossible to get wrong. A registry that cached the value at `init()` would be showing you
the value as of program start.

**`Unavailable` is separate from `Requires`.** `Requires` is *this token may not do this on this
site*, answered by the capability probe, and the palette already hides those rows and says why
(`palette.refusal`). `Unavailable` is *this control is real but inert right now* — `NO_COLOR` is
exported, or colour is off so a scheme is moot. Those are drawn, not hidden, because the user is
looking straight at the thing they want to change and needs to be told why it will not move.
`kernel.noColorForced` already computes one of these and throws it away into a status line.

**A section carries its scope.** Rendering `Appearance · saved to profile "work"` once beats putting
*(saved)* on six rows, and it is the answer to a question the current design never answers at all:
`saveTheme` has three outcomes — written, no profile so session-only, write failed — and says which
in a status line that is gone by the time you have read the next row.

## The screen

`internal/ui/settings` — a new package, a new directory, nobody else's files. It registers a
`ViewSpec` with `Slot: 0`, its own keys, and one command.

```
 Settings                                                    ctrl+, · saral
────────────────────────────────────────────────────────────────────────────
  Appearance                                     saved to profile "work"

  ▸ Theme                     ( ) auto  (•) dark  ( ) light  ( ) no colour
      how colours are chosen

    Colour scheme             nord                                       ▸
      which colours mean accent, danger and the rest

    Glyphs                    (•) unicode  ( ) ascii
      box drawing, or plain ASCII for a font you cannot trust

    Mouse                     [✓] on                       this machine
      clicking, dragging the split, the right-click menu

  Session                                        saved to profile "work"

    Project                   DA · Data Analytics                        ▸
      what a search means by "this project", and what the probe ran against

    Profile                   work · example.atlassian.net               ▸
      site, account and where the token comes from. changing it needs a restart

    Set up a profile again                                               →
      re-runs the questions onboarding asks

────────────────────────────────────────────────────────────────────────────
 Settings  up/down choose  ←/→ pick  enter open  esc back      ? ctrl+k q
```

### The control vocabulary

Five row shapes, and no sixth without a reason written down.

| Shape | When | Interaction |
|---|---|---|
| **Radios**, inline | `KindChoice`, options fit the row (roughly ≤4 short labels) | `←`/`→` moves through them and applies at once, `enter` applies the one under the cursor |
| **Value + `▸`** | `KindChoice`, options do not fit, or come from the site | `enter` or `→` opens a picker over the screen; it comes back with the value applied |
| **Toggle** `[✓]` | `KindToggle` | `enter`, `space` or `←`/`→` flips it |
| **Value, dimmed** | `KindInfo` | nothing; `enter` says why in the status line |
| **Label + `→`** | `KindAction` | `enter` runs it |

Radios apply immediately. There is no *Save* button and no *Apply*: every one of these settings is
already a live switch — `SwitchTheme` rebuilds the styles and writes the profile in one command — and
a screen with a save button would be a second model of the same state, which is the thing this whole
change is about removing.

**The picker behind a `▸` is the picker that already exists.** `palette.projectModel` is a filtered,
frecency-ranked list with a `current` marker and its own click zones; the project row opens exactly
that. The scheme row opens one built the same way, and each scheme's row is drawn in that scheme's
own colours through `Option.Style` — a colour picker that previews is the obvious thing the palette
structurally could not do.

### Keys

| Key | Where | What |
|---|---|---|
| `ctrl+,` | anywhere | open settings |
| `g` `s` | anywhere | the same, for terminals that swallow `ctrl+,` |
| `↑`/`↓`, `j`/`k` | in settings | move between rows |
| `←`/`→`, `h`/`l` | in settings | change the value on the row |
| `enter` | in settings | apply, open the picker, or run the action |
| `esc` | in settings | back |

`ctrl+,` is free: the globals are `q ctrl+c esc ? ctrl+k r R g` and the digits, and `alt+k` is
kill-line. `g s` is free the same way `g i` is — the kernel buffers `g` and forwards nothing until
the next key, so no view loses a gesture. Both go in `GlobalKeys`, so `?` lists them and the footer
can show them.

Settings takes **no footer slot**. `docs/UX.md` keeps the digits for the views a session lives in,
and this is not one.

## What moves, and what does not

### Moves out of the palette

| Today | Becomes |
|---|---|
| `theme.auto`, `theme.dark`, `theme.light`, `theme.no-color` | one `KindChoice` setting, `appearance.theme` |
| `scheme.default`, `.nord`, `.dracula`, `.solarized`, `.gruvbox` | one `KindChoice` setting, `appearance.scheme` |

Nine commands deleted, two settings registered. `registerThemeCommands` and `registerSchemeCommands`
go; `SwitchTheme` and `SwitchScheme` stay exactly as they are and become the settings' `Set`.

### Gains an interface for the first time

| State | Setting | Where it is kept |
|---|---|---|
| `Profile.Glyphs` | `appearance.glyphs`, choice of unicode/ascii | profile |
| `Config.Mouse` | `appearance.mouse`, toggle | config.toml, shared |

Glyphs already switches cleanly: `NewTheme` takes a `Glyphs` and a `ThemeMsg` carries the rebuilt
theme, so a `SwitchGlyphs` beside `SwitchTheme` is the same six lines. Mouse needs one new message —
the kernel holds `m.mouse`, calls `Zones.SetEnabled` once in `Init` and reads `m.mouse` per frame in
`View`, so a `SetMouseMsg` that sets the field, re-enables the zone manager and writes `Config.Mouse`
is all of it.

### Stays a command *and* appears as a setting

| Command | Why both |
|---|---|
| `project.switch` | It is a frequent verb as well as session state. The palette keeps the row; the settings row opens the same `projectModel`. One picker, two doors. |
| `onboarding.run` | It is a verb — *set up a profile* — and it belongs on the Profile section as a `KindAction`. |

### Is `KindInfo` and not changeable here

**Profile.** `run()` in `cmd/saral/main.go` builds the token, the client, the cache and the theme
before `kernel.New`, so switching profiles at runtime means rebuilding all four and re-probing.
That is not this packet. The row names the active profile, its site, its email and where its token
comes from (`TokenSource.String`, which never reveals the token). If `config.toml` holds more than
one profile, `enter` opens a picker that writes `active = "<name>"` and says, in the status line, that
it takes effect next run. Honest and small; a hot swap that half worked would be neither.

### Stays in the palette, unchanged

Every verb: Attachments, Backlog, Board, Comments, Issue, Issues, Plans, Releases, Search, Sprints,
Timeline. The four `timeline.zoom.*` commands are a radio group by the letter of this document, but
they are a property of what the timeline is showing rather than of the program, they already have
`+`/`-` keys and a live view to see the answer in, and moving them would put a view's own state on a
preferences screen. They stay. Written down here so the next reader does not have to re-derive it.

## The palette, after

Two changes, both small, neither about settings.

**`Command.Kind` orders the unfiltered list.** A new field on `kernel.Command`, defaulting to zero:

```go
type CommandKind int

const (
	KindAction CommandKind = iota // what you can do here — the default
	KindGoTo                      // a destination
	KindSearch                    // a search to run
	KindSession                   // scope: the project, the settings screen
)
```

`Commands()` orders by `Kind`, then `Group`, then `Title`. A rank on the command rather than a table
of group names in the palette, because a table would be a central switch every new group has to be
added to — the thing `AGENTS.md` forbids — and this way a package that registers a destination says
so in its own file.

Nine commands set it: the seven `*.open` rows and `views.switch` take `KindGoTo`, `project.switch`
and the new `settings.open` take `KindSession`, the `issues.*` searches take `KindSearch`. Everything
else keeps the default and needs no edit.

**Group headers in the unfiltered list.** With nothing typed the palette draws its groups with
headings, in `Kind` order; frecency still reorders *within* a group so a habit is still rewarded.
The moment anything is typed the headers go and it is one flat ranked list, because when you are
filtering, rank beats grouping and a heading is a row that cannot be chosen.

```
> what do you want to do?                       > sprint
──────────────────────────── 59 commands        ──────────────── 5 of 59
  Go to                                         ▸ Plan a new sprint       n
▸ Issues                            g1            Start the sprint you…   s
  Board                             g2            Complete the sprint…    c
  Backlog                           g3            Move the picked issues… m
  Session                                         Show or hide the closed o
  Settings…                     ctrl+,
  Switch project
  Issue
  Edit this issue                    e
```

## Consumers

`AGENTS.md`: *if your packet changes something other packets consume, changing it is half the job.*
The changed things and every consumer of each, by name.

| Changed | Consumers that must adopt it |
|---|---|
| `kernel.Command` gains `Kind` | `board`, `backlog`, `list`, `plan`, `release`, `sprint`, `timeline` `register.go` (`*.open` → `KindGoTo`); `kernel/destinations.go` (`views.switch`); `palette/register.go` (`project.switch`); `list/register.go` (the eight `issues.*` searches → `KindSearch`) |
| `kernel.Commands()` order | `palette.buildRows` and its golden files; `internal/ui/menu_test.go`; `kernel/palette_test.go` |
| `kernel.GlobalKeys` gains `Settings` | `kernel/keys.go` `KeySet()`, the `?` overlay, `internal/ui/keys_test.go`, `internal/ui/footer_test.go` |
| `registerThemeCommands` / `registerSchemeCommands` deleted | `kernel/theme_test.go`, `kernel/scheme_test.go`, `internal/ui/menu_test.go`, any golden file naming a `theme.*` or `scheme.*` row |

Grep for each new symbol and expect a hit per consumer, not just at the definition and its own test.

The frecency table needs nothing: `palette.table.trim` is bounded at 200 and its own comment already
says what the bound catches is *"IDs from older builds piling up behind renames"*. Nine orphaned
`theme.*` and `scheme.*` entries decay and fall out.

## Definition of done, beyond `docs/PARALLEL.md`

- Every registered `Setting` is exercised: a table test walks `kernel.Settings()` and asserts each one
  answers `Value` with an ID that `Options` actually offers, for a `Deps` built three ways — no
  theme, a themed session, and a session with `NO_COLOR` set. **The test asserts it scanned a
  non-zero set**, so a registry that failed to register cannot pass it by doing nothing.
- Golden files for the settings screen at 80 and 120 columns, in the default and the no-colour theme,
  with the cursor on a radio row and on a `▸` row.
- Switching theme mode keeps the colour scheme. This is a regression test with a name that says so,
  because it is a bug being fixed and not a property being added.
- Picking a scheme with colour off draws the row as unavailable and does not write the profile.
- The palette's unfiltered first screen contains at least one *Go to* row on an 80×24 terminal, and no
  `theme.*` or `scheme.*` row exists in the registry at all.
- `internal/arch` sweeps still pass: settings registers keys under its own scope like every other
  view.
- A benchmark for the settings screen's render path, per `docs/PERFORMANCE.md`. Rows memoize on a
  comparable key the way `palette.rowCache` does — the theme generation is part of it, and so is the
  current value, or a radio would not repaint when it moves.
