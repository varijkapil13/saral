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
- No network in any test. A CI check fails the build if a test opens a non-loopback connection —
  once PC.4 ([#33](https://github.com/varijkapil13/saral/issues/33)) lands. Until then the rule is
  enforced by review, and it is exactly as load-bearing either way.
- Test names describe behaviour: `TestReleaseVersion_RefusesWhenUnresolvedIssuesExist`.

## Import boundaries

A test in `internal/arch` asserts the layering from `docs/ARCHITECTURE.md`:

- `pkg/**` must not import `internal/**`
- `internal/ui/**` must not import `pkg/jira/cloud`
- only `cmd/**` and `internal/config` may construct a concrete adapter

This catches the most common way a modular design quietly stops being modular.
