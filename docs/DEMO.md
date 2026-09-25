# Recording the demo

`demo.tape` at the repo root is the demo, written as code. Recording it produces
`docs/media/demo.gif` and four screenshots beside it (`issue.png`, `board.png`, `bulk-move.png`,
`release.png`), all of which the README embeds.

**The tape is the source, the media are its output.** Anyone can re-record the tape the moment the
UI moves, so when a change alters what the tape shows, re-record it in the same PR rather than leave
the README showing a screen that no longer exists.

## The tape runs against the fake

`-fake` exists only in a binary built with `-tags demo`, because it embeds `pkg/jira/jiratest`'s
fixtures and a release binary has a size budget to keep. It is left out of `--help`.

`saral -fake` builds its session on `pkg/jira/jiratest`'s in-memory site instead of a profile: a
scrum project `PROJ` with a board, three sprints and three versions, sixty generated issues, every
third assigned to the account the fake answers as so the issue list's opening query has rows. The
running sprint is re-dated around the wall clock and given eighteen issues, so the board draws a
sprint with days left rather than an empty one that ended long ago. It reaches no network, never
asks a keychain for a token, and points `SARAL_CONFIG_DIR` and `SARAL_CACHE_DIR` at a temporary
directory it deletes on exit, so two recordings start from the same place and nothing is left behind.
`SARAL_SITE`, `SARAL_EMAIL`, `SARAL_TOKEN` and `SARAL_PROFILE` are ignored for the run.

Every word on screen is the fake's invented data, so the GIF shows nobody's tickets. The site in the
header is the invented `example.atlassian.net`.

What the tape does, and what the fake gives it:

| The tape does | the fake provides |
|---|---|
| opens the third row of the issue list | twenty issues assigned to the demo account |
| `tab` across the detail pane's regions | an ADF description on every generated issue |
| `t`, `j`, `enter`, `y` | the fake's workflow; the pick asks to confirm, and `y` makes it land (see *The transition landed* below) |
| `ctrl+k`, then *Timeline* | nothing; the command is registered unconditionally |
| `n` on the timeline | due dates on every sixth issue |
| `g` `2` for the board | the scrum board `WithProject(PROJ, Scrum)` builds, with its running sprint |
| `g` `3`, two `space` picks, `m`, `enter`, `y` | the running sprint's issues and an empty future sprint to move them to |
| `g` `5`, then `enter` on 2.0 | an unreleased version with open issues, and a later one to move them to |

`-fake` works with every other flag and subcommand, so `go run -tags demo ./cmd/saral -fake doctor`
is also a quick check that a build runs at all.

## Recording

```sh
mkdir -p /tmp/saral-demo
go build -trimpath -tags demo -o /tmp/saral-demo/saral ./cmd/saral
PATH=/tmp/saral-demo:$PATH vhs demo.tape
```

VHS writes each screenshot as a 24-bit PNG several hundred kilobytes large. Quantising them keeps
the four under 300 KB with no visible change, using the `ffmpeg` VHS already needs:

```sh
for f in docs/media/*.png; do
  ffmpeg -loglevel error -y -i "$f" \
    -vf "split[a][b];[a]palettegen=max_colors=64:stats_mode=full[p];[b][p]paletteuse=dither=none" \
    /tmp/saral-demo/q.png && mv /tmp/saral-demo/q.png "$f"
done
```

A `Screenshot` captures the frame drawn after it, so every one in the tape is followed by a `Sleep`
before the next keystroke; without it the picture is of the screen the next key opened.

`Require saral` fails the tape early if the binary is not on `$PATH`, which is the failure worth
having: a tape that records the shell's *command not found* looks like a recording.

Everything that decides what two recordings have in common is set at the top of the tape — the size,
the font, the theme, the framerate, the typing speed and a cursor that does not blink. Change one of
those and every frame differs; leave them alone and two recordings differ only where the UI did.

VHS has no `Set Columns`/`Set Rows`: the character grid comes out of `Width`, `Height`, `FontSize` and
`Padding`. The values in the tape aim at **120×36**, which is the width the golden files render at.

## Before committing the output

- **The frame is wide enough.** The issue detail pane must show the description, the fields and the
  thread side by side. If it shows one at a time the frame is under 90 columns — raise `Set Width`.
  `docs/UX.md` has the breakpoints.
- **The footer is intact.** All three cells — root, actions, globals — on one row, nothing cut. A
  truncated footer means the frame is narrower than the tape asks for.
- **Nothing private is legible.** Read the GIF frame by frame, not once at speed. Summaries, comment
  text, account names, avatars, board names, the site host in the header, and anything a status line
  said about a failure.
- **No error frames.** A status-line warning about a token, a cache another copy of Saral is holding,
  or an empty pane means the recording caught a broken session rather than the program.
- **The transition landed.** If the picked move had a screen, the tape's keystrokes fall out of step
  and the rest of the recording is a view nobody asked for. Pick another issue, or extend the tape.
- **Size.** Keep `docs/media` under about 3 MB together; the GIF is most of it. Fewer frames
  (`Set Framerate`) or shorter `Sleep`s, not a smaller frame.

[`docs/UX.md`](UX.md) has the keys the tape presses and what each of them does.
