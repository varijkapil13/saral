# Jira API notes

Verified against the Jira Cloud Platform REST API v3 and Agile API 1.0 in August 2026. Every entry
here cost time to find out; read this before writing an adapter method.

## Hard constraints

| Fact | Consequence |
|---|---|
| `/rest/api/3/search` returns **410 Gone** | Use `POST /rest/api/3/search/jql`. Not optional. |
| `/search/jql` pages by opaque `nextPageToken` and returns **no `total`** | The UI must work without a count. Use `POST /search/jql/approximate-count` where a number is genuinely needed. Guard against a token that repeats. |
| `/search/jql` returns almost nothing without `fields` | Always send an explicit, narrow field list. |
| The Agile API still uses `startAt`/`maxResults`/`total` | Two pagination models in one client, unified behind `Page[T]`. It also silently truncates against an unreadable instance limit. |
| v3 requires **ADF** for description, environment, comments, worklog comments and multi-line custom fields | Single-line fields take plain strings. `pkg/adf` is not optional. |
| `PUT /rest/agile/1.0/sprint/{id}` is a **full replace** — omitted fields become null | Never expose it. Use `POST /rest/agile/1.0/sprint/{id}` for partial updates. |
| Sprint state machine: `future → active → closed` only, start needs both dates set, `completeDate` is never writable, a closed sprint only accepts `name` and `goal` | Validate locally and return a real error instead of a 400. |
| `POST /rest/agile/1.0/backlog/issue` caps at **50 issues** | Chunk, and report partial progress. |
| Attachment upload needs `X-Atlassian-Token: no-check` and a multipart part named exactly `file` | Otherwise rejected by the XSRF guard. |
| `GET /rest/api/3/attachment/content/{id}` supports `Range`, and 303-redirects unless `redirect=false` | Free progress and resume. |
| Dynamic webhooks (`POST /rest/api/3/webhook`) are **Connect / OAuth 2.0 apps only** | No push channel for an API-token client. Poll, scoped and backing off. |
| The Plans API requires **Administer Jira** on every endpoint, and is experimental | Per-plan View/Edit rights in the UI do **not** grant API access. Fall back to locally defined plans. |
| `POST /rest/api/3/bulk/issues/move` is async, ≤1000 issues, needs global **Bulk Change** plus Move in source and Create in target | Poll the returned task. Follow the link off the submit response rather than hardcoding the status path. |
| There is **no release-notes API**. `ReleaseNote.jspa` is a rendered web page | Out of scope by decision. |
| Releasing a version is one `PUT` with `released: true` — and it will happily release with open issues | Always check `unresolvedIssueCount` first and make the user decide. |
| `editJiraIssue`-style field writes cannot change an issue's project | Cross-project moves go through the bulk-move endpoint only. |
| Status is not a writable field | Only `POST /issue/{key}/transitions`, and the available transitions depend on current status. |

## Things that vary per instance — never hardcode

| Thing | Where to get it |
|---|---|
| Custom field IDs (story points, start date, target dates) | `GET /rest/api/3/field`, resolved **by name** |
| Estimation field for a board | `GET /rest/agile/1.0/board/{id}/configuration` → `estimation.field.fieldId` (or `type: none`) |
| Rank field | same response → `ranking.rankCustomFieldId` |
| Column → status mapping | same response → `columnConfig.columns[].statuses` |
| Required fields for create | `GET /rest/api/3/issue/createmeta` per project + issue type |
| Available transitions | `GET /rest/api/3/issue/{key}/transitions` per issue |
| Whether attachments are enabled | `GET /rest/api/3/configuration` |
| Permissions | `GET /rest/api/3/mypermissions?permissions=…` |
| The user's timezone | `GET /rest/api/3/myself` |

Group board columns by `statusCategory` (three fixed values: To Do, In Progress, Done), never by
status name — "In Progress" is not guaranteed to exist.

## Rate limiting

Honour `Retry-After` on 429. Back off exponentially with jitter, cap concurrent requests, and pause
any poller on the first 429. Cost-based limits mean a burst of narrow requests beats one wide one.

## Still to confirm against live responses

- The exact bulk-move task-status path (read it off the submit response).
- The precise shape of `GET /rest/api/3/configuration`.
- `charm.land/bubbles/v2` module path (bubbletea and lipgloss v2 vanity paths are confirmed).
