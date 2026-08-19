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

Fixtures are captured with `scripts/capture.sh` (needs a real token, run by a human, never in CI) and
**must be scrubbed** — no account IDs, no email addresses, no site names, no tokens. The scrubber
runs as part of capture and there is a test asserting no fixture contains an `@` or a real site host.

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
- No network in any test. There is a CI check that fails the build if a test opens a non-loopback
  connection.
- Test names describe behaviour: `TestReleaseVersion_RefusesWhenUnresolvedIssuesExist`.

## Import boundaries

A test in `internal/arch` asserts the layering from `docs/ARCHITECTURE.md`:

- `pkg/**` must not import `internal/**`
- `internal/ui/**` must not import `pkg/jira/cloud`
- only `cmd/**` and `internal/config` may construct a concrete adapter

This catches the most common way a modular design quietly stops being modular.
