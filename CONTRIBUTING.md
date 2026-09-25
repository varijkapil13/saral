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

Conventional commit subjects (`feat(board): …`, `fix(search): …`, `perf(list): …`) — the release
notes are generated from them, grouped by type, so write them for a user. Add a line under
*Unreleased* in `CHANGELOG.md` for anything a user will notice, and say so there when a change alters
behaviour someone relies on.

## What reviewers check

1. Ownership boundary respected.
2. Works against `pkg/jira/jiratest`, with the failure paths tested.
3. Nothing instance-specific hardcoded.
4. Performance budget held on the touched path.

Style is the linter's job. See `docs/PARALLEL.md` for the full definition of done and
`AGENTS.md` if you are an automated contributor.

## Trying it without a site

```sh
go run -tags demo ./cmd/saral -fake
```

opens Saral on an invented, in-memory site: a project `PROJ` with a board, three sprints, three
versions and sixty issues. Nothing reaches the network or the keychain, and its config and cache are
deleted on exit. `-fake` exists only in a `-tags demo` build.

## Reporting a bug

Use the bug template. Include your terminal and its version and the output of `saral doctor`, which
checks the config, token, site, cache and proxy and is safe to paste: it never prints the token and
shortens the email. Saral supports Jira Cloud only. Never paste a token or a real account ID.
Security problems go through [`SECURITY.md`](SECURITY.md) instead.
