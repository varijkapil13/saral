# Performance

Saral should feel instant on a 10,000-issue project and start faster than a browser tab can paint.

These are budgets, not aspirations — but only where the **Guarded by** column below says so. A
benchmark on its own guards nothing: it prints a number, and for three batches the frame budgets were
held because a person ran `make bench` and read the output after each merge. Remove the person and
the budget is a comment. Every row that says *measured, not guarded* is one no build will fail on
today; comparing a run against a baseline is P9.2
([#30](https://github.com/varijkapil13/saral/issues/30)) and is the thing that will close them.

## Budgets

| Metric | Budget | Guarded by |
|---|---|---|
| Cold start → first paint (no cache) | **< 250 ms** | `ci.yml`, best of five `saral --bench-first-paint` runs against an empty cache directory |
| Cold start → first paint (warm cache) | < 60 ms | *measured, not guarded.* `hyperfine` on `saral --bench-first-paint`. Warming the cache needs a site, so CI cannot; the in-process half is `TestBudget_FirstPaintFromCache` |
| Keystroke → frame, steady state | **mean < 16 ms** at 10k rows | asserted in every view that takes a keystroke — list, issue, comment, filter, the timeline, the palette, the form and the kernel chrome. The budget used to read *p99*; a benchmark reports a mean and keeps no distribution, so p99 waits on #30 |
| Scroll a 10k-row list | 1 allocation a frame | the frame string `View` returns, and nothing behind it. Asserted with the mouse on, under a kept filter and under terms in force |
| Pan a chart across a thousand years of calendar | the allocations, the bytes and no more than twice the time that ten years costs | the timeline is the one view that scrolls in two dimensions. `TestBudget_TimelinePanningCostsTheSameOverAThousandYearsAsOverTen` compares all three because each catches a different mistake, and holds the count to a ceiling of 1700 besides |
| Frame allocations at 200×60 | ceilings in `internal/ui/kernel/budget_test.go` | 297 for a frame, 310 for a keystroke and its frame, 324 with the mouse on, each held to a ceiling about a tenth above |
| Full redraw at 200×60 | < 4 ms | asserted in list, issue, comment, filter, the timeline, the form and the kernel chrome |
| RSS with 10k issues cached | < 60 MB | *measured, not guarded.* Nothing reads the number; the harness that would is #30 |
| Stripped binary | **< 15 MiB** | `ci.yml`'s size step |
| Cache read for a view's first paint | < 5 ms | `BenchmarkCacheReadFirstPaint` |
| Rank 10k cached issues against a keystroke | **< 16 ms**, 1 allocation | `BenchmarkIndexSearch10k` and its two siblings |
| Rebuild the local index over 10k cached issues | < 16 ms | `BenchmarkIndexRebuild10k` |
| Resolve the date cascade over a timeline's worth of issues | **< 16 ms**, and linear in the issues | `BenchmarkResolveDates2k` against `BenchmarkResolveDates10k` |
| Render a description to styled lines | **≤ 8 allocations a line**, and linear in the lines | `BenchmarkRender`, over four widths and both palettes |

## How a budget is guarded

A guard is a test named `TestBudget_*` that runs a benchmark and fails on the number it comes back
with. Five things make one of them worth trusting, and each was got wrong once here:

- **It is absolute, with headroom.** A guard that only compares two benchmarks against each other
  catches a memo that stopped being hit and passes just as happily at nine hundred allocations a
  frame. The frame ceilings are numbers, set about a tenth above what the machine measures. The
  counts are exact rather than noisy — five runs of each produce the same figure — so a tenth is room
  for a compiler release to move one and not room for a regression to hide in. Tightening a ceiling
  after a real improvement is a judgement call and deliberately not automatic.
- **It runs without the race detector, and CI runs it anyway.** The detector puts about twenty times
  the cost on these paths, so a time budget under `-race` measures the instrumentation. Every guard
  is therefore built `//go:build !race` — and a `!race` tag on its own means a test that runs
  *nowhere*, because the only suite CI ran was the race one. The `budgets` job exists to close that:
  it runs the guards without the detector, inside the same loopback-only namespace the race suite
  uses, and proves the namespace isolates before it trusts it. What the job cannot see is a
  wall-clock assertion that never became a guard at all: three in the palette, two in the form and
  one in `cmd/saral` sat in an untagged file, ran under the detector, and failed on whichever run
  lost the lottery — about half of them. `TestBudget_EveryWallClockAssertionSitsInAGuard` is why
  that shape now fails the build instead of the suite.
- **It is not parallel.** An allocation count comes from process-wide `MemStats`, so a benchmark run
  beside another test is handed that test's allocations divided by its own iteration count. Measured
  here: a scroll that costs 1 allocation a frame reports **733** when one neighbouring parallel test
  allocates hard for the same second, and **226** without the detector. That is what made four list
  guards fail on a runner and pass on a laptop, and the detector's part in it was not its own
  allocations — it was slowing the benchmark from 617,000 iterations a second to 52,000, so the
  neighbours' noise was divided by a twelfth as much.
- **It measures one state, not a blend of two.** `BenchmarkListWalk10k` held `j` down and stopped
  at the last row, so every iteration past the ten thousandth was a memo hit rather than the memo
  miss the benchmark said it was measuring. What it reported was the two states averaged, weighted
  by how many iterations the machine got through in its second — 1 allocation a frame on an M2 Pro,
  2 compiled for amd64, 3 on the runner, none of them a fact about the code. It goes back to the top
  on reaching the bottom now, and reports 42 everywhere. A budget over a number that moves with the
  machine is not a budget, and the way that shows up is a ceiling that is red on the runner and green
  on the laptop it was written on.
- **It holds a timing against a number, never against another timing.** Two `testing.Benchmark`
  calls each pick their own iteration count and each meets whatever else the machine was doing during
  its second, so a ratio or a difference between their `ns/op` is a ratio between two independent
  samples rather than a fact about the code. The date cascade's linearity guard divided them, and
  measured this: one two-thousand-issue run read **11.4 ms** against another two-thousand-issue run's
  **2.4 ms**, slower than the fastest ten-thousand run, while the allocation counts repeated to the
  unit — 2,300 against 11,173. It compares allocations now, which are deterministic and are the one
  thing two benchmarks may be held against each other on. A `!race` tag does not make the timing
  shape sound, because it is a measurement problem and not a wall-clock one, so
  `TestBudget_NoBudgetDividesOneBenchmarksTimeByAnothers` fails the build on it wherever it appears —
  including inside a budget file, which is where it came back the second time.

Run them the way CI does:

```sh
go test -count=1 -parallel 1 -run '^TestBudget_' ./...
```

### The guards

Every `TestBudget_*` in the tree is listed here, and the list is checked both ways: `internal/app`
fails if a name here has no test or a test is missing from here, and the `budgets` job fails if the
set that actually ran is not this one. So a guard cannot be deleted quietly — only by editing this
table, which is the same thing as writing down that the budget is no longer held.

<!-- budget-guards -->

| Package | Guard |
|---|---|
| `internal/app` | `TestBudget_CacheReadForAViewsFirstPaint` |
| `internal/app` | `TestBudget_CIRunsTheGuardsWithoutTheDetector` |
| `internal/app` | `TestBudget_DateCascadeCostsNoMoreThanTheIssuesItIsGiven` |
| `internal/app` | `TestBudget_DateCascadeOverATimelineOfIssues` |
| `internal/app` | `TestBudget_EveryWallClockAssertionSitsInAGuard` |
| `internal/app` | `TestBudget_IndexRebuildAtTenThousandIssues` |
| `internal/app` | `TestBudget_IndexSearchAllocatesOnlyTheAnswerItHandsBack` |
| `internal/app` | `TestBudget_IndexSearchAtTenThousandIssues` |
| `internal/app` | `TestBudget_NoBudgetDividesOneBenchmarksTimeByAnothers` |
| `internal/app` | `TestBudget_TheDocumentNamesEveryGuardAndOnlyRealOnes` |
| `internal/ui/attach` | `TestBudget_AttachAMemoLookupCostsNothing` |
| `internal/ui/attach` | `TestBudget_AttachFullRedrawAt200x60` |
| `internal/ui/attach` | `TestBudget_AttachKeystrokeToFrame` |
| `internal/ui/attach` | `TestBudget_AttachRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/attach` | `TestBudget_AttachScrollingCostsTheSameOnTwoThousandFilesAsOnTwenty` |
| `internal/ui/backlog` | `TestBudget_BacklogAMemoMissCostsOneRowAndNotAWindow` |
| `internal/ui/backlog` | `TestBudget_BacklogFullRedrawAt200x60` |
| `internal/ui/backlog` | `TestBudget_BacklogKeystrokeToFrameAtTenThousandIssues` |
| `internal/ui/backlog` | `TestBudget_BacklogPickingAnIssueCostsOneRowAndTheFrame` |
| `internal/ui/backlog` | `TestBudget_BacklogRegroupingAfterAMoveIsOnTheKeystrokeBudget` |
| `internal/ui/backlog` | `TestBudget_BacklogRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/backlog` | `TestBudget_BacklogScrollingCostsTheSameOnTenThousandRowsAsOnTwenty` |
| `internal/ui/comment` | `TestBudget_FullRedrawAt200x60` |
| `internal/ui/comment` | `TestBudget_KeystrokeToFrameOnATenThousandCommentThread` |
| `internal/ui/comment` | `TestBudget_ScrollingCostsTheSameOnTenThousandCommentsAsOnTwenty` |
| `internal/ui/filter` | `TestBudget_PickerFullRedrawAt200x60` |
| `internal/ui/filter` | `TestBudget_PickerKeystrokeToFrame` |
| `internal/ui/filter` | `TestBudget_PickerRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/filter` | `TestBudget_PickerScrollingCostsTheSameOnTwoThousandRowsAsOnTwenty` |
| `internal/ui/filter` | `TestBudget_RankingReusesItsBuffers` |
| `internal/ui/form` | `TestBudget_FormFullRedrawAt200x60` |
| `internal/ui/form` | `TestBudget_FormKeystrokeToFrameOnALongScreen` |
| `internal/ui/issue` | `TestBudget_DragCostsAFrameWhileHeldAndAResizeWhileMoving` |
| `internal/ui/issue` | `TestBudget_FullRedrawAt200x60` |
| `internal/ui/issue` | `TestBudget_KeystrokeToFrame` |
| `internal/ui/issue` | `TestBudget_ScrollingCostsNoMoreThanStandingStill` |
| `internal/ui/issue` | `TestBudget_SteadyFrameCostsTheFrameAndTheThreadAndNothingElse` |
| `internal/ui/kernel` | `TestBudget_AFrameCostsWhatTheChromeCosts` |
| `internal/ui/kernel` | `TestBudget_FullRedrawAt200x60` |
| `internal/ui/kernel` | `TestBudget_KeystrokeToFrame` |
| `internal/ui/list` | `TestBudget_FirstPaintFromCache` |
| `internal/ui/list` | `TestBudget_FullRedrawAt200x60` |
| `internal/ui/list` | `TestBudget_KeystrokeToFrameAtTenThousandRows` |
| `internal/ui/list` | `TestBudget_RowRenderingCostsNothingOnceMemoized` |
| `internal/ui/list` | `TestBudget_ScrollingCostsTheSameOnTenThousandRowsAsOnTwenty` |
| `internal/ui/list` | `TestBudget_ScrollingCostsTheSameUnderAFilterThatHasBeenAccepted` |
| `internal/ui/list` | `TestBudget_ScrollingCostsTheSameUnderTermsInForce` |
| `internal/ui/list` | `TestBudget_ScrollingCostsTheSameWithTheMouseOn` |
| `internal/ui/list` | `TestBudget_AMemoMissCostsOneRowAndNotAWindow` |
| `internal/ui/move` | `TestBudget_MoveFullRedrawAt200x60` |
| `internal/ui/move` | `TestBudget_MoveKeystrokeToFrame` |
| `internal/ui/move` | `TestBudget_MoveRemapKeystrokeToFrame` |
| `internal/ui/move` | `TestBudget_MoveRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/move` | `TestBudget_MoveScrollingCostsTheSameOnAThousandIssuesAsOnTwenty` |
| `internal/ui/palette` | `TestBudget_PaletteKeystrokeOverEveryCachedIssue` |
| `internal/ui/palette` | `TestBudget_PaletteKeystrokeOverTwoThousandCommands` |
| `internal/ui/palette` | `TestBudget_PaletteOpeningIsOnTheKeystrokeBudget` |
| `internal/ui/release` | `TestBudget_ReleaseFlowFullRedrawAt200x60` |
| `internal/ui/release` | `TestBudget_ReleaseFlowScrollingCostsTheSameOnTwoThousandVersionsAsOnTwenty` |
| `internal/ui/release` | `TestBudget_ReleasesAMemoMissCostsTheRowsThatMovedAndNotAWindow` |
| `internal/ui/release` | `TestBudget_ReleasesFullRedrawAt200x60` |
| `internal/ui/release` | `TestBudget_ReleasesKeystrokeToFrame` |
| `internal/ui/release` | `TestBudget_ReleasesRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/release` | `TestBudget_ReleasesScrollingCostsTheSameOnTwoThousandVersionsAsOnTwenty` |
| `internal/ui/sprint` | `TestBudget_SprintRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/sprint` | `TestBudget_SprintScrollingCostsTheSameOnTwoThousandSprintsAsOnTwenty` |
| `internal/ui/sprint` | `TestBudget_SprintsFullRedrawAt200x60` |
| `internal/ui/sprint` | `TestBudget_SprintsKeystrokeToFrame` |
| `internal/ui/timeline` | `TestBudget_TimelineAMemoMissCostsThreeRowsAndNotAWindow` |
| `internal/ui/timeline` | `TestBudget_TimelineAZoomRepaintsAWindowAndNotTheWholeChart` |
| `internal/ui/timeline` | `TestBudget_TimelineFullRedrawAt200x60` |
| `internal/ui/timeline` | `TestBudget_TimelineKeystrokeToFrameAtTenThousandBars` |
| `internal/ui/timeline` | `TestBudget_TimelinePanningCostsTheSameOverAThousandYearsAsOverTen` |
| `internal/ui/timeline` | `TestBudget_TimelineRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/timeline` | `TestBudget_TimelineScrollingCostsTheSameOnTenThousandBarsAsOnTwenty` |
| `internal/ui/plan` | `TestBudget_APlansMemoMissCostsTwoRowsAndNotAWindow` |
| `internal/ui/plan` | `TestBudget_PlanRowsAreMemoizedSoAFrameCostsNothingToRedraw` |
| `internal/ui/plan` | `TestBudget_PlansFullRedrawAt200x60` |
| `internal/ui/plan` | `TestBudget_PlansKeystrokeToFrame` |
| `internal/ui/plan` | `TestBudget_PlansScrollingCostsTheSameOnTwoThousandPlansAsOnTwenty` |
| `internal/ui/plan` | `TestBudget_PlansStandingStillCostsTheFrameAndNothingElse` |
| `internal/ui/richtext` | `TestBudget_Render` |
| `internal/ui/richtext` | `TestBudget_ScalesWithTheDocument` |
| `internal/ui/richtext` | `TestBudget_Summary` |

<!-- /budget-guards -->

### What is still only measured

`make bench` prints these and nothing reads them. They are here so that the list of what is unguarded
is as visible as the list of what is:

- **RSS with 10k issues cached.** No harness measures it.
- **Cold start with a warm cache.** The cache has to be filled from a site first.
- **p99 of anything.** `testing.Benchmark` reports a mean over its iterations and throws the
  distribution away.
- **Every benchmark outside the table** — `pkg/adf`, `pkg/jira/cloud`'s decode and the onboarding
  view — which are watched by eye and by #30 when it lands.

## Where the time actually goes

In a Bubble Tea app of this shape, the costs are, in order:

1. **Styling in a loop.** `lipgloss` style construction per cell dwarfs everything else. Build
   styles once per theme load and reuse them.
2. **Rendering invisible rows.** Virtualize: render the visible window plus a small overscan, never
   the whole dataset.
3. **String width calculation.** Grapheme-aware width is not free. Cache per string, keyed by
   content and target width.
4. **JSON decoding.** Ask for narrow `fields` sets — a list view needs six fields, not sixty. This is
   the single biggest network and CPU win available and it is free.
5. **Re-fetching.** Cache first, coalesce identical in-flight requests, and never fan out a request
   per cursor movement.

Bubble Tea v2's renderer diffs frames for us; the work is to not generate frames that differ
needlessly.

## Rules that follow

- Memoize row rendering keyed by `(updatedAt, width, selected, themeGen)`.
- Reuse buffers; `strings.Builder` with `Grow` on hot paths.
- No `fmt.Sprintf` in a per-row loop where concatenation or a builder will do.
- Bound every cache. The issue cache is LRU with a configurable ceiling, default 5,000 issues.
- Do not hold decoded JSON. Map to domain types at the adapter boundary and let the raw bytes go.
- One goroutine per in-flight request, cancelled when the view closes. No worker pools, no timers
  that outlive their view.
- The optional poller is off by default, scoped to the focused view, and backs off on the first 429.

The `--bench-first-paint` flag used above is built in P0.1: it starts the program, renders the first
frame from cache, prints elapsed microseconds and exits. Without it the two start-up budgets are
unmeasurable, so it is part of the kernel rather than a later addition.

## Benchmark harness

Every packet touching a render path or a list adds benchmarks next to its code, and a `TestBudget_*`
beside them that fails on what they report:

```go
func BenchmarkBoardView10k(b *testing.B) { ... }
func BenchmarkRowRender(b *testing.B)    { ... }

func TestBudget_BoardScrollCostsTheFrameAndNothingElse(t *testing.T) { ... }
```

`make bench` runs the benchmarks; the `budgets` job runs the guards. P9.2 wires `benchstat` into CI
to fail a PR that regresses a budgeted path by more than 10%, which is the piece that turns a
slow slide into a failure rather than only a step change past a ceiling.

### What the local index costs today

Measured on an M2 Pro at ten thousand cached issues — twice the 5,000 the cache itself is bounded to,
so the real worst case is half of each of these:

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `BenchmarkIndexSearch10k` — three runes typed, a screenful asked for | 795,000 | 1 |
| `BenchmarkIndexSearchOneRune10k` — one rune, which nearly everything matches | 1,016,000 | 1 |
| `BenchmarkIndexSearchUnbounded10k` — every hit rather than a screenful | 926,000 | 1 |
| `BenchmarkIndexRebuild10k` — the walk a moved generation costs | 245,000 | 1 |
| `BenchmarkPatternScore` — one candidate scored | 65 | 0 |

The one allocation is the answer handed back, which the caller keeps. Everything behind it — the
rows, the prepared pattern, the ranking buffer — outlives the call or never leaves the stack, and
`app.Pattern` folds case without copying either side, so scoring is allocation-free however many
candidates it is run over. That is the property that made writing the scorer cheaper than taking
`github.com/sahilm/fuzzy`, whose API materialises a `[]string` of every target and a `[]int` of
matched offsets per hit.

### What rendering a document costs today

Measured on an M2 Pro over `internal/ui/richtext`, against the kitchen-sink description a real site
stored: 40 blocks, 31 node types, 11 mark types, 117 lines at 80 columns.

| Benchmark | ns/op | allocs/op |
|---|---|---|
| `BenchmarkRender/80` — a themed render, the width a pane usually has | 119,000 | 702 |
| `BenchmarkRender/40` — the same document in a narrow pane, so more lines | 121,000 | 779 |
| `BenchmarkRenderNoColor` — the same, in the no-colour theme | 109,000 | 525 |
| `BenchmarkRenderLong` — five copies of it, the size a long description reaches | 352,000 | 2,179 |
| `BenchmarkSummary` — a document flattened onto one 80-cell line | 2,000 | 6 |

That is **six allocations a line**, and `internal/ui/richtext/budget_test.go` asserts a ceiling of
eight per line with colour and six without, plus that the cost per line does not grow with the
document. The floor is one a line: the lines are the answer the caller keeps.

**A render is not a per-frame cost.** A pane memoizes it and re-renders on a resize or a change of
data, which is what the ceiling is sized for. Three things were what made it worth measuring:

- `strings.Builder.Reset` drops the buffer, so a builder reused for every line pays an allocation and
  a regrow each time. A `[]byte` truncated to zero keeps its capacity.
- Asking a `lipgloss.Style` to render a run costs a style walk per call, and for an underlined or
  struck-through run lipgloss re-styles **every rune** — a thirteen-letter word comes back as
  thirteen SGR pairs, with an escape sequence inside any grapheme cluster in it. The renderer asks a
  style once what it puts around one rune and then puts that around the whole run, so a run is one
  sequence and a joined emoji stays joined.
- A closure per block is an allocation per block, and a document has a block for every paragraph.

A summary stops at the width it was asked for rather than flattening 32,000 characters and cutting
the result, which is what a list of twenty rows over long descriptions pays.

## Measuring for real

```sh
make bench                        # allocations and ns/op
go test -run TestX -cpuprofile cpu.out ./internal/ui/board && go tool pprof -http=: cpu.out
hyperfine './saral --bench-first-paint'
```

Profile before optimising. The budgets above exist so that "it feels fine on my machine" is never the
standard.
