## Packet

<!-- e.g. P1.5 — Issue list and detail views. One packet per PR. -->

Closes #

## What changed

<!-- User-facing prose. If a golden file changed, say what changed visually and why. -->

## Definition of done

- [ ] Works against `pkg/jira/jiratest` — no live Jira needed to run the tests
- [ ] Failure paths tested: 403 (capability), 429 (rate limit), transport error
- [ ] Golden-file tests for any rendering; diffs reviewed, not blind-updated
- [ ] Nothing instance-specific hardcoded (field IDs, statuses, project keys, permissions)
- [ ] Keybindings and mouse zones registered, not hardcoded
- [ ] Benchmarks added/updated if a render or list path was touched
- [ ] Stayed inside the packet's owned paths
- [ ] `make check` green
- [ ] `docs/ROADMAP.md` checkbox ticked in this PR
