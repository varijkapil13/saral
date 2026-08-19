# Working in parallel

Several agents (or people) work on Kanso at once. This document is the contract that keeps that from
turning into merge hell. It is short on purpose — read all of it before opening a branch.

## The five rules

1. **One packet, one branch, one PR.** Packets are defined in `docs/ROADMAP.md` and mirrored as
   GitHub issues. Never combine two.
2. **Only touch files your packet owns.** Every packet declares its owned paths. If you need a change
   outside them, see *Shared files* below — do not just edit it.
3. **Never edit a central dispatcher.** There aren't any. Views, commands and keybindings
   self-register from a file your packet owns. If you find yourself wanting to add a `case` to
   someone else's switch, the design is wrong — say so in the issue.
4. **Rebase, never merge.** `git pull --rebase origin main` before pushing. PRs are squash-merged.
5. **Green CI, or it does not merge.** `make check` locally first. A red PR blocks everyone behind
   it in the queue.

## Why file ownership works here

The architecture is built so that a new capability is *additive*:

```
adding a view:      internal/ui/<view>/**          ← new directory, nobody else's files
adding a command:   RegisterCommand in your file   ← palette picks it up
adding keys:        RegisterKeys in your file      ← help overlay picks it up
adding an endpoint: pkg/jira/<area>.go             ← one file per area
adding fixtures:    pkg/jira/jiratest/fixtures/<area>/**
```

The only genuinely shared files are `go.mod`, `go.sum` and the port interface.

## Shared files

| File | Policy |
|---|---|
| `pkg/jira/port.go` | Frozen after Batch 0. Extending it needs an issue labelled `contract` and a fast review — it unblocks or blocks everyone. Add methods; never change an existing signature without a deprecation step. |
| `go.mod` / `go.sum` | Add dependencies in their own tiny PR, merged first. Never bundle a dep bump with feature work — it is the one guaranteed conflict. |
| `internal/ui/kernel/**` | Owned by the kernel packet. Other packets consume it. Changes need the `kernel` label. |
| `docs/ROADMAP.md` | Append-only status ticks. Do not restructure while others are mid-flight. |

If two packets both need the same new helper, the *second* one to need it consumes what the first
landed. Duplicating a helper is better than editing across ownership lines; consolidate later in a
dedicated cleanup packet.

## Definition of done for a packet

A packet is done when all of these hold. A PR missing any of them is not ready for review.

- [ ] Behaviour works against `jiratest` — **no live Jira required to run the tests**
- [ ] Table-driven unit tests for the logic, golden-file tests for any rendering
- [ ] Error paths tested, including 403 (capability), 429 (rate limit) and a transport failure
- [ ] No new `context.TODO()`, no `panic` in a non-`main` package, no swallowed errors
- [ ] `make check` passes (tidy, lint, race)
- [ ] Public API in `pkg/**` has doc comments; `internal/**` does not need them
- [ ] Keybindings registered, not hardcoded; mouse zones added for anything clickable
- [ ] Benchmarks added if the packet touches a render path or a list
- [ ] `docs/ROADMAP.md` checkbox ticked in the same PR

## Branch and commit conventions

```
feat/p3.2-attachment-upload      ← feat|fix|perf|refactor|test|docs / <packet id> - <slug>
```

Conventional commits, imperative mood, no ticket numbers in code comments:

```
feat(attachments): upload with multipart and XSRF header
perf(board): virtualize row rendering, drop per-frame allocations
fix(search): treat repeated cursor token as exhaustion
```

The changelog is generated from these, so the subject line is user-facing prose.

## Picking work

1. Open the milestone for the current batch. Anything unassigned with no unchecked dependency is
   available.
2. Assign yourself, then say so in the issue before starting — that is the lock.
3. If you finish and the next batch is not open yet, take a `good-first-packet` from the backlog
   rather than starting batch N+1 early. Batch order encodes value delivery, not just dependency.

## When you are blocked

Post on the issue with what you need and pick up something else. Do not:

- widen your packet to include the blocker,
- stub a shared interface "temporarily",
- or leave a half-finished branch unpushed.

Push what you have behind a `wip:` PR title so someone else can pick it up.

## Review

Reviews look for exactly four things, in order:

1. Does it stay inside its ownership boundary?
2. Does it work with the fake, and are the failure paths tested?
3. Does it assume anything about a Jira instance? (field IDs, statuses, permissions — all forbidden)
4. Does it hold the performance budget on the path it touches?

Style is the linter's job, not a reviewer's.
