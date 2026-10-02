# Changelog

All notable changes to Saral are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Saral uses
[semantic versioning](https://semver.org/). Until 1.0, a minor release may change behaviour; each
such change is listed under **Changed**.

## [Unreleased]

### Added

- `g /` searches every project's issues by what they say: summary, description and comments, with
  `tab` to narrow to the session's project. An exact key is listed first, `L` sends the search to
  the issue list, and the words you typed are painted in the summaries. The command palette has it
  too, as *Search issues on the site*.
- The command palette ends its list with *Search issues for "…"*, which opens that search with
  your words already in it. When nothing cached matched, it is the only row.
- Open an issue's parent and linked issues from its pane: the parent, subtasks and links are rows
  on the sidebar cursor that `enter` or a click on the key opens, and `p` opens the parent.

- An epic's pane lists its children with how many are done, and `c` opens them in a sheet where
  `@`, `t` and `P` change a child's assignee, status or priority without opening it.

### Fixed

- A plan read from the site that names boards instead of projects now lists the releases of the
  projects behind those boards, and names a board you cannot see with the site's reason. It used to
  say the plan named no project.
- Two directions of a link type that arrives with no phrasing are no longer listed together, and an
  issue that cannot be opened says why in its sidebar.

## [0.9.3] - 2026-10-02

### Fixed

- A plan that spans a project you cannot browse now lists the releases of the projects you can,
  and names the one it left out with the site's reason. It used to show no releases at all.

## [0.9.2] - 2026-10-01

### Fixed

- Opening a plan read from the site now lists the releases of its projects. It used to say they
  could not be read, because the site names a plan's projects by id rather than by key.

## [0.9.1] - 2026-09-30

### Added

- Right-clicking a card or row in the issue list, the board or the backlog selects that issue and
  offers *Copy key*, *Copy link* and *Open in browser* (`y`, `Y`, `o`) for it. The issue pane's menu
  offers the same three.

### Fixed

- A right-click on the issue pane's status, priority or assignee no longer opens that field's list.

## [0.9.0] - 2026-09-30

### Added

- The issue list, the backlog and the board draw issues as cards: roomy by default, compact, or the
  one-line rows as before. `V` cycles the look, the palette's *Cycle the row look* does the same, and
  the choice is kept on this machine for all three views. A roomy card shows the summary over two
  lines, the assignee, priority, due date, subtasks done, and the labels and fix versions; the list
  and the backlog also show the status beside the key, the backlog its estimate, and the board its
  estimate and each card's parent. Clicking a card's type, status or assignee in the list still
  filters by it, and the board's cards work in columns and in swimlanes, with clicks and drags as
  before.

## [0.8.0] - 2026-09-29

### Added

- A board column that holds more than one status now asks which one a card is moving to. Dropping a
  card there, by key, drag, `H`/`L` or the palette, lists each status the card can reach; pick one with
  the arrow keys and `enter`, or click it, and `esc` puts the card back. Moving several picked cards
  there asks once, and each card moves to the status chosen.
- Releases can be sorted with `s`: by the project's own order (the default), name, release date,
  start date or state. Choosing the same field again reverses it, versions with no date stay at the
  bottom, and the choice is remembered on this machine.
- Releases can be filtered by state with `f`, which cycles through all, unreleased, released and
  archived. The summary line shows the filter and how many versions it keeps, and the choice is
  remembered for the profile. A version you have just created is always shown.

### Changed

- The toolbar shows `g`, the key that opens the list of views and where to go.

### Fixed

- Refreshing a board no longer empties its columns and refills them. This covers `r`, coming back to
  a board after a while and a board opened from its stored copy. The cards on screen stay until the
  whole board has been read again, then change at once. The cursor stays on the card it was on, or
  on the card that took its place when that card has left the board. Each column stays scrolled to
  where it was, and cards you picked stay picked. If the refresh fails partway, the board keeps what
  it showed and marks it stale.
- A board whose quick filter was left on no longer shows its unfiltered cards for a moment when it
  opens, and the quick-filter line no longer disappears while the board refreshes.

## [0.7.2] - 2026-09-29

### Fixed

- `brew` no longer warns that the saral cask calls the deprecated `postflight`. The cask clears
  macOS quarantine with Homebrew's `postflight_steps` instead.

## [0.7.1] - 2026-09-29

The first published release of everything listed under 0.7.0: that tag was pushed, but its release
failed at signing and published nothing.

### Changed

- `checksums.txt` is signed into a single cosign bundle, `checksums.txt.sigstore.json`, instead of a
  separate `.sig` and `.pem`. To verify by hand: `cosign verify-blob --bundle
  checksums.txt.sigstore.json --certificate-identity-regexp '^https://github.com/varijkapil13/saral/'
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt`.

## [0.7.0] - 2026-09-29 [UNPUBLISHED]

### Changed: read these before upgrading

- **Icons default to plain unicode.** Nerd Font icons are now opt-in: set `glyphs = "nerd"` in your
  profile or choose *nerd font* under Glyphs in settings. Earlier builds treated Nerd Font as the
  default and did not save it, so a profile that chose it before now reads as unicode until you
  choose it once more.
- **Only my issues is `M`** on the board and the backlog, and **`o` opens the issue in the browser**
  everywhere: the issue pane, the list, the board and the backlog.
- **Reverting an edit in the issue pane is `x`** (the field or region that has focus) **and `X`**
  (everything). `backspace` and `U` still work but are no longer advertised.
- **`-fake` is only in builds made with `-tags demo`.** Release binaries do not carry the demo site.
- **`config.toml` gains `version = 1`.** Saral adds it the next time it writes the file. An older
  build reads a newer file but never rewrites it, and a key Saral does not know is reported on the
  status line instead of stopping startup.
- A Scrum board shows its **running sprint** instead of every issue the board's filter matches.
  With parallel sprints, `s` shows the next one.
- The pushed transition picker on the board is gone: a move that needs a screen opens the issue pane
  on that transition.
- Filters on the board, backlog and timeline are kept per project and come off when you switch
  project.
- Drafts (unsaved issue edits, comments and create forms) live in one `drafts/` directory beside
  `config.toml`. Comment drafts from earlier builds move there the first time a thread opens.

### Added

- **Scriptable commands** that need no terminal UI: `saral issue view|create`, `search`,
  `transition`, `comment add`, `assign`, `open` and `completion bash|zsh|fish`, with tab-separated
  output and a documented JSON schema. See `docs/CLI.md`.
- **`saral doctor`** checks the config, token, site, cache and proxy and prints a report that is safe
  to paste into an issue.
- `saral --help`, a `saral version` that reads the build info of a `go install` build, suggestions for
  a mistyped view name, exit codes (`2` usage, `3` config, `4` auth), and `--log FILE` with
  secrets redacted.
- Environment profiles: `SARAL_PROFILE`, and `SARAL_SITE`, `SARAL_EMAIL` and `SARAL_TOKEN` over or in
  place of a config file.
- **Custom fields edited in place** in the issue sidebar: text, URL, paragraph, number, date, date
  and time, labels, select, multi-select, cascading select, user and multi-user, taken from the
  issue's own edit screen.
- **@mention autocomplete** in the description, paragraph fields, the comment composer and create
  forms, writing real mention nodes.
- Around an issue: copy the key (`y`) or link (`Y`), open it in the browser (`o`), and sheets for
  links (`L`), worklogs (`w`) and watchers (`W`). *Clone this issue* in the palette.
- Board and backlog: rank reorder (`K`/`J`, `{`/`}`, drag), one-stroke column moves (`H`/`L`),
  only my issues (`M`), find (`/`, then `n`/`N`), points per backlog section, and a sprint header
  with the goal, days left and a progress bar.
- Board swimlanes by assignee or parent (`w`), with lanes folded by `z`, `Z` or a click, and the
  choice remembered per board.
- Create an issue from where you are (`c`): in a board column it lands in the running sprint and
  that column, in a backlog sprint section it lands in that sprint.
- Pick several cards on the board (`space`, `v` for a whole column, `x` to let go) and assign them
  (`@`), label them (`+`) or move them (`m`) in one go, with a confirmation and a report of what
  did not change.
- Completing a sprint asks where its open issues go: the backlog, the next sprint, or a new one.
  The sprints view shows the running sprint's dates, days left and progress.
- **Bulk fix-version assignment**: `b` on the release list puts a version on, or takes it off, every
  issue a JQL query matches, with a preview first.
- The sprints and releases views draw from the cache before the network answers.
- *Clear the cache* in settings. The cache is bounded per kind and recovers from a corrupt file.
- The create form keeps a draft, asks before you leave with unsaved content, and searches people as
  you type. Attachment uploads show progress and can be cancelled; pasted paths are cleaned up and
  `tab` completes them.
- The move wizard lists the fields a cross-project move would drop.
- The issue sidebar has a Type row, icons on Status and Priority, and clickable status, priority and
  assignee in the header. The settings Glyphs row previews every icon.
- Release packages for Windows (zip) and Linux (`.deb`, `.rpm`, `.apk`).

### Fixed

- A save in the issue pane no longer overwrites a field that changed on the site since you started
  editing it: your changes are kept, and you review them against the new value before saving again.
  The read after a save goes through the issue endpoint, so it is not stale.
- After you transition or edit an issue in the issue pane, the list, board or backlog underneath
  shows the new values without a refresh.
- Text typed into the inline description editor survives `esc`.
- Editing a description or comment through markdown keeps untouched list items, table cells,
  panels and mentions exactly as they were; literal `*`, `_` and similar characters stay literal.
- A card move on the board updates that card in place and no longer cancels loading the rest of
  the board; a refused move puts the card back and says why.
- WIP limits are drawn only where the board enforces them.
- The backlog no longer crashes when a re-read brings back fewer issues with the cursor near the end.
- The view you asked for at startup opens once the site confirms you may use it.
- The palette no longer re-reads the whole cache on every open, and saving its history no longer
  stalls a frame.
- Setup trims pasted tokens, explains a refused token, and suggests `<name>.atlassian.net` for a bare
  site name.
- A GET started after a write can no longer be answered with data from before the write.
- A `Retry-After` longer than a minute is reported instead of waited out; a replayed DELETE that
  finds nothing to delete counts as done.

### Security

- Text from Jira is stripped of terminal escape sequences, control characters and bidi overrides
  before it is drawn, in the TUI and in scriptable output.
- Plain `http://` sites are refused except on loopback, downloads redirected off `https` are refused,
  and response bodies are bounded.
- Every path segment sent to Jira is validated and escaped once.
- Release checksums are signed with cosign and carry a build provenance attestation; the install
  script verifies them when `cosign` or `gh` is available. GitHub Actions are pinned to commit SHAs,
  and CI runs `govulncheck`.
- config.toml and ui.toml writes hold a file lock and follow symlinks safely.

## [0.6.1] - 2026-09-08

### Fixed

- Each board column scrolls on its own instead of the whole grid.

## [0.6.0] - 2026-09-07

### Added

- Edit fields in place in the issue pane, assign people, and get asked before changes are lost.

## [0.5.0] - 2026-09-07

### Added

- The board, the backlog and the issue pane draw from the cache first, and Saral opens where you
  left off.

## [0.4.5] - 2026-09-07

### Fixed

- A board without sprints, and the icon of a cached issue type.

## [0.4.4] - 2026-09-07

### Added

- Type and status icons in the list, the backlog and the issue pane, in front of the name.

## [0.4.3] - 2026-09-07

### Fixed

- An issue type is drawn with a shape, never with its first letter.

## [0.4.2] - 2026-09-07

### Fixed

- The board reads every page, not only the first.

## [0.4.1] - 2026-09-07

### Fixed

- The board counts what is on it and says why it is not more.

## [0.4.0] - 2026-09-04

### Added

- Pin your own fields to the issue pane, per profile; the fields shown come from the issue's own
  screen, and a plugin's bookkeeping fields are hidden.
- Sorting in the list and the backlog, and one filter bar in every list-shaped view.
- A third glyph tier, and an icon for each issue type.

### Fixed

- `F` reaches its digit on the board and backlog, and the backlog reads all its pages before
  ordering them.

## [0.3.0] - 2026-09-04

### Added

- The settings screen, with appearance and session state moved out of the palette.
- Colour schemes.
- Board filters by person, status, type, priority or label, and the board's own quick filters.
- A development build keeps its files apart from an installed copy's (`saral-dev`).

### Fixed

- A sprint's name is drawn in the sidebar instead of its JSON.

## [0.2.1] - 2026-09-03

### Fixed

- Publishing the Homebrew cask.

## [0.2.0] - 2026-09-03

### Added

- `g` then `i` jumps to an issue by key or URL, and `g` says where it goes while it waits for the
  digit.

## [0.1.0] - 2026-08-27

The first release: the issue list, issue detail, create and edit forms from the site's own screens,
transitions, comments with markdown ⇄ ADF, attachments with inline image preview, the board and
backlog, sprints, releases with the unresolved-issue decision, the cross-project move wizard, the
timeline, plans, the command palette, full mouse support and cache-first paint.

[Unreleased]: https://github.com/varijkapil13/saral/compare/v0.9.3...HEAD
[0.9.3]: https://github.com/varijkapil13/saral/compare/v0.9.2...v0.9.3
[0.9.2]: https://github.com/varijkapil13/saral/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/varijkapil13/saral/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/varijkapil13/saral/compare/v0.8.0...v0.9.0
[0.8.0]: https://github.com/varijkapil13/saral/compare/v0.7.2...v0.8.0
[0.7.2]: https://github.com/varijkapil13/saral/compare/v0.7.1...v0.7.2
[0.7.1]: https://github.com/varijkapil13/saral/compare/v0.7.0...v0.7.1
[0.7.0]: https://github.com/varijkapil13/saral/compare/v0.6.1...v0.7.0
[0.6.1]: https://github.com/varijkapil13/saral/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/varijkapil13/saral/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/varijkapil13/saral/compare/v0.4.5...v0.5.0
[0.4.5]: https://github.com/varijkapil13/saral/compare/v0.4.4...v0.4.5
[0.4.4]: https://github.com/varijkapil13/saral/compare/v0.4.3...v0.4.4
[0.4.3]: https://github.com/varijkapil13/saral/compare/v0.4.2...v0.4.3
[0.4.2]: https://github.com/varijkapil13/saral/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/varijkapil13/saral/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/varijkapil13/saral/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/varijkapil13/saral/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/varijkapil13/saral/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/varijkapil13/saral/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/varijkapil13/saral/releases/tag/v0.1.0
