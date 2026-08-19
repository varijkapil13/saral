# Performance

Saral should feel instant on a 10,000-issue project and start faster than a browser tab can paint.
These are budgets, not aspirations: CI enforces the ones it can, and benchmarks guard the rest.

## Budgets

| Metric | Budget | How it is measured |
|---|---|---|
| Cold start → first paint (warm cache) | **< 60 ms** | `hyperfine` on `saral --bench-first-paint` |
| Cold start → first paint (no cache) | < 250 ms | same, cache purged; network excluded |
| Keystroke → frame, steady state | **p99 < 16 ms** | benchmark over `Update`+`View` at 10k rows |
| Scroll a 10k-row list | 0 allocations per frame | `-benchmem`, `allocs/op` asserted in the test |
| Full redraw at 200×60 | < 4 ms | render benchmark |
| RSS with 10k issues cached | **< 60 MB** | measured in the bench harness |
| Stripped binary | **< 15 MiB** | enforced in CI (`ci.yml`) |
| Cache read for a view's first paint | < 5 ms | bbolt read benchmark |

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

Every packet touching a render path or a list adds benchmarks next to its code:

```go
func BenchmarkBoardView10k(b *testing.B) { ... }   // asserts allocs/op == 0 in steady state
func BenchmarkRowRender(b *testing.B)    { ... }
```

`make bench` runs them all. P9.2 wires `benchstat` into CI to fail a PR that regresses a budgeted
path by more than 10%.

## Measuring for real

```sh
make bench                        # allocations and ns/op
go test -run TestX -cpuprofile cpu.out ./internal/ui/board && go tool pprof -http=: cpu.out
hyperfine './saral --bench-first-paint'
```

Profile before optimising. The budgets above exist so that "it feels fine on my machine" is never the
standard.
