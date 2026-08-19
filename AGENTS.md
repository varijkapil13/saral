# Working agreement for coding agents

You are one of several agents working on this repository at the same time. Read this file and
`docs/PARALLEL.md` before touching anything.

## Before you start

1. Open `docs/ROADMAP.md` and pick **one** packet from the current batch that is unassigned and has
   no unchecked dependency.
2. Claim it by commenting on its GitHub issue. That comment is the lock — if someone else has
   commented, pick another.
3. Read `docs/ARCHITECTURE.md` (layers and the port), `docs/API-NOTES.md` (the traps), and
   `docs/TESTING.md` (how to test without Jira).

## Non-negotiable rules

- **Stay inside your packet's owned paths.** They are listed with the packet in `docs/ROADMAP.md`.
  Needing a change elsewhere means opening an issue, not widening your diff.
- **Never add a `case` to another package's switch.** Views, commands and keybindings self-register.
  If registration doesn't cover your need, say so on the issue — do not centralise.
- **Never hardcode anything instance-specific.** No `customfield_10016`, no `"In Progress"`, no
  project keys, no assumed permissions. Resolve at runtime and cache. This is the most common way a
  PR gets rejected here.
- **Never expose a raw destructive API shape.** The canonical example: `PUT` on a sprint nulls every
  omitted field, so the port exposes `StartSprint`/`CompleteSprint`/`UpdateSprint` instead.
- **Tests must not touch the network.** Use `pkg/jira/jiratest`. CI fails on a non-loopback
  connection from a test.
- **No comments that restate the code.** One line only where the code cannot express a non-obvious
  constraint. No ticket or PR numbers in comments, no notes aimed at a reviewer — that belongs in the
  PR description.
- **`go.mod` changes go in their own PR**, merged before the work that needs them.

## Working loop

```sh
git switch -c feat/p1.5-issue-list origin/main
# ... work ...
make check          # tidy + lint + race; must be green
git push -u origin HEAD
gh pr create --fill --label "batch-1"
```

Rebase on `origin/main` before every push. PRs are squash-merged, so keep the branch to one packet.

## Definition of done

The checklist in `docs/PARALLEL.md` is the gate. In short: works against the fake, failure paths
tested (403 / 429 / transport), golden files for rendering, keybindings and mouse zones registered,
benchmarks if you touched a render path, `make check` green, and the roadmap checkbox ticked in the
same PR.

## Performance is a requirement, not a follow-up

If your packet renders a list, it virtualizes. If it renders rows, it memoizes. If it fetches, it
asks for a narrow `fields` set and coalesces duplicate in-flight requests. Budgets are in
`docs/PERFORMANCE.md`; a PR that regresses a budgeted path does not merge.

## When you are unsure

Prefer asking on the issue over guessing, and prefer a smaller correct packet over a larger
speculative one. If you discover something about the Jira API that isn't in `docs/API-NOTES.md`, add
it there in your PR — that file is how the next agent avoids your afternoon.
