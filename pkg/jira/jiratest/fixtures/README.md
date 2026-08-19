# Fixtures

Scrubbed Jira API responses replayed by `jiratest.Server`. Captured with
[`scripts/capture.sh`](../../../../scripts/capture.sh) against a real site by a human — never in CI.

**Nothing in this directory may contain a real name, email address, account ID or site host.** The
capture script scrubs those, but it is best-effort; a test asserts no fixture contains an `@` outside
`user@example.com` or a hostname other than `example.atlassian.net`. Read the diff before committing.

Hand-authored (cannot be captured): `rate_limited.json` (429 with `Retry-After`),
`bulkmove_task_*.json` (one per task state), `validation_error.json` (400 with per-field errors).
