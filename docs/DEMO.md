# Recording the demo

`demo.tape` at the repo root is the demo, written as code. Recording it produces `demo.gif`, which
the README embeds.

**The tape is the artefact, not the GIF.** A tape can be re-recorded by anyone the moment the UI
moves; a GIF committed once is a blob nobody can regenerate. So the tape is committed and the GIF is
whatever the last person to run it produced.

## The tape cannot be driven against the fake today

It should be able to be. `pkg/jira/jiratest` is a complete `jira.Client` with projects, boards,
sprints, versions and generated issues, and a demo built on it would need no site, no credentials and
no network. Two things stop it, and neither is in this packet's owned paths:

1. **`cmd/saral` has one way to build a client.** `build()` in `cmd/saral/main.go` reaches
   `deps.Jira` only through `clientFor(profile)` → `cloud.New`. There is no flag, no environment
   variable and no build tag that installs another implementation of the port. A session with no
   reachable site is a real state — it draws from the cache and says so — but it is not a demo.
2. **A loopback fixture server is unreachable from a profile.** `jiratest.NewServer` is an
   `httptest.Server` and serves plain HTTP, while `config.NormalizeSite` refuses any scheme but
   `https` and `cloud.parseSite` prepends `https://` to a bare host. `cloud.New` will accept
   `http://…` from a caller that passes one — that is what the adapter's own tests do — but nothing
   between the config file and `cloud.New` can express it.

**The smallest honest fix is a flag in `cmd/saral`**: a `-fake` that sets
`deps.Jira = jiratest.New(jiratest.WithProject(…), jiratest.WithIssues(jiratest.Gen(…)))` and skips
`clientFor` entirely. No import rule forbids `cmd/**` from importing `pkg/jira/jiratest`, the fixture
tree it embeds is under 300 KiB against a 15 MiB budget, and it would replace this whole section with
one line of the tape. Until it exists, the tape records against a scripted profile, and the recorder
supplies what that profile points at.

## What the recorder has to set up

The tape sets `SARAL_CONFIG_DIR` and `SARAL_CACHE_DIR` to directories under `/tmp/saral-demo`, so a
recording never touches a real profile and never leaves one behind. Both must exist before `vhs`
starts, and the config directory must hold a `config.toml`:

```sh
mkdir -p /tmp/saral-demo/config /tmp/saral-demo/cache
cat > /tmp/saral-demo/config/config.toml <<'TOML'
active = "demo"

[profiles.demo]
site  = "your-site.atlassian.net"
email = "you@example.com"
project = "EX"
theme = "dark"
token = { env = "SARAL_DEMO_TOKEN" }
TOML
export SARAL_DEMO_TOKEN=...
```

`Env SARAL_DEMO_PROJECT` in the tape must match the profile's project key, because `--project`
scopes the session and three of the six capabilities are answered per project.

For the sequence to be worth watching, that project needs:

| The tape does | so the project needs |
|---|---|
| opens the second row of the issue list | at least three issues the account can see |
| `tab` across the detail pane's three regions | an issue with a description worth reading and a comment on it |
| `t`, then the second move in the list | two or more available transitions, and **no transition screen** on the one it picks — a screen adds a step the tape does not type |
| `ctrl+k`, then *Timeline* | nothing; the command is registered unconditionally |
| `n` on the timeline | issues whose dates resolve, so the notes have provenance to report |
| `g` `2` for the board | a board on the project, or `CapBoards` is absent and the slot is not registered |

Everything on screen ends up in a GIF in a public README. **Record against a site whose ticket
summaries, board names and account names you are willing to publish** — a scratch site, not the one
your team works in. `scripts/checkleak.py` guards the fixture tree; nothing guards pixels.

## Recording

```sh
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
