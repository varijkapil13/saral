# Fixtures

Synthetic Jira API responses replayed by `jiratest.Server`, describing one invented site: project
`EX` on `example.atlassian.net`.

**Nothing here is a capture.** [`scripts/capture.sh`](../../../../scripts/capture.sh) writes to
`testdata/live/fixtures/`, which is gitignored and stays that way — a real response carries ticket
summaries, release names, field names and board names that belong to whoever ran it. A capture is
used to correct the *shape* of these files: keys, nesting, types, date formats, paging envelopes. The
words are invented. [`scripts/checkleak.py`](../../../../scripts/checkleak.py) proves the two halves
stayed separate and is worth running before you commit a change here.

**Nothing in this directory may contain a real name, email address, account ID or site host.** The
capture script scrubs those, but it is best-effort; a test asserts no fixture contains an `@` outside
`user@example.com` or a hostname other than `example.atlassian.net`. Read the diff before committing.

The shapes were modelled on Atlassian's published OpenAPI schemas and then corrected against a real
capture. The `@` rule has one deliberate exception beyond the placeholder address: ADF writes the
sign into a mention's `text` attribute, so a leading `@user` is allowed.

Deliberately not Jira's stock vocabulary: the statuses are `Backlog` / `In Review` / `Released`, so
code that hardcodes `"In Progress"` fails here rather than in front of a user.

Hand-authored (cannot be captured): `rate_limited.json` (429 with `Retry-After`),
`bulkmove_task_*.json` (one per task state), `validation_error.json` (400 with per-field errors).
