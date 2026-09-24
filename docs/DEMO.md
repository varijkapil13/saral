# Recording the demo

`demo.tape` at the repo root is the demo, written as code. Recording it produces `demo.gif`, which
the README embeds.

**The tape is the artefact, not the GIF.** A tape can be re-recorded by anyone the moment the UI
moves; a GIF committed once is a blob nobody can regenerate. So the tape is committed and the GIF is
whatever the last person to run it produced.

## The tape runs against the fake

`saral -fake` builds its session on `pkg/jira/jiratest`'s in-memory site instead of a profile: a
scrum project `PROJ` with a board and sprints, sixty generated issues, every third assigned to the
account the fake answers as so the issue list's opening query has rows. It reaches no network, never
asks a keychain for a token, and points `SARAL_CONFIG_DIR` and `SARAL_CACHE_DIR` at a temporary
directory it deletes on exit, so two recordings start from the same place and nothing is left behind.
`SARAL_SITE`, `SARAL_EMAIL`, `SARAL_TOKEN` and `SARAL_PROFILE` are ignored for the run.

Every word on screen is the fake's invented data, so the GIF shows nobody's tickets. The site in the
header is the invented `example.atlassian.net`.

What the tape does, and what the fake gives it:

| The tape does | the fake provides |
|---|---|
| opens the second row of the issue list | twenty issues assigned to the demo account |
| `tab` across the detail pane's three regions | an ADF description on every generated issue |
| `t`, then the second move in the list | the fake's workflow, where only a move into a done status has a screen — if the second move is one, the keystrokes fall out of step (see *The transition landed* below) |
| `ctrl+k`, then *Timeline* | nothing; the command is registered unconditionally |
| `n` on the timeline | due dates on every sixth issue |
| `g` `2` for the board | the scrum board `WithProject(PROJ, Scrum)` builds |

`-fake` works with every other flag and subcommand, so `saral -fake doctor` is also a quick check
that a build runs at all.

## Recording

```sh
mkdir -p /tmp/saral-demo
go build -trimpath -o /tmp/saral-demo/saral ./cmd/saral
PATH=/tmp/saral-demo:$PATH vhs demo.tape
```

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
- **Size.** A README GIF over about 5 MB is a page nobody waits for. Fewer frames (`Set Framerate`)
  or shorter `Sleep`s, not a smaller frame.

[`docs/UX.md`](UX.md) has the keys the tape presses and what each of them does.
