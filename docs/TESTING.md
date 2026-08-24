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
invent the words. `scripts/checkleak.py` then proves no string of yours came across; run it before
committing. There are also tests asserting no fixture contains an `@` beyond the placeholder, a host
other than `example.atlassian.net`, or anything shaped like a live Atlassian account ID.

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
  async cases.
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

A test in `internal/arch` asserts the layering from `docs/ARCHITECTURE.md`. Five rules, one table
entry each:

- `pkg/**` must not import `internal/**`
- `internal/ui/**` must not import `pkg/jira/cloud`
- only `cmd/**` and `internal/config` may construct a concrete adapter
- `internal/app` must not import `internal/ui`
- `pkg/adf` must not import `pkg/jira`

The sixth rule the layer diagram implies — `internal/store` must not import `internal/ui` — waits for
P3.2 to create the package; its case is already in the table, expecting nothing.

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

The test only checks the line exists, in a file that is part of the build rather than in a `_test.go`
— the compiler checks that it is *true*, and does so at `go build` time. Nobody had written one for
`pkg/jira/cloud`, so it implemented 12 of the port's 34 methods for two whole batches while passing
lint, vet, race and a cross-build. Nothing outside the package assigned a `*Client` to anything, so
nothing ever asked.
