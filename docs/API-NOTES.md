# Jira API notes

Verified against the Jira Cloud Platform REST API v3 and Agile API 1.0 in August 2026. Every entry
here cost time to find out; read this before writing an adapter method.

## Hard constraints

| Fact | Consequence |
|---|---|
| `/rest/api/3/search` returns **410 Gone** | Use `POST /rest/api/3/search/jql`. Not optional. |
| `/search/jql` pages by opaque `nextPageToken` and returns **no `total`** | The UI must work without a count. Use `POST /rest/api/3/search/approximate-count` — no `/jql` segment — where a number is genuinely needed. Guard against a token that repeats. |
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
| `POST /rest/api/3/bulk/issues/move` is async, ≤1000 issues, needs global **Bulk Change** plus Move in source and Create in target | Poll the returned task. |
| The bulk-move submit response is `{"taskId": "10641"}` and nothing else — the schema is `additionalProperties: false`, so there is **no link to follow** | Progress is `GET /rest/api/3/bulk/queue/{taskId}`, which the client has to construct. That is not the generic `GET /rest/api/3/task/{taskId}`, which answers a different shape (`status`, `progress`, `submittedBy`, `elapsedRuntime`). |
| Bulk-move progress reports `status`, `progressPercent`, `processedAccessibleIssues`, `failedAccessibleIssues`, `invalidOrInaccessibleIssueCount`, `totalIssueCount`, and epoch-millis `created`/`started`/`updated` | The failure detail is counts and issue IDs, not messages. |
| There is **no release-notes API**. `ReleaseNote.jspa` is a rendered web page | Out of scope by decision. |
| Releasing a version is one `PUT` with `released: true` — and it will happily release with open issues | Always check `unresolvedIssueCount` first and make the user decide. |
| `editJiraIssue`-style field writes cannot change an issue's project | Cross-project moves go through the bulk-move endpoint only. |
| Status is not a writable field | Only `POST /issue/{key}/transitions`, and the available transitions depend on current status. |
| The platform and Agile APIs write date-times differently: `2021-01-17T12:34:00.000+0000` (no colon in the offset) versus `2015-04-11T15:22:00.000+10:00` (colon) | Two layouts, `2006-01-02T15:04:05.000-0700` and `2006-01-02T15:04:05.000-07:00`. One decoder cannot serve both. `/task/{id}` timestamps are epoch millis instead. |
| The `/search/jql` response has exactly six fields — `issues`, `nextPageToken`, `isLast`, `names`, `schema`, `warnings` | There is no `total`, confirmed against the OpenAPI schema. Treat an absent `nextPageToken` as the end and `isLast` as advisory. |
| `statusCategory.id` is a **number** while `status.id` is a **string** | The four categories are fixed on every site: 1 `undefined`, 2 `new`, 3 `done`, 4 `indeterminate`. Branch on `key`; `name` is localised. |
| `GET /rest/api/3/issue/createmeta?expand=…` is deprecated | Use the paginated pair, `GET /issue/createmeta/{projectIdOrKey}/issuetypes` then `…/issuetypes/{issueTypeId}`. |

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

## ADF

| Fact | Consequence |
|---|---|
| The published JSON schema (`go.atlassian.com/adf-json-schema`) and the prose docs disagree, and the prose is the stale one | Model from the schema. It has node types the prose omits — `taskList`, `decisionList`, `layoutSection`, `extension`, `blockCard`, `embedCard`, `placeholder` — all of which Jira stores. |
| Key order in the JSON is neither documented nor stable | A byte-stable round trip is only possible by keeping the original bytes. `pkg/adf` does exactly that, and re-encodes only the subtrees that changed. |
| Marks come back from the editor in ProseMirror rank order (`link, em, strong, strike, subsup, underline, code, textColor, backgroundColor, …`) | The REST API accepts any order, but anything a human opens in the browser comes back reordered. Mark order is byte-significant and semantically meaningless — never diff on it. |
| The validator *repairs* rather than rejects: unknown `attrs` keys are deleted and an `unsupportedNodeAttribute` mark is appended | Sending a document you rebuilt from a partial model is lossy even when it validates. Send back what you were given wherever you did not edit. |
| `text` on a text node has `minLength: 1` | An empty text node is invalid. Drop it; never emit `"text": ""`. |
| A node's permitted marks depend on where it sits — the schema encodes this with `_with_no_marks` / `_with_alignment` / `_root_only` variants | A paragraph at the root may carry `alignment`; the same paragraph inside a list item may carry none. |

Group board columns by `statusCategory` (three fixed values: To Do, In Progress, Done), never by
status name — "In Progress" is not guaranteed to exist.

## Rate limiting

Honour `Retry-After` on 429. Back off exponentially with jitter, cap concurrent requests, and pause
any poller on the first 429. Cost-based limits mean a burst of narrow requests beats one wide one.

## Still to confirm against live responses

- The precise shape of `GET /rest/api/3/configuration`.
- Everything above is from the published OpenAPI schemas, not from a live site. `scripts/capture.sh`
  has not been run yet, so the fixtures in `pkg/jira/jiratest/fixtures/` are hand-authored to those
  schemas. Re-capture against a real instance before trusting a field name that only matters once.

Module paths, all confirmed against the proxy in August 2026: `charm.land/bubbletea/v2`,
`charm.land/lipgloss/v2`, `charm.land/bubbles/v2` and `github.com/lrstanley/bubblezone/v2` (the v1
bubblezone still depends on Bubble Tea v1 and must not be used).
