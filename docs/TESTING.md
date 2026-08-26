# Testing

The rule that shapes everything: **the full test suite runs with no Jira instance, no network and no
credentials.** That is what lets many agents work at once without sharing a sandbox, and what makes
CI honest.

## Three levels

### 1. The in-memory fake — `pkg/jira/jiratest`

A complete implementation of the `jira.Client` port backed by maps. Use it for anything above the
adapter: use cases, views, workflows.

```go
c := jiratest.New(
	jiratest.WithProject("PROJ", jiratest.Scrum),
	jiratest.WithIssues(jiratest.Gen(500)),
	jiratest.WithCapabilities(jiratest.NoBulkMove, jiratest.NoPlans),
)
```

It must be able to *misbehave on demand*, because the failure paths are the interesting ones:

```go
c.FailNext(&jira.RateLimitError{RetryAfter: 30 * time.Second})
c.FailNext(&jira.CapabilityError{Reason: "needs Bulk Change permission"})
c.Delay(200 * time.Millisecond)          // to test spinners and cancellation
c.CursorLoop()                           // reproduce Jira's repeated-token bug
```

### 2. The HTTP fixture server — `jiratest.Server`

An `httptest.Server` replaying recorded JSON from `pkg/jira/jiratest/fixtures/`. This is how the
`cloud` adapter is tested: real wire bytes, real headers, no live site.

**A capture is not a fixture.** `scripts/capture.sh` (needs a real token, run by a human, never in
CI) writes to `testdata/live/fixtures/`, which is gitignored, and nothing there is ever committed. A
response from a company's Jira carries ticket summaries, release names, custom field names, board
names and plan names, and no scrubber can tell those from the shape around them. The scrubber removes
*identity* — account IDs, emails, avatars, the site host, the names inside ADF mentions — which is a
different and much smaller problem.

What a capture is for is the **shape**: the keys, the nesting, the types, the date formats, the paging
envelopes. When a committed fixture is wrong about one of those, correct it from the capture and
invent the words.

`scripts/checkleak.py` is what proves the second half happened, and it runs in two halves because
only one of them can be mechanical. The half CI runs on every pull request needs no capture: the
fixture tree has to be non-empty, every file in it has to parse, every absolute URL has to name the
invented site, and `testdata/live/` has to be both untracked and still ignored — deleting that
ignore rule is the quiet way a capture ends up in the history, because nothing breaks until the next
`git add -A`. `pkg/jira/jiratest/fixtures_test.go` asserts the rest of the shape rules over the same
tree: no `@` beyond the placeholder, no host other than `example.atlassian.net`, nothing shaped like
a live Atlassian account ID or a credential.

The other half compares the committed fixtures against your capture, string by string, ignoring the
vocabulary Jira and HTTP ship identically everywhere. **No runner can run it** — a capture only
exists on the machine that took one, and a CI step over that half would be green for the same reason
it was green before anyone wrote it. So it is yours: `scripts/checkleak.py --require-capture` after a
capture, which fails rather than skips when there is nothing to compare against. `scripts/checkleak_test.py`
covers both halves against planted leaks, and CI runs it before it runs the check.

Fixtures to keep, at minimum: a rich ADF description, a paginated search response and its second
page, `createmeta` for two different projects, a board configuration with and without estimation,
`mypermissions` for an admin and a non-admin, a 429 with `Retry-After`, and a bulk-move task in each
of its states.

### 3. Golden files for rendering

Views render into a fixed-size buffer and compare against `testdata/*.golden`. Update with
`go test ./... -update`, and read the diff before committing it — a golden file changing is a UI
change and belongs in the PR description.

```go
func TestBoardView(t *testing.T) {
	got := render(t, board.New(deps), 120, 40)
	golden.Assert(t, got, "board_120x40.golden")
}
```

Bubble Tea's `teatest` drives full programs where an interaction sequence matters.

## What must be tested

| Area | Required |
|---|---|
| Any adapter method | success, 4xx mapped to the right typed error, 429, malformed body, cancellation |
| Pagination | both models, exhaustion, repeated-cursor guard |
| `pkg/adf` | round-trip byte-stability on untouched documents; unknown nodes preserved |
| Forms | required-field validation from `createmeta`, `ValidationError` mapped to the right field |
| Capabilities | every view's behaviour when its capability is absent |
| Rendering | golden file at a couple of widths, plus a narrow terminal |
| Destructive flows | the confirm step is unskippable; release checks unresolved count first |

## Conventions

