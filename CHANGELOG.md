# Changelog

All notable changes to Saral are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Saral uses
[semantic versioning](https://semver.org/). Until 1.0, a minor release may change behaviour; each
such change is listed under **Changed**.

## [Unreleased]

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
- Text typed into the inline description editor survives `esc`.
- Editing a description or comment through markdown keeps untouched list items, table cells,
  panels and mentions exactly as they were; literal `*`, `_` and similar characters stay literal.
- A card move on the board updates that card in place and no longer cancels loading the rest of
  the board; a refused move puts the card back and says why.
- WIP limits are drawn only where the board enforces them.
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

[Unreleased]: https://github.com/varijkapil13/saral/compare/v0.6.1...HEAD
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
