# Bootstrap

Everything needed to go from a fresh clone to starting P0.1. Read this first if you are the one
opening the repo for the first time; it is the only document that assumes nothing exists yet.

## 1. Verify what your token can reach

Four questions, seven calls, run once by a human. They decided what P0.1 and P1.3 built, and their
responses became the first test fixtures. Guessing at any of them has already cost a rewrite once, so
run them before trusting a `schema` row in [`docs/API-NOTES.md`](API-NOTES.md) about the same
endpoint.

```sh
export SARAL_SITE=your-site.atlassian.net
export SARAL_EMAIL=you@example.com
export SARAL_TOKEN=...   # https://id.atlassian.com/manage-profile/security/api-tokens
```

**a. Are plans reachable?** 200 means the Plans API works for you; 403 is the normal answer and is
what the locally-defined-plan fallback exists for. Either way it is a valid capability answer, not a
failure.

The path has a doubled segment and that is not a typo — `/rest/api/3/plans` alone does not exist.

```sh
curl -su "$SARAL_EMAIL:$SARAL_TOKEN" "https://$SARAL_SITE/rest/api/3/plans/plan?maxResults=5"
```

**b. What can this token do?** Look for `havePermission: true` on `BULK_CHANGE`, `MOVE_ISSUES` and
`CREATE_ISSUES`. Without `BULK_CHANGE`, cross-project move (P7.1) is unavailable and degrades to
recreate-and-close.

```sh
curl -su "$SARAL_EMAIL:$SARAL_TOKEN" \
  "https://$SARAL_SITE/rest/api/3/mypermissions?permissions=BULK_CHANGE,MOVE_ISSUES,CREATE_ISSUES,DELETE_ISSUES"
curl -su "$SARAL_EMAIL:$SARAL_TOKEN" "https://$SARAL_SITE/rest/api/3/configuration"
curl -su "$SARAL_EMAIL:$SARAL_TOKEN" "https://$SARAL_SITE/rest/api/3/myself"
```

**c. How is a board actually configured?** This is where the estimation field, the rank field and the
column-to-status mapping come from — the three things a reusable board view must never hardcode.

```sh
curl -su "$SARAL_EMAIL:$SARAL_TOKEN" "https://$SARAL_SITE/rest/agile/1.0/board"
curl -su "$SARAL_EMAIL:$SARAL_TOKEN" "https://$SARAL_SITE/rest/agile/1.0/board/{id}/configuration"
```

**d. What does your real ADF look like?** Pull descriptions from a few rich tickets and count the node
types. That census decides what `pkg/adf` renders properly and what it only needs to preserve
verbatim.

```sh
curl -su "$SARAL_EMAIL:$SARAL_TOKEN" "https://$SARAL_SITE/rest/api/3/issue/PROJ-1?fields=description"
```

## 2. Capture, to check the shapes

Once the calls above look sane, capture them properly. The capture lands in `testdata/live/fixtures/`,
which is gitignored — **it is never committed**, because a real response carries your ticket
summaries, release names and field names. What it is for is checking that the fixtures in
`pkg/jira/jiratest/fixtures/` have the right keys, types, date formats and paging envelopes; those
are what make every later packet testable with no Jira, no credentials and no shared sandbox.

```sh
./scripts/capture.sh
```

It asks for the site, email, token, a project key and an issue key, so nothing has to be exported
first; set any of `SARAL_SITE`, `SARAL_EMAIL`, `SARAL_TOKEN`, `SARAL_PROJECT` or `SARAL_ISSUE`
beforehand to skip that prompt. Pick an issue with lists, code blocks, panels, links and an image —
that one description decides what `pkg/adf` has to render properly.

The script follows the search page token and walks the boards itself, so there is nothing left to
capture by hand. What it cannot capture it names at the end, along with the four places a real
capture has to be reconciled with the tests: the fixture inventory, the create-meta issue type the
fixture server dispatches on, and the ADF node census. **Read the diff** — the scrubber handles
account ids, names, emails and mentions, but not the prose in a summary or a comment. See
[`pkg/jira/jiratest/fixtures/README.md`](../pkg/jira/jiratest/fixtures/README.md).

## 3. Start P0.1

[Issue #1](https://github.com/varijkapil13/saral/issues/1) is serial and blocks every other packet.
Read in this order:

1. [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) — the layers, the port, the registries. The port
   interface printed there is normative: implement that shape.
2. [`docs/API-NOTES.md`](API-NOTES.md) — the traps. `PUT` on a sprint nulls omitted fields;
   `/search` is 410 Gone; webhooks are unavailable. Read it before designing a method.
3. [`docs/TESTING.md`](TESTING.md) — what the fake must be able to do, including misbehave on demand.
4. [`docs/PARALLEL.md`](PARALLEL.md) — the definition of done that gates the PR.
5. [`AGENTS.md`](../AGENTS.md) — the working agreement.

Dependencies to add in P0.1, all confirmed current as of 2026-08:

| Module | Note |
|---|---|
| `charm.land/bubbletea/v2` | v2 shipped 2026-02. `View()` returns `tea.View`; `KeyPressMsg`/`MouseClickMsg` replace `KeyMsg`/`MouseMsg`; mouse mode is declared on the view, not as a program option |
| `charm.land/lipgloss/v2` | confirmed vanity path |
| `charm.land/bubbles/v2` | confirmed when P0.1 merged; the path this table used to say was unverified |
| `github.com/lrstanley/bubblezone/v2` | mouse hit-testing by zone, not coordinate arithmetic. The v2 path, not the unversioned one |
| `go.etcd.io/bbolt` | cache |

`go.mod` is the current answer to all of it and has four more besides — `BurntSushi/toml`,
`zalando/go-keyring`, `charmbracelet/x/ansi` and `golang.org/x/sync`. This table is what P0.1 was told
to add, kept because the two corrections in it are the kind that cost an afternoon.

The port signature **freezes when P0.1 merges**. Extending it later needs its own small PR labelled
`contract`; changing an existing signature needs a deprecation step.

## 4. Then fan out

Batch 1 opens and five packets can run in parallel. `docs/ROADMAP.md` has the order and every
packet's owned paths; each links to its issue.