- Table-driven tests, subtests named after the case in prose.
- `t.Parallel()` wherever there is no shared state — which should be everywhere.
- No `time.Sleep` in tests. Inject a clock; `jiratest.Delay` plus `teatest`'s wait helpers cover the
  async cases. `app.WithClock` is there for exactly this: every cache TTL is checked by winding a
  clock forward, never by waiting for one.
- **A test that parks a handler has to be able to fail.** `httptest.Server.Close` waits for every
  handler still running, so one parked on a channel nothing closes holds `Close` there until `go
  test` gives up on the entire package — ten minutes, reported as a hang rather than as the
  assertion that failed. `pkg/jira/cloud` has three helpers for the shape: `gate`, whose release is
  idempotent and is deferred *after* the server's close so that it runs first; `receive` in place of
  a bare channel receive; and `closeServer` in place of `s.Close`. A handler says it arrived by
  closing a channel and never by sending on one — a send nobody is left to receive cannot be freed
  by anything, which is how the coalescing test wedged a CI job for its full timeout.
- No network in any test, and CI is what makes that true. The race suite runs inside a network
  namespace with only loopback up, so a test that reaches for a real host fails the build instead of
  passing for whoever wrote it. A step before it compiles every test binary while the network is
  still reachable, because the namespace has no route to the module proxy; `GOPROXY=off` inside turns
  a cache miss into a legible error rather than a timeout. The jailed step first proves it isolates —
  it attempts a request to the module proxy and fails the build if that request gets out — because an
  `unshare` that quietly stopped working reads as a hermetic suite. A test that needs a server starts
  one on loopback with `httptest`, which is what `jiratest.Server` does; `internal/arch` asserts that
  the workflow still runs the suite that way.
- Test names describe behaviour: `TestReleaseVersion_RefusesWhenUnresolvedIssuesExist`.

## Import boundaries

A test in `internal/arch` asserts the layering from `docs/ARCHITECTURE.md`. Seven rules, one table
entry each:

- `pkg/**` must not import `internal/**`
- `internal/ui/**` must not import `pkg/jira/cloud`
- only `cmd/**` and `internal/config` may construct a concrete adapter
- `internal/app` must not import `internal/ui`
- `pkg/adf` must not import `pkg/jira`
- `internal/store` must not import `internal/ui`
- `internal/ui/**` must not import `internal/store`

A second test reads the table itself, because a rule can be wrong in a way that only ever shows up as
green: a duplicated name, a missing `why`, an exemption that no package the rule covers could match,
or one broad enough to swallow the rule's whole scope.

The rules only see imports within this module. "No IO libs in `internal/app`" is a convention nobody
can check this way — a `net/http` import there is invisible to the walk.

This catches the most common way a modular design quietly stops being modular.

## Adapters say what they satisfy

A third test in `internal/arch` fails any package under `pkg/jira/**` that adapts the port and never
writes down which of its interfaces it satisfies:

```go
var _ jira.Prober = (*Client)(nil)
```

The test checks the lines exist, in a file that is part of the build rather than in a `_test.go` —
the compiler checks that they are *true*, and does so at `go build` time. One of them has to be
`jira.SessionClient`, the composite a session is built with, so that an adapter cannot shed its
single-role claims one at a time and still pass. Nobody had written one for `pkg/jira/cloud`, so it
implemented 12 of the port's 34 methods for two whole batches while passing lint, vet, race and a
cross-build. Nothing outside the package assigned a `*Client` to anything, so nothing ever asked.

## Adapters answer the same question the same way

An assertion is only worth what it is run against. Everything above the port is tested against the
fake, so a rule the cloud adapter enforces and the fake does not is a rule no test ever meets — and
the suite stays green while the binary fails against a site. That has happened twice: the fake
accepted a whitespace-only field list the site refuses, and the fake accepted a 200 naming nobody
that the cloud adapter refuses, which is the answer onboarding reads as proof a credential works.

`pkg/jira/cloud/conformance_test.go` runs one table of cases over both adapters through the port role
that names the method — `jira.Identifier` for `Me`. Each case builds a site in each adapter's own
terms (a replay server for `cloud`, an option for `jiratest`) and then asserts the same thing about
the answer, so a divergence fails on the adapter that has it.

Three tables stand beside each other today: `Me` in `conformance_test.go`, the five methods a filter
picker calls in `conformance_people_test.go`, and the account on an issue in
`conformance_search_test.go` — the last because the adapter dropped `accountType` from an issue read
while the picker badged app accounts by it, so one screen said an account was an app and the next
said nothing. A new adapter method is a new table beside them; a harness over all 34 port methods is
[#74](https://github.com/varijkapil13/saral/issues/74) and deliberately not this.
