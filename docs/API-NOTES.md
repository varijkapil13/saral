# Jira API notes

Verified against the Jira Cloud Platform REST API v3 and Agile API 1.0 in August 2026. Every entry
here cost time to find out; read this before writing an adapter method.

## Hard constraints

| Fact | Consequence |
|---|---|
| `/rest/api/3/search` returns **410 Gone** | Use `POST /rest/api/3/search/jql`. Not optional. |
| `/search/jql` pages by opaque `nextPageToken` and returns **no `total`** | The UI must work without a count. Use `POST /rest/api/3/search/approximate-count` — no `/jql` segment — where a number is genuinely needed. Guard against a token that repeats. |
| `/search/jql` returns almost nothing without `fields` | Always send an explicit, narrow field list. |
| The response carries only the field keys it **actually returned** — nothing anywhere says what the endpoint did with the list you sent | The field list cannot be recovered from the answer, so the `jira.FieldMask` on `Issue.Requested` records what was *asked for*, never what the site had. "Requested and absent" means the site sent nothing for that field, and a field ID the site does not have looks exactly the same. Every consumer of the mask has to know that. |
| The Agile API still uses `startAt`/`maxResults`/`total` | Two pagination models in one client, unified behind `Page[T]`. It also silently truncates against an unreadable instance limit. |
| There is a **third** paging style: `/rest/api/3/plans/plan` uses `cursor`/`nextPageCursor` and *does* report a `total` and an `isLast` | It is neither of the other two. `Plans` returns a plain slice for that reason. A fourth shape — no paging at all — covers `/field`, attachment upload and the bare-array `/project/{key}/versions`, which is the one endpoint of that four the client should *not* call: use the paged `/project/{key}/version`. |
| The Plans API lives at `/rest/api/3/plans/plan` — the doubled segment is correct | `GET /rest/api/3/plans` does not exist. |
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
| The generic `GET /rest/api/3/task/{taskId}` answers `TaskProgressBeanObject`: `id`, `self`, `status`, `progress`, `elapsedRuntime`, `submitted`, `lastUpdate`, `submittedBy`, and optionally `description`, `message`, `started`, `finished`, `result` | The two progress bodies overlap on exactly three keys — `status`, `started`, `submittedBy` — and the third has a different **type** in each: an int64 legacy user id here, an object with an `accountId` on the bulk-move queue. A struct written for one either errors on the other or reads `status` and `started` and leaves the rest at their zero values. |
| `submittedBy` on a task progress bean is a **numeric** user id, not an account id | It is the only place left in v3 that answers with the pre-GDPR id. There is no endpoint that turns it back into a user, so it is for display and comparison at most. |
| A task progress bean's `submitted`/`started`/`lastUpdate`/`finished` are **epoch millis**, while the Connect variant `TaskProgress` sends the same four fields as **RFC 3339 strings** | Two shapes, one name, one letter apart in the docs. Anything reading a task's clock has to know which one it asked for. |
| `result` is typed *unknown* in the schema, the published example sends a **string**, and a real task sends whatever its own operation documents — an object for the option-replacement tasks | Keep it as `json.RawMessage` and decode per operation. A `Result string` compiles, passes against a fixture that happens to hold a string, and fails on the first real one. |
| The task status enum has **seven** values: `ENQUEUED`, `RUNNING`, `COMPLETE`, `FAILED`, `CANCEL_REQUESTED`, `CANCELLED`, `DEAD` | A cancelled task is read as `CANCEL_REQUESTED` for as long as it takes to stop, so a poller sees it. `jira.TaskState` is missing that one ([#59](https://github.com/varijkapil13/saral/issues/59)). |
| There is **no release-notes API**. `ReleaseNote.jspa` is a rendered web page | Out of scope by decision. |
| Releasing a version is one `PUT` with `released: true` — and it will happily release with open issues | Always check `unresolvedIssueCount` first and make the user decide. |
| `editJiraIssue`-style field writes cannot change an issue's project | Cross-project moves go through the bulk-move endpoint only. |
| Status is not a writable field | Only `POST /issue/{key}/transitions`, and the available transitions depend on current status. |
| A key present in `fields` with the value `null` **empties that field**; a key that is absent leaves it alone | Those are two different requests and a struct with a zero value cannot say which was meant. `jira.IssuePatch` is sparse for this reason, and a fetch-edit-PUT that cannot tell "not requested" from "empty" blanks every field the read never asked for. `Issue.Requested` is the answer to which of the two a caller is holding. |
| `POST /rest/api/3/issue` answers with `id`, `key` and `self` — **nothing else**, not even the status the workflow put the issue in | The issue as stored takes a second `GET`. That second call failing is not a reason to report the create as failed: the issue exists, and a caller's retry would make another one. |
| `GET /issue/{key}/transitions` sends an **empty `fields` object on every transition** unless the request asks for `expand=transitions.fields` | Without it a transition with a screen and a required field looks like a move that needs nothing, and the `POST` then fails with a 400 naming a field the client never saw. |
| A transition's `fields` is a JSON **object keyed by field id**, so it has no order, and each entry carries `fieldId`, `key` and `name` | Sort it into something stable — required first, then by id — or a form's rows move between two reads of one screen. Read the id from `fieldId`, falling back to `key` and then the map key. |
| Every transition carries `isAvailable`, which is `false` only when the request asked for unavailable ones too | Filter on it anyway. Offering a move the site has already said cannot be made spends a round trip to earn a 400. |
| `PUT /issue/{key}` takes `notifyUsers` as a **query parameter**; `POST /issue/{key}/transitions` has no notification control at all | The transition body is an `IssueUpdateDetails` with no room for it, so an intent to suppress mail cannot be carried through a transition. |
| `GET /issue/{key}` accepts `expand=schema`, which answers with one entry per field on the issue | Worth sending on a single-issue read: without it a custom field's value is typed by its shape alone, which cannot tell a number from a number-shaped string or a sprint array from an option array. |
| A select-like field is **written** as `{"id": "…"}`, and a labels-like array of bare strings is written as the strings | An id is the same on a site in any language and a `value` is not, so write the id wherever the value has one. An option with no id can only have come from an array of strings, which is the one shape that produces them. |
| The platform and Agile APIs write date-times differently: `2021-01-17T12:34:00.000+0000` (no colon in the offset) versus `2015-04-11T15:22:00.000+10:00` (colon) | Two layouts, `2006-01-02T15:04:05.000-0700` and `2006-01-02T15:04:05.000-07:00`. One decoder cannot serve both. `/task/{id}` timestamps are epoch millis instead. |
| The `/search/jql` response carries **three** keys in practice — `issues`, `nextPageToken`, `isLast` | There is no `total`. `names` and `schema` come back only if you send `expand=names,schema`, and each issue's own `expand` string lists them anyway, which reads as though they were expanded when they were not. Treat an absent `nextPageToken` as the end. |
| The `nextPageToken` is **not opaque**: base64url-decoding one yields the sort column and direction, the last row's sort value, the project key, a row count, and **the full JQL string** | Never cache a token across sessions, never reuse one with a different `jql`, `fields` or sort, and never log one — it carries the query. It is not a stable identifier for a result set. |
| `expand=schema` on `/search/jql` answers with one entry per **requested** field, keyed by field ID | That is enough to type a `customfield_NNNNN` in the same round trip, so a search does not need a second call to `/field` to decode its own answer. A query of nothing but system fields needs no schema and should not ask for one. |
| A field's declared `schema.type` is not always the shape of its value: a multi-line text field declares `string` and stores an **ADF document**, `any` (rank, epic link) declares nothing at all, and a sprint field is an `array` of `items: json` | Read the value itself for a document and for `any`. What the schema does name and a client has no slot for is worth keeping verbatim rather than guessing at — reading a sprint as its name silently drops its state and dates. |
| A user on a `/search/jql` response carries **no `avatarUrls`** | The same account has them elsewhere, so anything rendering an avatar fetches it separately. A list view must not depend on one being there. |
| `status.iconUrl` is the **site root URL**, not an icon | Fetching it gets the Jira homepage. Status glyphs come from `statusCategory.key`. `issuetype.iconUrl` and `priority.iconUrl` are real images; only status is degenerate. |
| A bare `GET /issue/{key}` returns **every field on the site**, with unset ones as explicit `null` | On a site with ninety custom fields that is ninety nulls per issue. This is the concrete reason a narrow `fields` list is not an optimisation but the normal way to call it. |
| `fields.statusCategory` arrives as a field in its own right, beside `fields.status` | Two places carry the same answer; `status.statusCategory` is the one to read. |
| `timetracking` is `{}` — an empty object, not `null` — on an issue with no estimates | Decoding into a struct gives zeroes, which is right; decoding into a pointer never gives nil. |
| `statusCategory.id` is a **number** while `status.id` is a **string** | The four categories are fixed on every site: 1 `undefined`, 2 `new`, 3 `done`, 4 `indeterminate`. Branch on `key`; `name` is localised. |
| `GET /rest/api/3/issue/createmeta?expand=…` is deprecated | Use the paginated pair, `GET /issue/createmeta/{projectIdOrKey}/issuetypes` then `…/issuetypes/{issueTypeId}`. |
| A `timeZone` on `/rest/api/3/myself` is not a promise this machine can load it | The name comes out of the site's own zone database and is resolved locally against Go's zoneinfo, which a slim container image may not carry at all. That failure is **local**: report it as this machine having no entry for the zone the account is set to, never as Jira failing to answer, and fall back to UTC. |

## Things that vary per instance — never hardcode

| Thing | Where to get it |
|---|---|
| Custom field IDs (story points, start date, target dates) | `GET /rest/api/3/field`, resolved **by `untranslatedName`, falling back to `name`** — see below |
| Which fields a board estimates in, and whether it ranks at all | `GET /rest/agile/1.0/board/{id}/configuration`, **feature-detected** — see below |
| Estimation field for a board | `GET /rest/agile/1.0/board/{id}/configuration` → `estimation.field.fieldId` (or `type: none`) |
| Rank field | same response → `ranking.rankCustomFieldId` |
| Column → status mapping | same response → `columnConfig.columns[].statuses` |
| Required fields for create | `GET /rest/api/3/issue/createmeta` per project + issue type |
| Available transitions | `GET /rest/api/3/issue/{key}/transitions` per issue |
| Whether attachments are enabled | `GET /rest/api/3/configuration` |
| Permissions | `GET /rest/api/3/mypermissions?permissions=…` |
| The user's timezone | `GET /rest/api/3/myself` |
| Which projects exist, and which this token can see | `GET /rest/api/3/project/search` — paginated, and the only endpoint that answers it. The port has no method for it, so the onboarding picker derives the keys from the projects on a `/search/jql` page instead, which is a shorter answer: it lists what the account has touched, not what it could reach. |

## Permissions, which is what the capability probe reads

| Fact | Consequence |
|---|---|
| In the **global context** `mypermissions` reports a project permission as held if the token holds it in *any* project | An unscoped probe answers a different question from the one being asked, and answers it optimistically: it reports Move as available to a token that cannot move an issue in the project on screen. Always send `projectKey`. |
| The `permissions` parameter is required, and an empty or unrecognised key is a **400** rather than a silently dropped one | Ask for a fixed, narrow list. Widening it speculatively is how a probe starts failing outright on a site that does not know the new key. |
| A key that *was* asked for can still be missing from the answer, and a **404 means either "no such project" or "you may not see it"** — the endpoint does not distinguish them | Absent, `havePermission: false`, and "the call failed" are three different answers. Word all three differently; a view keyed off the wrong one is either hidden for no reason or lying about why. |
| `BULK_CHANGE` is a `GLOBAL` permission while `MOVE_ISSUES`, `CREATE_ISSUES` and `DELETE_ISSUES` are `PROJECT` ones — each entry's `type` says which | A cross-project move needs the global one, Move in the source **and** Create in the target, so a probe scoped to one project can only half-answer it. The move flow has to re-check Create once a target is chosen. |
| Every entry carries a localised `name` and `description` | The `name` is what the site's own administrator sees, in the site's own language, so it is the right wording for a capability's reason — and, being translated, it is never something to match on. |
| `GET /rest/api/3/attachment/meta` answers the same question as `/configuration` with `enabled`, and adds `uploadLimit` | Narrower than reading the whole site configuration, and the only place the maximum attachment size comes from. |

## ADF

| Fact | Consequence |
|---|---|
| The published JSON schema (`go.atlassian.com/adf-json-schema`) and the prose docs disagree, and the prose is the stale one | Model from the schema. It has node types the prose omits — `taskList`, `decisionList`, `layoutSection`, `extension`, `blockCard`, `embedCard`, `placeholder` — all of which Jira stores. |
| Key order in the JSON is neither documented nor stable | A byte-stable round trip is only possible by keeping the original bytes. `pkg/adf` does exactly that, and re-encodes only the subtrees that changed. |
| Marks come back from the editor in ProseMirror rank order (`link, em, strong, strike, subsup, underline, code, textColor, backgroundColor, …`) | The REST API accepts any order, but anything a human opens in the browser comes back reordered. Mark order is byte-significant and semantically meaningless — never diff on it. |
| The validator *repairs* rather than rejects: unknown `attrs` keys are deleted and an `unsupportedNodeAttribute` mark is appended | Sending a document you rebuilt from a partial model is lossy even when it validates. Send back what you were given wherever you did not edit. |
| `text` on a text node has `minLength: 1` | An empty text node is invalid. Drop it; never emit `"text": ""`. |
| A node's permitted marks depend on where it sits — the schema encodes this with `_with_no_marks` / `_with_alignment` / `_root_only` variants | A paragraph at the root may carry `alignment`; the same paragraph inside a list item may carry none. |
| `mention` carries an account id, `status` carries a colour, `date` carries epoch millis, `media` carries a collection, and a `taskItem` carries a `localId` the editor generated | None of them has a markdown spelling, so ADF → markdown → ADF cannot rebuild any of them: a mention comes back as its display text and a lozenge as bracketed text. Editing a document means reconciling against the one you were given — `adf.ParseMarkdownInto` reuses the original node for every block the author did not touch, which is the only way the ids survive. Never invent a `localId`: a made-up one is a *new* item, not the one that was there. |
| `orderedList` numbers from **one** — `order` is a positive integer | A list stored with `order: 0` is displayed from 1, so reading a "0." back as a list starting at zero invents a document Jira does not have. |
| ADF has **no nested tables**, and no `table`, `heading` or `rule` inside a `blockquote` or a `listItem` | Markdown can ask for every one of those. Refuse with a typed error rather than sending something the validator will silently repair into a shape nobody wrote. |
| ADF's content model for a task or decision list is `(item | list)+` | Indenting an action item in the editor stores a sibling `taskList` *inside* the parent list, not inside the item above it. Treating that child as an item loses every checkbox under it. |

## Localisation, which breaks resolution by name

`name` is translated into the site's language — statuses, transitions, issue types, priorities,
resolutions and custom fields all arrive in German on a German site. Anything that matches on a
display name works on exactly one site.

| Fact | Consequence |
|---|---|
| `/rest/api/3/field` sends `untranslatedName` alongside `name`, on **custom fields only** | Resolve by `untranslatedName` first and fall back to `name`. `jira.FieldByName` does this. A system field has no `untranslatedName`, so an empty query must not match one. |
| `clauseNames` follows `untranslatedName`, never the localised `name` | JQL written from a display name does not parse. Prefer `cf[NNNNN]`, which is always in `clauseNames`. |
| `untranslatedName` appears on `/field` and **not** in createmeta | A form builder that only has a create screen cannot know it; join to the `/field` catalogue on `fieldId`. |
| Field names are not unique, and Jira signals it by adding a clause name `Name[Field Type]` | That bracketed form is the only unambiguous name; `cf[NNNNN]` is better. |
| Statuses and transitions are localised too, and their `id` values are not | Group columns by `statusCategory.key` and move issues by transition **id**. Never by name. |

## Boards differ from each other far more than the schema suggests

Everything below is per-board, and absence is a normal answer rather than an unset value.

| Fact | Consequence |
|---|---|
| A Kanban board sends **no `estimation` object at all** | `estimation.type: "none"` is a Scrum answer meaning "estimation is off". Absent means "this board does not estimate". `jira.BoardConfig.Estimation` is a pointer for exactly this. |
| A board ordered by priority — or by anything its filter sorts on — has **no rank field**: `ranking` is present but empty | Detect on `rankCustomFieldId`, never on the presence of `ranking`. Without one, the order comes from the board's saved filter and rows cannot be reordered. Reading that order takes a separate `GET /rest/api/3/filter/{id}`. |
| `subQuery` is Kanban-only; `estimation` is Scrum-only | Branch on board type before reading either. |
| Column `min` and `max` are independently optional | Keep both as pointers; a default of zero means something. |
| The Agile API's nested `self` links point at `/rest/api/2/` | Code that derives a URL by string-matching `/rest/api/3/`, or follows a `self` expecting a v3 body, is broken. |
| `location.projectId` is a number on `GET /board`; `location.id` is a string on `GET /board/{id}/configuration` | Same project, two types, one call apart. |

## Version and plan endpoints

| Fact | Consequence |
|---|---|
| `/project/{key}/versions` returns a **bare array**; `/project/{key}/version` returns a **paged envelope** | One letter apart, different top-level JSON type. Use the paged one — a project accumulates hundreds of versions. |
| `expand=issuesstatus` is per-request, not per-version | Either every version in the page has `issuesStatusForFixVersion` or none does. Its absence never means "no issues". |
| `issuesStatusForFixVersion` buckets by **status category**; `unresolvedIssueCount` counts by **resolution** | They disagree — an issue can be in the Done category with no resolution. Only the second is the pre-release gate. |
| `overdue` is emitted only when true, and the date fields are independently optional | Absent means false. Do not round-trip it. |
| `userStartDate` / `userReleaseDate` are locale display strings (`05/Jan/26`) | Render `startDate` / `releaseDate` yourself. |
| Archived versions are filtered out of createmeta allowed values but present in `/project/{key}/version` | A fix-version picker fed from the wrong endpoint offers archived versions. |
| Plans `id` is a **string** in the list response while the get-plan example shows a number | Decode leniently. `issueSources[].value` is a numeric project **id**, never a key. |

Group board columns by `statusCategory` (three fixed values: To Do, In Progress, Done), never by
status name — "In Progress" is not guaranteed to exist.

## Rate limiting

| Fact | Consequence |
|---|---|
| `Retry-After` is not always a number of seconds — HTTP permits an absolute date, and Atlassian also sends **`X-RateLimit-Reset`** as an RFC 3339 instant | A client that only does `strconv.Atoi` on `Retry-After` silently falls back to its own backoff on both, and waits the wrong amount. Read seconds, then an HTTP-date, then `X-RateLimit-Reset`, and clamp an instant already in the past to zero. |
| A 429 refuses the request **before it runs**; a 5xx may have got far enough to change something | So a 429 is safe to replay whatever the method, and a 5xx is only safe for a request that was repeatable to begin with. Getting that backwards duplicates issues. |
| `/search/jql` is a `POST` that only reads | Any blanket "never retry a POST" or "never coalesce a POST" rule has to make an exception for it, or search loses both retries and request coalescing on the hottest path in the application. |

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
