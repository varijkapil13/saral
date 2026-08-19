# Contributing

Thanks for looking. Saral is built in small, independent packets so that several people can work at
once — please follow the flow rather than opening a large PR.

## Setup

```sh
git clone https://github.com/varijkapil13/saral
cd saral
make check        # tidy, lint, race — should be green on a fresh clone
```

You need Go (see `go.mod`) and `golangci-lint`. You do **not** need a Jira account to build or test.

## Flow

1. Pick an unassigned packet from the current batch's milestone in `docs/ROADMAP.md`.
2. Comment on the issue to claim it.
3. Branch as `feat/<packet-id>-<slug>`, e.g. `feat/p1.5-issue-list`.
4. Keep the diff inside the packet's owned paths.
5. `make check`, then open one PR for that packet.

Conventional commit subjects (`feat(board): …`, `fix(search): …`, `perf(list): …`) — they generate the
changelog, so write them for a user.

## What reviewers check

1. Ownership boundary respected.
2. Works against `pkg/jira/jiratest`, with the failure paths tested.
3. Nothing instance-specific hardcoded.
4. Performance budget held on the touched path.

Style is the linter's job. See `docs/PARALLEL.md` for the full definition of done and
`AGENTS.md` if you are an automated contributor.

## Reporting a bug

Include your terminal and its version, `saral version`, whether the site is Cloud or Data Center, and
the redacted output of `saral doctor` once that exists. Never paste a token or a real account ID.
