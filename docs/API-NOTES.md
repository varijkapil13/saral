# Jira API notes

Jira Cloud Platform REST API v3 and Agile API 1.0, as of August 2026. Every entry here cost time to
find out. Read it before writing an adapter method.

**Every row says how we know it.**

- `live` — seen in a response from a real Jira Cloud site. One site, read in August 2026, built to
  hold every shape this client claims to handle: a company-managed project and a team-managed one,
  five boards, sprints in all three states, versions released and unreleased and archived, custom
  fields of every type, a transition with a screen and a validator, and the site language flipped to
  German and back.
- `schema` — taken from Atlassian's published OpenAPI and JSON schemas. Nobody has watched a real
  site do it.
- `assumed` — weaker than both: the shape a fixture was written to, reasoned from the endpoint's
  documented behaviour and the shapes either side of it by somebody with no site to check against.
  An adapter may be built on one; a product decision may not. The first capture that touches the
  endpoint either promotes the row or deletes it.

A `live` row is enough to disprove a schema claim and not enough to prove a shape universal: one
site is one site, and two rows below record a site disagreeing with *itself*. When you verify a
`schema` row, change the word. Three defects in shipped code came from claims in this file that
nobody had checked, so the marker is the point, not decoration.

## Hard constraints

| Source | Fact | Consequence |
|---|---|---|
| live | `/rest/api/3/search` is **410 Gone** on GET and POST, and so is `/rest/api/2/search` | Use `POST /rest/api/3/search/jql`. Not optional. |
| live | v3 requires **ADF** for description, environment, comments, worklog comments and multi-line custom fields | Single-line fields take plain strings. `pkg/adf` is not optional. |
| live | `PUT /rest/agile/1.0/sprint/{id}` is a **full replace** — and it 400s first if `state` is missing | The 400 is not a guard. A `PUT` carrying `name` and `state` succeeds and silently drops the goal. Never expose it; use `POST /rest/agile/1.0/sprint/{id}` for partial updates. |
| live | Sprint state machine: `future → active → closed` only, start needs both dates, `completeDate` is never writable, a closed sprint takes only `name` and `goal` | Validate locally and return a real error instead of a 400. Two sprints can be active on one board at once, and a closed sprint can still hold unfinished issues — neither is a corrupt read. |
| live | A key present in `fields` with the value `null` **empties that field**; an absent key leaves it alone | Two different requests, and a struct with a zero value cannot say which was meant. `jira.IssuePatch` is sparse for this reason. A description never set reads back `null`; one cleared with `null` reads back as an **empty ADF document** — also two different states. |
| live | `POST /rest/api/3/issue` answers `id`, `key` and `self` and **nothing else**, not even the status the workflow put the issue in | The issue as stored takes a second `GET`. That second call failing is not a reason to report the create as failed: the issue exists, and a retry would make another one. `POST /issue/bulk` answers `{"issues":[…],"errors":[]}`. |
| live | `POST /rest/api/3/issueLink` answers **201 with an empty body** | The new link's id needs a re-read. A `comment` in that request body lands on the **inward** issue. |
| live | Status is not a writable field | Only `POST /issue/{key}/transitions`, and the available transitions depend on the current status. |
| live | `POST /rest/agile/1.0/backlog/issue` **and** `POST /rest/agile/1.0/sprint/{id}/issue` both cap at **50 issues**, and a refusal is atomic | Chunk, and report partial progress. Nothing moved on the call that was refused. |
| live | `PUT /rest/api/3/issue/{key}/comment/{id}` with **only** `body` does **not** clear a `visibility` the request omits | So an edit may send the body alone, and the read-before-edit this file used to prescribe buys nothing but a round trip. |
| live | Echoing a comment's `visibility` back **verbatim is a 400**: the body parameters `value` and `identifier` are mutually exclusive | A read hands you both keys; a write accepts either one beside `type`. For a role, `identifier` is the role *name* — an id there is a 400 — so neither key is the language-proof half, and there is nothing to gain by preferring one. Group visibility can be disabled site-wide, and is refused outright when it is, so a comment form cannot assume both kinds are on offer. |
| live | Attachment upload needs `X-Atlassian-Token: no-check` and a multipart part named exactly `file`. Success is **200**, and the body is an **array** | Without the header the answer is a **404 whose body is the plain string** `XSRF check failed`. With a wrongly named part it is an RFC 7807 400. Neither is the classic error envelope. |
| live | `GET /rest/api/3/attachment/content/{id}` 303-redirects to a signed media URL whose token expires in about ten minutes, and it redirects **even when the request carries `Range`** | The `206` comes from the media host after the redirect, not from Jira. `?redirect=false` gets a 200 from Jira directly. The redirect URL is a credential: never log it, never cache it, never hand it to another process. A client that follows the redirect itself has to strip its **own** Authorization header on the way, and Go keeps that header across a redirect it judges to stay on one host — on loopback every redirect does, so no test notices the leak. |
| schema | A ranged read is RFC 7233, and `bytes=N-` is **unsatisfiable at `N` equal to the length** as well as past it: the answer is a `416` carrying `Content-Range: bytes */N` | So the `416` that answers a caller resuming a file it already holds whole is a finished download, not a failure — the caller wrote every byte and the rename is what did not happen. Read the total out of `Content-Range` and compare: equal means done, greater means the offset really is past the end. `http.ServeContent` implements exactly this, which is what the fixture server answers with. No real site has been watched refusing a range. |
| schema | `DELETE /rest/api/3/attachment/{id}` answers **204 with no body**, takes the attachment's id alone and names no issue. Its 403 body is unrecorded — this row is the published schema plus what the fixture server was made to answer, and no real site has been asked | Classify on the status and read neither body. The permission is Delete own attachments or Delete all attachments, both **project** permissions, so a 403 here is not the site-wide switch `CapAttachments` stands for and must not be reported as "attachments are switched off". A delete also leaves any ADF `media` node that named the file pointing at an attachment the issue no longer has, which is the `ATTACHMENT_VALIDATION_ERROR` in the ADF section — deleting an attachment gives the caller a description to fix. |
| schema | An issue's attachments are a **field on the issue**, read with `GET /issue/{key}?fields=attachment`, and the id is a **string** there. There is no collection endpoint to list them: `/issue/{key}/attachments` takes the upload POST and nothing else | `Attachments` is an issue read asking for one field. An issue with no attachments, an attachment field not on that issue's screen and a site with attachments switched off are one answer between them — the key is absent — and absent is not an error. |
| live | `timetracking` is `{}` — an empty object, not `null` — on an issue with no estimates, and when populated it carries pretty strings beside `*Seconds` | Decode into a struct, not a pointer. Read `originalEstimateSeconds`; never parse `2d 4h`, whose day is the site's working day (see the site configuration row). |
| live | A bare `GET /issue/{key}` returned **91 fields, 39 of them explicit `null`** | A narrow `fields` list is not an optimisation, it is the normal way to call it. |
| live | One identifier, two JSON types, twice over: a transition is `id: "31"` with `to.id: "10036"` on `/issue/{key}/transitions` and `transitionId: 31` with `to.statusId: 10036` on the bulk-transition meta; an attachment id is a **string** in the upload response and a **number** in `GET /attachment/{id}` | Decode leniently at every boundary, and never compare an id read from one endpoint against one read from another without normalising first. |
| live | Releasing a version is one `PUT` with `released: true`, and it releases with open issues — one site held a released version with ten of them | Always check the unresolved count first and make the user decide. That count is not on any version read; see Versions and plans. |
| schema | `editJiraIssue`-style field writes cannot change an issue's project | Cross-project moves go through the bulk-move endpoint only. |
| schema | `POST /rest/api/3/bulk/issues/move` is async, caps at 1000 issues, and needs global **Bulk Change** plus Move in the source and Create in the target | Poll the returned task. |
| live | A bulk submit answers `{"taskId": "…"}` and nothing else — the schema is `additionalProperties: false`, so there is **no link to follow** | Progress is `GET /rest/api/3/bulk/queue/{taskId}`, which the client constructs. That is not the generic `GET /rest/api/3/task/{taskId}`, which answers a different shape. |
| live | The two progress bodies overlap on exactly three keys — `status`, `started`, `submittedBy` — and `submittedBy` has a different **type** in each: a numeric legacy user id on `/task/{id}`, an object with an `accountId` on the bulk queue | A struct written for one either errors on the other or reads two keys and leaves the rest zeroed. The numeric id is the last pre-GDPR id left in v3 and nothing turns it back into a user. |
| live | On the queue body, `failedAccessibleIssues` and `invalidOrInaccessibleIssueCount` are **absent when zero** | Absent is not a zero with a key on it. A decoder that requires them fails on a clean run. |
| live | A task's `description` is not reliable prose — it reported zero issues for a run of sixty — and its `message` is sometimes an unresolved i18n key | Report progress from the counts. Neither of those two strings is safe to show a user. |
| live | A task's `result` is an object for a bulk operation, and its clock — `submitted`, `started`, `lastUpdate`, `finished` — is **epoch millis** | Keep `result` as `json.RawMessage` and decode per operation. The Connect variant `TaskProgress` sends those same fields as RFC 3339 strings: two shapes, one name. |
| schema | The task status enum has **seven** values: `ENQUEUED`, `RUNNING`, `COMPLETE`, `FAILED`, `CANCEL_REQUESTED`, `CANCELLED`, `DEAD` | A cancelled task reads as `CANCEL_REQUESTED` for as long as it takes to stop, so a poller sees it. `jira.TaskState` carries all seven, and `Done()` deliberately reports `CANCEL_REQUESTED` as still running. |
| schema | `progress` on `/task/{id}` is a **percentage** and `elapsedRuntime` beside it is a duration in millis, while the queue calls the same percentage `progressPercent` | Two key names for one number, and neither of them is an instant — a poller that reads `progress` off the clock row above renders a timestamp as per cent complete. |
| schema | The move payload keys its mapping by `<project id or key>,<issue type id>` and optionally a third `,<parent id or key>`, and a **duplicate key is dropped without failing the request** | One target per submit, and a comma inside either half silently corrupts the key, so refuse one before sending rather than watching half a move happen. |
| schema | All four `infer…Defaults` flags are **required**, and each is the opposite of the mapping beside it: `targetStatus` only when `inferStatusDefaults` is false, `targetMandatoryFields` only when `inferFieldDefaults` is false, `targetClassification` only when `inferClassificationDefaults` is false | A payload carrying a flag and its mapping is contradicting itself. `targetStatus[].statuses` is keyed by the **target** status id with the source status ids as its values, which is the opposite direction from a from-to remap: group by the target before sending. |
| schema | `targetMandatoryFields[].fields` is **not** the edit endpoint's field map. Every value is wrapped as `{"retain": …, "type": "raw"\|"adf", "value": …}`, and a `raw` value is an **array of strings** however few it holds — `{"retain":false,"type":"raw","value":["5"]}` for a number, `["10064"]` for an option, `["<accountId>"]` for a user; `adf` carries the document object under `value` | The swagger leaves `fields` free-form ("Contains the value of mandatory fields"), so nothing catches an edit-shaped payload: `5`, `{"id":"10064"}` and a bare ADF document are all accepted by the schema and wrong on the wire. `retain` is the alternative to a value — true keeps the source issue's — so it is false wherever one is sent. One flat list cannot say which child of a cascading select belongs under which parent, so that field cannot be moved at all. |
| schema | The queue declares `created`, `started` and `updated` as `string (date-time)` and the same document's own examples send **epoch-milli integers** | The declared type is wrong, and a decoder built from it breaks on the first real body. Nothing above the port needs a task's clock, so the cheapest correct reading is not to read it at all. |
| schema | The queue's `failedAccessibleIssues` is keyed by numeric issue **id**, its values are open-ended reason text, and progress is kept for only **14 days** | `jira.TaskStatus.Failed` therefore carries ids and not keys: nothing on that body turns one into a key. The reasons are not drawn from a fixed list, so nothing may match on their wording, and a poll after a fortnight is a 404 rather than a lost move. |
| schema | Dynamic webhooks (`POST /rest/api/3/webhook`) are **Connect / OAuth 2.0 apps only** | No push channel for an API-token client. Poll, scoped and backing off. |
| schema | The Plans API requires **Administer Jira** on every endpoint, is experimental, and lives at `/rest/api/3/plans/plan` — the doubled segment is correct | Per-plan rights in the UI do not grant API access. `GET /rest/api/3/plans` does not exist. Fall back to locally defined plans. |
| schema | **403 is documented for two different causes**: a token without Administer Jira, and a site with no Premium edition of Jira | Only the body says which, so the sentence a view shows beside its local plans has to be the site's own. `Plans` carries `CapabilityError.Reason` through unchanged for that reason, and names `CapPlans` on it so the fallback is the one thing a caller can tell apart. An absent Plans API is a 404 and stays a `*NotFoundError`: `capsPlans` already words that one differently. |
| schema | There is **no release-notes API**. `ReleaseNote.jspa` is a rendered web page | Out of scope by decision. |
| schema | A `timeZone` on `/rest/api/3/myself` is not a promise this machine can load it | The name comes from the site's zone database and is resolved against Go's zoneinfo, which a slim container image may not carry. That failure is **local**: report it as this machine having no entry for the account's zone, never as Jira failing, and fall back to UTC. |

## Search and paging

There are three paging models and, inside the Agile one, three envelopes.

| Source | Fact | Consequence |
|---|---|---|
| live | `/search/jql` pages by opaque `nextPageToken` and reports **no `total`**. Its envelope holds exactly `issues`, `nextPageToken`, `isLast`, and the last page carries **no token key at all** | The UI must work without a count. `POST /rest/api/3/search/approximate-count` — no `/jql` segment — where a number is genuinely needed. Treat an absent token as the end, and guard against one that repeats. |
| live | `maxResults` is silently capped at **100** and the envelope never echoes it | Ask for a thousand, get a hundred and a token. The cap cannot be read from the response, so a walk can never distinguish truncation from a small result and must keep following tokens until one is absent. The Agile API is the opposite: asked for a thousand it echoed the thousand and returned every issue, so the truncation this file used to attribute to Agile belongs to `/search/jql`. |
| live | A page token is bound to the sort and the JQL but **not** to `fields`. Reusing one with a different `jql` is a 400; reusing it with a different field list is a **200 carrying the new field set** | A walk that changes its mask mid-flight produces pages of two different shapes, and `Issue.Requested` is then wrong for half of them. Settle the mask before the first page. |
| schema | The token is **not opaque**: base64url-decoding one yields the sort column and direction, the last row's sort value, the project key, a row count and the full JQL string | Never cache one across sessions, never log one — it carries the query — and never treat it as an identifier for a result set. |
| live | `/search/jql` returns almost nothing without `fields`, and `fields: ["key"]` returns issues with **no `fields` object at all** | Always send an explicit narrow list, and let a key-only caller read the key from the issue rather than from the field set. |
| live | Only *syntax* errors 400. An unknown field, a typo'd project key, a renamed status and an unknown account all answer **200 with zero issues** | An empty list is not evidence of an empty project. This is the most likely way this client lies to somebody. Word an empty result as "no rows for this query", never as "nothing here", and check a saved query's field ids against the catalogue before trusting its silence. |
| live | An unbounded query is refused: `400 "Unbounded JQL queries are not allowed here"` | Every query needs a restriction clause, so nothing can ask the site for everything — the onboarding project picker included. |
| live | Asking for a field by the wrong spelling returns nothing at all, with no error: the field id is `statusCategory`, and `statuscategory` is silently ignored | The response carries only the keys it actually returned and nothing says what the endpoint did with the list you sent. `jira.FieldMask` on `Issue.Requested` therefore records what was *asked for*, never what the site had, and "requested and absent" covers three cases at once: empty on the issue, spelled wrongly, and not on this site. Every consumer of the mask has to know that. |
| live | The Agile API has **three** offset envelopes. `{startAt, maxResults, total, isLast}` on `/board` and `/board/{id}/sprint`; **no `total`** on `/board/{id}/epic` and `/board/{id}/version`; **no `isLast`**, and the array named **`issues`** rather than `values`, on `/board/{id}/issue`, `/board/{id}/backlog` and `/sprint/{id}/issue` | A decoder that reads only `values` turns a board or backlog response into a zero-length page **and no error** — the view renders empty and nothing reports a failure. Read `issues` as a fallback the way the platform envelope already does, treat a missing `total` as unknown rather than zero, and end a walk on a short or empty page. |
| live | `startAt` past the end answers 200 with an empty array, and the `expand` key disappears with it | The empty page is the reliable end of an offset walk, whatever a `total` claimed. |
| live | The comment envelope is `{startAt, maxResults, total, comments}` on the *platform* API, with **no `isLast`**; worklogs are the same shape with `worklogs` and a default `maxResults` of **20** | Three array names on three platform endpoints, so neither shared envelope in `pkg/jira/cloud/paginate.go` reads them. The walk ends on the total, and `maxResults` is capped at 100 here too. |
| live | The order comments come back in is not documented, and `orderBy` accepts only `created` and `-created` | Send `orderBy=created` when oldest-first is what the caller was promised. |
| schema | A **third** paging style: `/rest/api/3/plans/plan` pages by `cursor`/`nextPageCursor` and *does* report a `total`. Its end-of-results flag is spelled **`last`** and its page size comes back as **`size`** — not `isLast` and `maxResults` (`PageWithCursorGetPlanResponseForPage`, re-read from the published swagger in August 2026) | It is neither of the other two, which is why `Plans` returns a plain slice. `plans_ok.json` was written to `isLast`/`maxResults`, a shape the schema does not have, so `pkg/jira/cloud/plan.go` reads the flag under both spellings — but the spelling changes an answer only on a page that says it is the last and *still* carries a cursor, and no committed fixture is one. **`plans_ok.json` wants correcting to `last`/`size`**; it is `pkg/jira/jiratest/**` and not the plans packet's to edit. A fourth shape — no paging at all — covers `/field`, attachment upload and the bare-array `/project/{key}/versions`. |

## Errors, and what a failure actually says

The status is the classification; the body is the explanation, and the body has **four** shapes.
Three of them can hand you text no user should ever see.

| Source | Fact | Consequence |
|---|---|---|
| live | The classic envelope is `{"errorMessages": […], "errors": {…}}` — loose sentences in the first, per-field messages in the second, in the order Jira wrote them | The first per-field message names the field a form should focus. Some endpoints send an empty array under `errors`, which is not a failure and carries nothing. |
| live | **RFC 7807 is a second envelope.** An unknown route answers `{"type":"about:blank","title":"Not Found","status":404,"detail":"No endpoint GET <path>."}`, a wrong method answers 405 with `detail: "Method 'POST' is not supported."`, and a *real* endpoint uses it too — a malformed multipart upload answers 400 with its only text in `detail` | A parser that reads only `errorMessages` degrades every routing-level failure to bare status text, and routing-level failures are the class most likely to be this client's own bug. Fall back to `detail`, then `title`. |
| live | **The Agile API puts its prose under `errors`, keyed by an internal request parameter, and sends `errorMessages: []`.** A 404 on a board configuration reads `{"errorMessages":[],"errors":{"rapidViewId":"The requested board cannot be viewed because it either does not exist or you do not have permission to view it."}}` | Joining `errorMessages` for the reason yields the empty string, so the user is told "Not Found" while the one sentence that separates *does not exist* from *you cannot see it* is routed into per-field errors — where a create or edit screen will render `rapidViewId` as though it were one of its own fields. On the Agile API, an `errors` key that is not a field on the screen is a message. |
| live | Text in a body is not always presentable. Three shapes seen: an empty `errorMessages`, a bare token (`INVALID_INPUT`, `ATTACHMENT_VALIDATION_ERROR`), and an unresolved i18n key (`bulk.operation.progress.percent.complete`) | Anything that prints Jira's own words needs a fallback for when the words are not words. A single upper-case token or a dotted key is an enum, not a sentence. |
| live | The XSRF guard answers **404 with a plain-text body** | A JSON decode fails on top of a status that reads as "no such issue". Classify on the status; never let a body that will not parse change the classification. |
| schema | **`/bulk/**` is a fourth envelope.** `BulkOperationErrorResponse` is `{"errors":[{"message":"…"}]}` — an **array of objects** where every other endpoint puts either sentences or a field-keyed object, and its `ErrorMessage` declares `message` and nothing else. It is the declared 400 and 401 of `/bulk/issues/move`, `/bulk/issues/fields` and `/bulk/queue/{taskId}`, and the declared **403** of `/bulk/issues/delete` and `/bulk/issues/transition` — move and fields document no 403 at all, although every one of them is gated on the global Bulk Change permission | `parseErrorList` reads it on **every** status, not only the two it is declared for: an entry names no field, so it can only ever be a reason, and routing one into `ValidationError.Fields` would hang a sentence on an input no form has. Bodies also carry `errorType`; it is an enum and is never shown. **No fixture holds this shape** — the bulk 403 tests still serve `plans_403.json`, which is the *platform* envelope, and the coverage for this one is an inline body in `pkg/jira/cloud/bulkmove_test.go`. A bulk fixture belongs in `pkg/jira/jiratest/fixtures`, which that packet does not own. |
| live | Error text is **localised**, in both envelopes, and a sprint validation message came back in German on an account whose locale read `en_US` | Show it; never match on it. There is no error string in either envelope that is safe to branch on. |
| live | `GET /issue/createmeta/{project}/issuetypes/{id}` for a type the project does not have answers **404 with an empty body** | There is nothing to report but the status, so the caller must supply the wording. Read the issue-type list half first and the failure becomes "this project has no such type" instead of a bare 404. |
| live | A 429 refuses the request **before it runs**; a 5xx may have got far enough to change something | A 429 is safe to replay whatever the method; a 5xx is only safe for a request that was repeatable to begin with. Getting that backwards duplicates issues. |
| live | `/search/jql` is a `POST` that only reads | Any blanket "never retry a POST" or "never coalesce a POST" rule needs an exception for it, or search loses both on the hottest path in the application. |

## Reads: issues, fields and schemas

| Source | Fact | Consequence |
|---|---|---|
| live | A field's declared `schema.type` is not always the shape of its value: a multi-line text field declares `string` and stores an **ADF document**, rank and epic link declare `any`, and a sprint field is an `array` of `items: json` | Read the value itself for a document and for `any`. What the schema does name and this client has no slot for is worth keeping verbatim rather than guessing at — reading a sprint as its name drops its state and its dates. |
| live | **`schema.items` is not always a type name.** Of 22 array fields in one catalogue, three carried a **localised display label** there — one field answered `items` with its own English display name under `en_US` and with the German translation of it under `de_DE` — and two more carried an unresolved i18n key, of the shape `admin.customfield.type.something.name` | A switch on `items` takes a different branch per language and falls through entirely for the i18n-key fields. Branch on `schema.type` and `schema.custom`, neither of which moved between locales, and let an unknown `items` mean "keep the raw bytes", never "not an array". |
| live | A `number` field comes back as a **JSON float** whatever was written: an estimate written as `5` reads `5.0` | Decode every number into a float64. An `int` field compiles, passes against a fixture somebody hand-wrote as `5`, and fails on the first real read. |
| live | A labels-type field comes back **sorted**, not in the order it was sent | Never diff a labels write against its read to decide whether it landed. |
| live | A user on a `/search/jql` response **does** carry `avatarUrls`, and `emailAddress`, `timeZone` and `accountType` besides | This file said the opposite. The advice survives the correction: the account's own privacy settings decide what is there, so a row that needs an avatar to lay itself out breaks on the account that hides one. |
| live | An account arrives in the same shape wherever it appears: an issue's `assignee` and `reporter`, a comment's `author` and `updateAuthor`, `/myself` and the people endpoints all carry `accountType` beside the id and the name | One decoder reads all of them, so `jira.User.Kind` is filled by every read rather than by the people endpoints alone. A user with no `accountType` on the wire is `AccountUnknown`: the answer was silent, not the account odd, and nothing may read a missing kind as "a person". |
| live | `status.iconUrl` is the **bare site root** for a status created through `POST /rest/api/3/statuses`, and a generic placeholder image for one a project template made. Both kinds live on one site | Every value of it is useless for rendering. Status glyphs come from `statusCategory.key`. `issuetype.iconUrl` and `priority.iconUrl` are real images. |
| live | `fields.statusCategory` arrives as a field in its own right, beside `fields.status` | Two places carry the same answer; `status.statusCategory` is the one to read. |
| live | `statusCategory.id` is a **number** while `status.id` is a **string**. Four categories on every site: 1 `undefined`, 2 `new`, 3 `done`, 4 `indeterminate` | Branch on `key`. All four `name`s are localised. |
| live | A select-like field is **written** as `{"id": "…"}`, and a labels-like array of bare strings as the strings | An id is the same on a site in any language and a `value` is not, so write the id wherever the value has one. An option with no id can only have come from an array of strings. |
| live | Cascading select has **three** shapes: nested under `children` (an array) in createmeta, under `child` (an object) on a stored value, and a **flat list with an `optionId` back-reference to the parent** from `GET /field/{id}/context/{ctx}/option` | One decoder reads one of them. The create-screen option type stays separate from the issue decoder's, and the third shape needs its own or a deliberate decision not to call that endpoint. |
| live | Setting `parent` on a company-managed issue also fills the epic-link custom field, which reads back as a **bare key string** beside the nested `parent` object | Two statements of one relationship, and a JQL search on either counts the same children. Write `parent`, read `parent`, and treat the mirror as a fallback for an issue that has only that. |
| live | The platform and Agile APIs write date-times differently, and there are **three** live layouts, not two: `2026-09-01T14:30:00.000+0200` on platform fields (no colon in the offset), a colon form in the Agile schema, and sprint dates **UTC-normalised with `Z`** whatever offset was sent | `2006-01-02T15:04:05.000-0700` and `2006-01-02T15:04:05.000-07:00` between them do not parse a sprint boundary; that one needs `time.RFC3339`. A task's clock is epoch millis instead. One decoder cannot serve all of them. |
| schema | The three layouts are a *read* problem; a sprint **write** takes one layout, the colon form `2006-01-02T15:04:05.000-07:00`, which is what Atlassian's own create-sprint example sends | The read fallback chain is not the answer for a write. A UTC instant therefore goes out as `+00:00` and not `Z`, and the millis are not optional: `time.RFC3339` drops them and the platform layout drops the colon. Either is a localised 400, and the row above says nothing may branch on one. |
| live | `expand=schema` on `/search/jql` answers one entry per **requested** field, keyed by field id, and `names`/`schema` come back only if asked for — while each issue's own `expand` string lists them either way | That is enough to type a custom field in the same round trip, so a search need not call `/field` to decode its own answer. A query of nothing but system fields should not ask. Do not read the `expand` string as evidence anything was expanded. |
| schema | `GET /issue/{key}` accepts `expand=schema`, one entry per field on the issue | Worth sending on a single-issue read: without it a custom field's value is typed by its shape alone, which cannot tell a number from a number-shaped string, or a sprint array from an option array. |
| schema | The **sprint field is declared `array` with `items: json`**, and neither the array nor an entry in it carries a date: an entry has an id, a name, a state and a board id | This client has no slot for `items: json`, so *our decoder* hands the value over in one of two shapes and which one is our own doing: a read that expanded `schema` leaves it as `jira.KindUnknown` carrying the JSON, and a read that sent no schema infers the same array as options, an id and a name. `needsSchema` in `pkg/jira/cloud/search.go` sends the expand as soon as a field list names one non-system field, so a timeline gets the first shape and a list view of system fields the second. Anything reading a sprint off an issue reads both, or it works in a list view and not in a timeline. The dates come from `GET /sprint/{id}` either way. |
| schema | **`duedate` is a date and not a date-time** — `2026-03-27`, no time and no offset — unlike `created`, `updated` and `resolutiondate` beside it | It is held as a `jira.Date`, so a due date cannot slip a day for anyone east of the server. The timeline cascade's rule 3 ends on it. |

## People, and the words a filter narrows by

Everything in this section was read from one Cloud site in August 2026. It is the half of the API a
filter picker lives on, and almost none of it is shaped like the rest.

| Source | Fact | Consequence |
|---|---|---|
| live | **`GET /rest/api/3/user/search` answers a bare JSON array.** No envelope, no `total`, no `isLast` — neither paginator in `pkg/jira/cloud/paginate.go` reads that shape | Decode it as a slice. It takes `startAt`/`maxResults` and a page past the end is `[]`, so a walk is possible; nothing here needs one, because a person is found by typing more rather than by paging. |
| live | `?query=` **absent** is a 400 (`"The username or property query parameter must be provided"`); `?query=` **present and empty** is a 200 listing every account | So the parameter is always sent, including from the state a picker opens in. There is no way to ask this endpoint for nothing. |
| live | **Matching is neither substring nor fuzzy.** A two-letter needle that appears inside the one human account's surname found nobody; the same account's two initials found it; and a needle that appears only inside the email address found it too | It is word-prefix, initials and email tokens, in some combination no read reveals. Nothing local reproduces it, type-ahead cannot narrow what it already holds — a longer needle can match what a shorter one did not — and the caller must rank what comes back rather than present Jira's order as its own. |
| live | **`GET /rest/api/3/user/assignable/search?project=X` with no query answers only real people**, dropping app accounts. The measured site had **11 accounts, 10 of them `accountType:"app"`** | It is the endpoint a picker should prefer wherever a project is in hand, and not only for assigning: it is the difference between a readable list and a page of robots. Without a project it is a 400 in the classic envelope; with an unknown one, a 404 in the classic envelope. |
| live | **`/user/assignable/multiProjectSearch` answers RFC 7807** (`detail: "Required parameter 'projectKeys' is not present."`) while its sibling one path up answers the classic envelope | Two error envelopes one path segment apart. `parseErrorBody` reads both; nothing may assume which a neighbouring route uses. |
| live | **`GET /rest/api/3/user/bulk` defaults to `maxResults: 10`** whatever it was asked for, and answers `{self, startAt, maxResults, total, isLast, values}` — the Agile offset envelope, on a platform endpoint | Eleven ids came back as ten and `isLast: false`. It has to be walked even for a short list. |
| live | **A bulk read answers JSON `null` inside `values`** for an id the site does not know, and `total` counts the ids **asked for** rather than the ones found | A decoder reading into a struct gets a zero value with an empty name, and a caller drawing a row per id puts a blank row on screen. Drop them — but count them first, or the offset walk ends a page early. |
| live | **Three of eleven account ids contain a colon**: a numeric prefix, a colon, then a UUID. `url.Values` escapes it and Jira answers the escaped form with the raw one — the same body carried the escaped spelling in `self` and the raw one in `accountId` | Never build the query string by hand, and never compare two spellings of one id without decoding first. Anything that splits an id on a separator, or drops one into a JQL clause or a cache key, has to survive it. |
| live | **One account answered to two different display names on two endpoints** — one name on `/user/search` and another on `/user/bulk`, same account id, same minute | A name is not an identity even within one site and one session. Join on `accountId`; a name hydrated later may differ from the one the picker showed. |
| live | `emailAddress` is `""` on `/user/search` for an app account and the key is **absent entirely** on `/user/bulk` | Absent and empty are one answer here, but only because nothing may depend on an email at all: an account's privacy settings decide whether it is there. |
| live | `accountType` is one of `atlassian`, `app`, `customer`. It is an enum and not display text, so unlike a status or a priority name it did not move between locales | `jira.AccountKind` labels with it; it must never filter. An app account is assigned work and reports issues exactly as a person does, so hiding one loses rows. |
| live | **`/user/picker` and the JQL autocomplete endpoints wrap the matched span in HTML** — `<b>`, `<strong>` — inside the field they call `displayName`, and spell a person as `Name - email` in it. `/user/picker` also carries a localised prose `header` (`"Showing 1 of 1 matching users"`) | Neither is data: rendering one puts markup on screen, and the markup lands mid-grapheme on a non-ASCII value. `/user/picker` 400s in RFC 7807 without a `query`. Use `/user/search`. |
| schema | A token without **Browse users and groups** (`USER_PICKER`, id 27, `type: GLOBAL`) cannot search users | The probe reads a 403 from `/user/search` as the refusal and shows the site's own sentence. No token without the permission was available to provoke it, so the 403 itself is unconfirmed. |
| live | **`mypermissions` does not know `BROWSE_USERS`.** It answered `400 {"errorMessages":[],"errors":{"BROWSE_USERS":"Unrecognized permission"}}` — the Agile-shaped refusal, on a platform endpoint | The key is `USER_PICKER`. And since one unrecognised key fails the whole request, a probe that folds a new key into an existing list puts every capability in that list behind whether the site knows it. `CapPeople` calls the endpoint instead. |
| live | **`GET /rest/api/3/project/{key}/statuses` answers a bare array of issue types, each with its own `statuses`**, and two types in one project can carry different ones | It is the only read that says which statuses a filter may offer. Each status carries `untranslatedName` and a nested `statusCategory`; a bad project key is a 404 in the classic envelope. |
| live | `GET /rest/api/3/priority` is a **bare array**; `GET /rest/api/3/priority/search` is the paged envelope and adds `isDefault` | The bare one is deprecated. The order is the ranking order and is not alphabetical. On the measured site `isDefault` was `false` on all five, so nothing may assume one is flagged. |
| live | **`GET /rest/api/3/label` ignores a `query`.** A narrowed request answered byte-identically to the unnarrowed one | So there is no server-side label search. Walk it — `{startAt, maxResults, total, isLast, values}` with `values` an array of **bare strings**, `maxResults` defaulting to 1000 — and filter locally. |
| live | Labels are whatever anybody typed, and the measured site held a German one and a Japanese one among seventeen | A width taken with `len()` over one is wrong. `ansi.StringWidth`, as everywhere. The list came back sorted, and `startAt` past the end answers `values: []` with `isLast: true`. |

## Create screens and transitions

| Source | Fact | Consequence |
|---|---|---|
| live | `GET /issue/{key}/transitions` omits `fields` **entirely** without `expand=transitions.fields`; with the expand it is `{}` on a screenless transition and populated on a screened one | This file had it backwards. Either way, always ask for the expand — without it a transition with a screen and a required field looks like a move that needs nothing, and the `POST` then fails with a 400 naming a field the client never saw. |
| live | **A workflow validator's required fields are reported `required: false`.** A screened transition whose validator named two fields listed all four screen fields, with `required: true` on only one of them — and the field configuration did not require that one either | No read tells you what a validator will demand. The `POST` fails 400 with the message in **`errorMessages`** and `errors` **empty**, so a form rendering per-field errors from `errors` shows nothing at all. A transition form has to survive being told after the fact, in an administrator's own prose and language, that a field it drew as optional was required. Keep the user's input and re-present it with the message attached to the whole form. |
| live | A transition's `fields` is a JSON **object keyed by field id**, so it has no order, and each entry carries `fieldId`, `key` and `name` | Sort it into something stable — required first, then by id — or a form's rows move between two reads of one screen. Read the id from `fieldId`, falling back to `key` and then the map key. |
| live | `GET /rest/api/3/issue/createmeta?expand=…` is deprecated but still answers 200 | Still deprecated. Use the paginated pair, `GET /issue/createmeta/{projectIdOrKey}/issuetypes` then `…/issuetypes/{issueTypeId}`. |
| live | Both halves of the pair page by `startAt`/`maxResults`/`total` and name their arrays **`issueTypes`** and **`fields`** rather than `values` | Neither shared envelope decodes them; `pkg/jira/cloud/meta.go` has its own. Send `maxResults` rather than inheriting the site's default. |
| live | The fields half states **no project and no issue type of its own** | The only statement of either is inside the `project` and `issuetype` fields' `allowedValues`, and the project's `key` lives only there. Read the issue type from the list half instead. |
| live | **The createmeta field list depends on the caller's permissions.** The same project and issue type answered 33 fields and then 34 — gaining `reporter` — because the account picked up a project role in between | A cached create schema is per token, not per project. Key the cache on both and drop it when the session's account changes. |
| live | createmeta **omits disabled options** | Right for a picker, wrong for a renderer: an issue can still hold a disabled option, so a detail view must render an option that no create screen offers. |
| live | `operations` is stated **per field per issue type**, and an empty array means the field is on the screen to be read | It is the only statement of whether a field can be set at all. Measured: a link field offers `add` and `copy` and no `set`; `issuetype` normally sends `[]`. A form that offers every field on the screen offers fields Jira will refuse. |
| live | A field configuration cannot be created through the v3 API on a current Cloud site — `POST /fieldconfiguration` and `POST /fieldconfigurationscheme` are both refused site-wide, and the endpoint their message points at does not exist | The reads work, and `PUT /fieldconfiguration/{id}/fields` on the shared default configuration is reachable and site-wide. So there is no project-scoped way for a client to make a field required, and no point offering one. |
| schema | createmeta sends `hasDefaultValue` **and** the `defaultValue` itself, and `jira.FieldMeta` carries only the boolean | A form can say Jira will fill a field in but not what with. Widening the port is [#68](https://github.com/varijkapil13/saral/issues/68). |
| schema | Every transition carries `isAvailable`, which is `false` only when the request asked for unavailable ones too | Filter on it anyway. Offering a move the site has already refused spends a round trip to earn a 400. |
| schema | `PUT /issue/{key}` takes `notifyUsers` as a **query parameter**; `POST /issue/{key}/transitions` has no notification control at all | An intent to suppress mail cannot be carried through a transition. |

## Things that vary per instance — never hardcode

| Source | Thing | Where to get it |
|---|---|---|
| live | Custom field ids | `GET /rest/api/3/field`, resolved by `untranslatedName` and then `name` — with the caveats in Localisation, which are worse than they look |
| live | Which field a board estimates in, and whether it ranks at all | `GET /rest/agile/1.0/board/{id}/configuration`, **feature-detected** — see Boards |
| live | Column to status mapping | same response, `columnConfig.columns[].statuses`, which is not the whole story — see Boards |
| live | What a board holds, and what is in its backlog | `GET /rest/agile/1.0/board/{id}/issue` and `…/backlog`. Neither set can be composed from a board's configuration: the filter behind a board is arbitrary JQL, of which the configuration reports only `filter.id`, and reading it needs `GET /rest/api/3/filter/{id}`. `columnConfig` names status ids and says nothing about which issues are in them. `subQuery` is the one part these endpoints leave to the caller — see Boards |
| live | Required fields for create | `GET /issue/createmeta/{project}/issuetypes/{id}`, per project, per issue type **and per token** |
| live | Available transitions | `GET /issue/{key}/transitions?expand=transitions.fields` per issue, and even then not the validator's demands |
| live | Whether attachments are enabled, and how large one may be | `GET /rest/api/3/attachment/meta` answers both (`enabled`, `uploadLimit`). The size is nowhere in `/configuration` |
| live | The site's feature switches and its working day | `GET /rest/api/3/configuration`: voting, watching, unassigned issues, sub-tasks, issue linking, time tracking and attachments, plus a `timeTrackingConfiguration` carrying working hours per day and days per week as **floats**. A "3d" estimate is three of *those* days, not seventy-two hours, so any conversion of `originalEstimateSeconds` to days reads them first |
| live | Permissions | `GET /rest/api/3/mypermissions?permissions=…&projectKey=…` |
| schema | The user's timezone | `GET /rest/api/3/myself` |
| live | Who a filter may name | `GET /rest/api/3/user/search`, or `…/user/assignable/search?project=` where a project is in hand; ids resolve back through `GET /rest/api/3/user/bulk`. Never a display name — see People |
| live | Which statuses a project's issue types can reach | `GET /rest/api/3/project/{key}/statuses`, per issue type, by **id** |
| live | The site's priorities and its labels | `GET /rest/api/3/priority/search` and `GET /rest/api/3/label`. Both are site-wide; the label endpoint cannot be narrowed |
| live | Which projects exist, and which this token can see | `GET /rest/api/3/project/search` — paginated, and the only endpoint that answers it. The port has no method for it, so onboarding derives keys from a `/search/jql` page instead, which answers a shorter question: what the account has touched, not what it could reach. And that page cannot be unbounded, so it cannot even ask for everything |

## Permissions, which is what the capability probe reads

| Source | Fact | Consequence |
|---|---|---|
| live | The `permissions` parameter is required, and an unrecognised key fails the **whole request** with a 400 naming it | Ask for a fixed, narrow list. Widening it speculatively is how a probe starts failing outright on a site that does not know the new key. |
| live | `BULK_CHANGE` is a `GLOBAL` permission while `MOVE_ISSUES`, `CREATE_ISSUES` and `DELETE_ISSUES` are `PROJECT` ones — each entry's `type` says which | A cross-project move needs the global one, Move in the source **and** Create in the target, so a probe scoped to one project can only half-answer it. The move flow re-checks Create once a target is chosen. |
| live | Every entry carries a localised `name` and `description` | The `name` is what the site's own administrator sees, in the site's own language, so it is the right wording for a capability's reason — and, being translated, is never something to match on. |
| schema | In the **global context** `mypermissions` reports a project permission as held if the token holds it in *any* project | An unscoped probe answers a different question, optimistically: it reports Move as available to a token that cannot move an issue in the project on screen. Always send `projectKey`. |
| schema | A key that *was* asked for can still be missing from the answer, and a **404 means either "no such project" or "you may not see it"** | Absent, `havePermission: false`, and "the call failed" are three different answers. Word all three differently; a view keyed off the wrong one is either hidden for no reason or lying about why. |

## ADF

The round trip is settled. A document holding thirty node types and thirteen marks — every panel
kind, nested lists, tables with spans and column widths, layout columns, the three card types, task
and decision lists, mentions, dates, status lozenges, external media, and non-ASCII text in several
scripts — was sent, stored and read back. Jira changed **three things**: it upper-cased a hex colour
in a custom panel's `panelColor`, and it dropped two empty `attrs: {}` objects on table nodes.

So a fetch-edit-send round trip is stable, because `pkg/adf` re-emits every untouched node from the
bytes it was given. The two normalisations only bite content this client authors itself — which is
also why "what I sent came back different" is never evidence that a write failed.

| Source | Fact | Consequence |
|---|---|---|
| live | **The validator rejects; it does not repair.** An unknown node type, an unknown `attrs` key, a nested table, a heading inside a blockquote and a rule inside a list item each answered `400 {"errorMessages":["INVALID_INPUT"],"errors":{}}` | This file said the opposite, and the opposite was the kinder half. The refusal names no node, no path and no attribute, so a client that posts an unvalidated document can only tell the user "Jira refused the document". Validate locally and refuse with a typed error that can say *where*. |
| live | An empty text node is not rejected — it is **deleted**, and its paragraph comes back with no content at all | The schema's `minLength: 1` on `text` is enforced by silent removal. Never emit `"text": ""`; drop the node yourself so what you hold matches what Jira stored. |
| live | Key order is neither documented nor stable: a document came back with `marks` after `content` where it went out before | A byte-stable round trip is only possible by keeping the original bytes, which is what `pkg/adf` does, re-encoding only the subtrees that changed. |
| live | A `media` node naming an attachment the issue does not have answers 400 `ATTACHMENT_VALIDATION_ERROR` | Media is validated against the issue, so a document cannot be moved between issues by copying its bytes. |
| live | An empty document (`content: []`) round-trips exactly, and a `placeholder` node survives untouched | So an emptied description is a real, storable state, distinct from a description that was never set. |
| schema | The published JSON schema (`go.atlassian.com/adf-json-schema`) and the prose docs disagree, and the prose is the stale one | Model from the schema. It has node types the prose omits — `taskList`, `decisionList`, `layoutSection`, `extension`, `blockCard`, `embedCard`, `placeholder` — all of which Jira stores. |
| schema | Marks come back from the editor in ProseMirror rank order (`link, em, strong, strike, subsup, underline, code, textColor, backgroundColor, …`) | The REST API accepts any order, but anything a human opens in the browser comes back reordered. Mark order is byte-significant and semantically meaningless — never diff on it. |
| schema | A node's permitted marks depend on where it sits; the schema encodes this with `_with_no_marks` / `_with_alignment` / `_root_only` variants | A paragraph at the root may carry `alignment`; the same paragraph inside a list item may carry none. |
| schema | `mention` carries an account id, `status` a colour, `date` epoch millis, `media` a collection, and a `taskItem` a `localId` the editor generated | None has a markdown spelling, so ADF to markdown and back cannot rebuild any of them. Editing means reconciling against the document you were given: `adf.ParseMarkdownInto` reuses the original node for every block the author did not touch, which is the only way those ids survive. Never invent a `localId` — a made-up one is a *new* item. |
| schema | `orderedList` numbers from **one**; `order` is a positive integer | A list stored with `order: 0` displays from 1, so reading a "0." back invents a document Jira does not have. |
| schema | ADF has **no nested tables**, and no `table`, `heading` or `rule` inside a `blockquote` or a `listItem` | Markdown can ask for every one of those, and the API now confirms it refuses them. |
| schema | The content model for a task or decision list is one-or-more of item-or-list | Indenting an action item in the editor stores a sibling `taskList` *inside* the parent list, not inside the item above it. Treating that child as an item loses every checkbox under it. |

## Localisation, which breaks resolution by name

`name` is a display string and nothing else. On one site flipped from `en_US` to `de_DE` and back,
**59 of 101 field names changed**, while every field `id`, every `untranslatedName` and every
`clauseNames` entry stayed byte-identical. That is the rule this file repeats most often, and it now
has numbers behind it: match on an id, never on a name.

What does *not* translate matters as much, because the previous version of this note was wrong about
half of it.

| Source | Thing | Under `de_DE` |
|---|---|---|
| live | Field `name` | 59 of 101 changed |
| live | Status `name` | 9 of 15 changed — the six that did not are the ones an administrator had named |
| live | Status category `name` | all 4 changed; `key` and `id` did not |
| live | Priority `name` | **0 of 5** |
| live | Resolution `name` | **0 of 4** |
| live | Issue type `name` | **1 of 10**, and only its capitalisation |
| live | Board column `name`, and a board's `estimation.field.displayName` | changed; the `fieldId` beside it did not |
| live | Permission `name` and `description`, and error text in both envelopes | changed |
| live | `untranslatedName`, `clauseNames`, `schema.type`, `schema.custom`, every id | did not move |

Priorities and resolutions are per-site rows an administrator can rename, not translation keys, so
their English spelling on a German site is a coincidence and not a guarantee. Translation is
**partial** everywhere: a site is a mixture of Atlassian's strings, which move, and its own, which
do not, and no read tells you which kind you are holding.

| Source | Fact | Consequence |
|---|---|---|
| live | **`untranslatedName` is a third spelling, not the `en_US` display name.** Two of the 57 custom fields in one catalogue differed from their own English `name`: one dropped a space the display name had, one differed in case | So a resolver that tries an exact case-insensitive `untranslatedName` and then an exact `name` fails **both** passes on a localised site — the first on the punctuation, the second on the translation. `jira.FieldByName` is that resolver, and a `[profiles.x.timeline]` field name resolving through it is not portable to another site or another language. Compare on a folded, space-stripped form, and prefer configuration that holds an id resolved once. |
| live | `untranslatedName` is sent for **custom fields only** — 57 of 101 here, and not one system field | An empty query must not match anything, and a system field can only be found by its id or by its localised `name`. |
| live | `clauseNames` follows `untranslatedName`, never the localised `name` — and is **empty on 9 of 101 fields**, eight system and one custom | So `cf[NNNNN]` is *not* always available. A field with no clause names cannot be named in JQL at all, by any spelling, and a query built from one silently matches nothing. Check the list before building a clause, and have somewhere for "this field is not searchable" to go. |
| live | Names are not unique in either language. One site held **four separate pairs** of distinct status ids sharing a display name, because a team-managed project mints project-scoped statuses that reuse the stock names — and every pair collided again under translation. Issue types collide the same way, for the same reason | A name-keyed lookup silently picks whichever row came first, and the two are usually in different projects. Group columns by `statusCategory.key`, move issues by transition **id**, and never build a status or type index keyed by name. |
| live | Jira flags an ambiguous field with a bracketed clause name `Name[Field Type]`, and a field `name` may itself contain brackets | So the bracketed form is not reliably parseable either. Use `cf[NNNNN]` where it exists and the id otherwise. |
| live | `PUT /rest/api/3/mypreferences/locale` is **eventually consistent** — minutes, not seconds — and `Accept-Language` on a request is **ignored outright** | There is no per-request language, so nothing can ask for a stable one, and a read straight after the write still answers in the old language. |
| live | The language of `name` was seen **changing mid-session** on an account whose `locale` read `en_US`, with `/rest/api/3/status` answering English and `/issue/{key}/transitions` answering German **at the same instant** | Language is a property of neither the request, nor the account, nor reliably the endpoint. This single observation is the whole argument for the rule, and the reason a display name must never be cached as though it identified anything. |
| live | Every person picker on a create screen carries a site-absolute `autoCompleteUrl` — except a group picker, which carries none, and a labels field, whose URL is on a **different API version** | `internal/ui` takes the port and never an adapter, so a view holding one cannot call it anyway, and it never needs to: `FindPeople` and `Labels` reach the same answers in domain terms. The URL stays unread. |
| schema | `untranslatedName` appears on `/field` and **not** in createmeta | A form builder holding only a create screen cannot know it; join to the `/field` catalogue on the field id. |

## Boards differ from each other far more than the schema suggests

Everything here is per-board, and absence is a normal answer rather than an unset value.

| Source | Fact | Consequence |
|---|---|---|
| live | **`GET /board/{id}/issue` does not apply the board's own `subQuery`.** A board whose sub-query hid resolved issues on released versions showed 16 rows in the browser while the endpoint returned 18 | A client that ignores `subQuery` draws issues the real board hides, and no parameter makes the endpoint apply it. Read it from the configuration and apply it yourself, or send it as an extra clause. |
| live | A Kanban board sends **no `estimation` object at all**, and a board ordered by priority sends **`ranking: {}`** | `estimation.type: "none"` is a Scrum board saying estimation is off; absent means the board does not estimate. `jira.BoardConfig.Estimation` is a pointer for that, and rank is detected on `rankCustomFieldId`, never on the presence of `ranking`. Without a rank field the order comes from the saved filter and rows cannot be reordered; reading that order takes a separate `GET /rest/api/3/filter/{id}`. |
| live | A board's estimation field may be a **system** field — an original-estimate field with no `customfield_` prefix | Nothing may assume the estimation field id starts with `customfield_`. |
| schema | **`ranking.rankCustomFieldId` is a bare number and not a field id**, while the estimation field in the same response is named in full: `estimation.field.fieldId: "customfield_10032"` beside `ranking: {"rankCustomFieldId": 10019}` | The rank field is asked for as `customfield_<number>` — the spelling the `/field` catalogue, an issue's `fields` object and JQL all use, and the one `jira.BoardConfig.RankFieldID` has to hold. Storing the number instead reads nothing back off an issue and orders nothing, and it fails silently: the rows simply arrive in the filter's order, which is what a board with no rank field looks like. Two ids, two spellings, one response. |
| live | **A board's estimation field need not be on the project's create screen.** A board estimating in a story-points field faced a create screen of fifteen fields that did not include it | A create form cannot offer the estimate, so either new issues arrive unestimated or the estimate is a follow-up edit. Say which; do not draw a field createmeta did not offer. |
| live | A board created from a saved filter carries **no `location` key at all** | `jira.Board.ProjectKey` is unresolvable for such a board. Never key a board by project, and never render a project column as though every board had one. |
| live | Column names are **localised**, two columns on one board may share a name, a column may map **no statuses**, and one column may span **two status categories** | A column's identity is its position, never its name, and a column cannot be given a category from its statuses. |
| live | **A live status can be mapped to no column at all.** Assigning a workflow the board's column config never learned about left 21 issues in two statuses that appear in no column, and `columnConfig` gives no hint they exist. `columns[].statuses[]` carries only `id` and `self` — no name, no category | Grouping strictly by the column mapping drops rows silently. Count what fell outside and show the count; resolve a status id to a category through the catalogue, not the column. |
| live | `subQuery` is Kanban-only; `estimation` is Scrum-only; `columnConfig.constraintType` varies per board | **Do not branch on board type to decide what to read.** A team-managed board reports a third type, `simple`, so gating `subQuery` on `kanban`, or `estimation` and `ranking` on `scrum`, silently drops its whole answer. Read each object wherever it arrives; an absence is the answer. Column `min` and `max` mean nothing under a constraint type that does not count, and are independently optional — keep both as pointers, because a default of zero means something. `jira.BoardConfig` carries no constraint type yet, so **nothing may draw `min`/`max` as a WIP limit**: a board with `constraintType: "none"` stores both numbers and enforces neither. |
| schema | A board's own **Done** column is the **last column with any status mapped to it**, not the column whose statuses are in the Done category | The board marks an issue in that column as already completed, so a completed marker read from `statusCategory` disagrees with the board the user sees on any board whose last mapped column is not the Done-category one. Take done from the column's position and use the category for colour only. |
| schema | `projectKeyOrId` on `GET /board` is a **filter, not a scope** — relevance means the board's saved filter references the project — and the documented answers are 200, 400, 401 and 403, with **no 404** | An unknown or renamed key is an ordinary empty page rather than a refusal, so nothing may read `*jira.NotFoundError` as "no such project" here; and a call with no key at all lists every board on the site, which is why `Boards` requires one. `jiratest.Fake.Boards` answers `*jira.NotFoundError` for a key it does not hold: a divergence no capture has settled, and the reason the conformance table asserts nothing about an unknown key. |
| live | `/board/{id}/epic` reports an epic's `name` as `""`, in both project styles: the epic-name field is absent from the issue and not on its create screen, and the entry carries two separate colour keys instead | An epic's label comes from its `summary`. A board view keyed on the epic name draws a column of empty strings. |
| live | The Agile API's nested `self` links point at `/rest/api/2/` | Code that derives a URL by string-matching `/rest/api/3/`, or follows a `self` expecting a v3 body, is broken. |
| schema | `location.projectId` is a number on `GET /board`; `location.id` is a string on `GET /board/{id}/configuration` | Same project, two types, one call apart. |

## Company-managed and team-managed are two data models behind one API

| Source | Aspect | Company-managed | Team-managed |
|---|---|---|---|
| live | `style` / `simplified` on the project | `classic` / `false` | `next-gen` / `true` |
| live | Issue types | from a site-wide scheme, `scope` absent | project-scoped, `scope.type: "PROJECT"` |
| live | The sub-task type's name | **`Sub-task`** | **`Subtask`** |
| live | Statuses | shared site-wide | minted per project, reusing the stock names, so ids collide by name |
| live | Board `type` | `scrum` or `kanban` | `simple` |
| live | The story-point field | the classic template's own float field | a different field of a different custom type. One site held both, and only a board's configuration says which one that board means |
| live | Parent | writing `parent` also fills the epic-link custom field | `parent` alone |
| live | Sub-tasks | parented | seen with **no parent at all** |

Read the shape per project — `style` is the flag — and let nothing assume a name, a type id or a
field id is shared between two projects on one site. A sub-task with no parent, an epic with no name
and two story-point fields on one site are all normal answers this client has to render.

## Versions and plans

| Source | Fact | Consequence |
|---|---|---|
| live | **No version read reports the unresolved count** — not `GET /version/{id}`, not with `expand=issuesstatus`, not the paged project endpoint. It lives on `GET /rest/api/3/version/{id}/unresolvedIssueCount`, and the key there is spelled `issuesUnresolvedCount` | `jira.Version.Unresolved` can only be filled by that extra call, one per version. So the pre-release gate costs a round trip, and a version list cannot show the number for free — which is fine, because the number is only needed at the moment of releasing. |
| live | `overdue` is emitted with an explicit **`false`** on an unreleased version and is **absent on a released one** | Read `released` for whether it shipped. `overdue` is documented only as *is this version overdue*, so its presence is not a second way of saying unreleased: a version trimmed into an issue read or a createmeta answer omits the key whatever its state. Do not round-trip it. |
| live | `expand=issuesstatus` is per-request, not per-version | Either every version in the page has `issuesStatusForFixVersion` or none does. Its absence never means "no issues". |
| live | `issuesStatusForFixVersion` buckets by **status category**; the unresolved count counts by **resolution** | They disagree — an issue can be in the Done category with no resolution. Only the count is the gate. |
| live | Archived versions are filtered out of createmeta allowed values but present in the paged version list | A fix-version picker fed from the version list offers archived versions, so filter the list rather than swapping the source: an issue can already carry an archived version, and a detail view has to render it whatever a create screen would offer. |
| schema | `/project/{key}/versions` returns a **bare array**; `/project/{key}/version` returns a **paged envelope** | One letter apart, different top-level JSON type. Use the paged one — a project accumulates hundreds of versions. |
| schema | `Version.projectId` is a JSON **number** (`integer(int64)`), while `jira.Version.ProjectID` is a Go **string** | Convert at the boundary — a `json.Number` or an int64 intermediate. Decoding a version straight into the port type fails with *cannot unmarshal number into Go struct field … of type string*. Both version fixtures send the number a site sends; do not "fix" them. Same trap as `location.projectId` on a board, above. |
| schema | `userStartDate` / `userReleaseDate` are locale display strings | Render `startDate` / `releaseDate` yourself. |
| schema | Plans `id` is a **string** in the list response while the get-plan example shows a number | Decode leniently. `issueSources[].value` is a numeric project **id**, never a key. |
| schema | `POST /rest/api/3/version` requires **`projectId`**, an integer, and the `project` key it also takes is deprecated. `released` is "not applicable" on a create, `projectId` "not applicable" on an update | A create resolves the project key to its id first — one extra request, and the alternative is a field Atlassian has already deprecated. An update sends neither key. |
| schema | `PUT /rest/api/3/version/{id}` answers **400** both for an invalid request *and* when the token lacks Administer Projects | A release refused for permissions arrives as a `*jira.ValidationError` with no field on it rather than a `*jira.CapabilityError`, so a release screen that only draws field errors says nothing at all. Draw `Messages` too. |
| schema | `PUT /rest/api/3/version/{id}` takes **`moveUnfixedIssuesTo`** — the *self URL* of another version — and moves the unfixed issues itself as the version is released. Nothing does the same for stripping the version off them | Nobody has watched it work, and a key a site ignores would release the version over exactly the issues the user asked to move. So `ReleaseVersion` sweeps the issues itself and flips `released` last, and the failure direction is an unreleased version rather than a released one full of open work. Verifying this live would replace N writes with one. |
| schema | **No endpoint lists the issues on a version.** `unresolvedIssueCount` and `relatedIssueCounts` are counts | The pre-release sweep finds them with JQL: `fixVersion = <id> AND resolution IS EMPTY`, the id unquoted — a quoted value is matched against the version *name*, which is neither unique nor stable. There is no id form for anything but a bare number, so `ReleaseVersion` refuses a version id that is not one rather than sweeping by name. Resolution, not status category, because that is what the gate counts. |
| assumed | `unresolvedIssueCount` is the project's own count; the sweep's JQL is answered by the **index** and narrowed by what the token may see. So the two can disagree in number even though both count by resolution — issue-level security, or an index that has not caught up | A release that swept fewer issues than the count named is refused with the version left unreleased, because the alternative is a released version with issues nobody dealt with. A caller retries once the two agree. Nobody has watched this happen; the arithmetic is what forces the choice. |
| schema | **No endpoint lists the issues on a version.** `unresolvedIssueCount` and `relatedIssueCounts` are counts | The pre-release sweep finds them with JQL: `fixVersion = <id> AND resolution IS EMPTY`, the id unquoted — a quoted value is matched against the version *name*, which is neither unique nor stable. Resolution, not status category, because that is what the gate counts. |
| schema | An issue's fix versions can be written only as the **whole array** (`fields.fixVersions`) or through the edit endpoint's `update.fixVersions` **add**/**remove** verbs. `jira.IssuePatch` reaches the first shape and not the second | So nothing above the port can take one version off an issue without sending back every other version it carries, and the fake cannot even be asked: `jiratest.Fake.UpdateIssue` files a `fixVersions` value into the generic field bag rather than into `Issue.FixVersions`, and its JQL understands no `fixVersion` field, so a query naming one is refused as invalid. Bulk fix-version assignment is therefore `ReleaseVersion`'s `MoveUnresolved` and `StripUnresolved`, where `pkg/jira/cloud/version.go` does it with the add/remove verbs one issue at a time. Reassigning a fix version **without** releasing needs a port method that does not exist. |
| schema | `GET /project/{key}/version` takes `orderBy` (`sequence` is "the order of appearance in the user interface"), a `status` filter of `released,unreleased,archived`, and a literal `query` | The walk asks for `sequence`: offsets over an undefined order are not stable between pages. The `status` filter stays unused — one list feeds a picker and a renderer, which want different subsets of it. |
| schema | `issueSources[].type` arrives **capitalised** — `Board`, `Project`, `Filter` — and the enum has a fourth member, **`Custom`**, that `jira.PlanSourceType` has no constant for | Lowercase it at the boundary or nothing matches the port's own values. Keep an unrecognised type rather than dropping the source: a plan quietly short of an issue source renders as a narrower plan, and no error anywhere says so. |
| schema | Nothing in this port turns the numeric project id on a plan's issue source into a project key — `/project/search` is the only endpoint that answers it, and the port does not expose it | A project source can only be rendered as an id today. `jiratest.Fake.Plans` puts a project **key** in that slot, so the same view shows `EX` against the fake and `10000` against a site: the divergence is the fake's, not the schema's. **Do not write a plan view against the fake's value here.** It is asserted, red, by `TestPlans_BothAdaptersAnswerTheSameWay/a_project_issue_source_carries_the_numeric_project_id_the_schema_documents/fake`, and that test goes green when `pkg/jira/jiratest/fake.go` sends an id. Rendering a project source as its key needs a port method that does not exist yet. |
| schema | `maxResults` on the plan list is capped at **50**, which is also its default | A site with more plans than that pages, and a walk whose page size comes from the site changes length when the site does — so 50 is sent explicitly. |
| schema | `includeTrashed` and `includeArchived` are **optional with no documented default** | So which plans a site lists is not knowable from the schema, and `plans_ok.json` lists a `Trashed` one. `Plans` sends neither flag, and nothing may assume a row is live — read `Plan.Status`. |
| schema | `status` is a **closed enum**: `Active`, `Trashed`, `Archived` | This one is API vocabulary, not a display name an administrator renames and not translated on a German site, so a view **may** branch on it — the never-match-a-name rule is about instance-defined words like a status or a priority. `pkg/jira/cloud/plan.go` carries an unrecognised fourth value through rather than dropping the plan; `jira.Plan.Status` is still a bare string with no constants beside it. |

## Attachments, versions and sprint writes, as the fixtures answer them

These rows are what `pkg/jira/jiratest/fixtures/**` and the fixture server's routes were written to,
so that Batch 4-8 has something to test an adapter against. The marker on each says how far that
goes: a `schema` row is Atlassian's published shape, an `assumed` row is somebody reasoning with no
site to check against. Nothing here is `live`.

| Source | Fact | Consequence |
|---|---|---|
| assumed | The attachment array on an **issue read** spells `id` as a **string**, the way the upload's answer does and unlike `GET /attachment/{id}` (the `live` id-type row in Hard constraints has the other two) | Normalise to a string at the boundary; nothing may compare an id from two reads raw. |
| assumed | An entry in the upload's answer **omits `thumbnail` entirely** for a file the site could not thumbnail, and sends it beside `content` for one it could | Branch on the key's presence. A struct that expects it everywhere reads an empty string as a URL and a preview pane then fetches nothing, twice. |
| assumed | With `?redirect=false` Jira honours `Range` **itself**: `bytes=N-` answers **206** with `Content-Range: bytes N-<last>/<length>`, and a start past the end answers **416** | `DownloadOptions.From` is a real resume rather than a re-read with the front thrown away. The `live` row above covers the redirect and where an unqualified 206 comes from. `jiratest.AttachmentContent` is the payload the fixture server streams, and its `/media/` route stands in for the host a redirect points at. |
| assumed | A site with attachments switched off refuses the upload in the **classic envelope** — a sentence in `errorMessages`, `errors` empty | It is a Jira handler saying no, so it is not one of the two shapes the same endpoint answers a bad request in. `attachment_disabled.json` is that body. |
| schema | `GET /rest/api/3/version/{id}/unresolvedIssueCount` answers three keys and no more: `self`, `issuesUnresolvedCount` and `issuesCount` (`VersionUnresolvedIssuesCount`) | The total arrives beside the count, so a release gate can say "8 of 14" from one request. Only the unresolved half is the gate. |
| schema | `POST /rest/api/3/version` answers **201** with the whole version, and `PUT /rest/api/3/version/{id}` answers **200** with it | Neither write needs a confirming read. |
| assumed | `GET /rest/agile/1.0/sprint/{id}` answers the same object an entry of `/board/{id}/sprint` carries, dates included | It is what `jira.Client.Sprint` calls, so a timeline resolves an issue's sprint id without walking every board of the project. A sprint answers with no dates until they are **set**, whatever its state — `sprint_page.json`'s sprint 43 has none and `sprint_created.json` is a future sprint with both — so a missing date is nothing to draw rather than a failed read. |
| schema | `POST /rest/agile/1.0/sprint` answers **201** with the created sprint | The new sprint's id arrives with it, so a create needs no confirming read. It is the only one of the four sprint writes whose status is documented; the three below are reasoned. |
| assumed | `POST /rest/agile/1.0/sprint/{id}` answers **200** with the whole sprint as it stands after the patch | The partial update is both the safe call and the one that answers enough to redraw the row. It answers the state the transition left, so `sprint_updated.json` is mid-life: only `CompleteSprint` gets a `completeDate` back, and a test for that one overrides the route with `sprint_one.json`. |
| assumed | `POST /rest/agile/1.0/sprint/{id}/issue` and `POST /rest/agile/1.0/backlog/issue` answer **204 with no body** | There is nothing to decode, so a move is reported from the status and the 50-issue cap above is the only thing to chunk for. |
| assumed | A `CANCEL_REQUESTED` task carries **no `finished` and no `result`**, exactly as a `RUNNING` one does | The state is not an ending, and the body agrees with `TaskState.Done()` rather than needing a special case. `task_cancel_requested.json` is that body. |
| schema | The bulk queue's `status` is the **same seven-value enum** as the generic task endpoint's, `CANCEL_REQUESTED` included (`BulkOperationProgress.status`) | A move poller needs every case a task poller needs. There is no `bulkmove_task_cancel_requested.json`, and that is a gap in the fixture set rather than a state the endpoint cannot report. |

## Writes that do not read back at once

One consequence, stated once: **never confirm a write by reading it back.** Say what you asked for,
report the status the write returned, and let the next scheduled read correct the screen.

| Source | Fact | Consequence |
|---|---|---|
| live | A rank write answers 204 and the rank field's value changes immediately, but the order `/board/{id}/issue` reports lags behind it | Move the row locally and trust the 204. A confirming read can hand back the old order, and a view that re-fetches to confirm snaps the row to where the user dragged it *from*. |
| live | `approximate-count` lags a write by seconds: straight after clearing 45 assignees it answered 42 and 104 where an exact walk said 46 and 100 | It is an estimate and named as one. Never print it beside a list it can contradict, and never use it to decide whether a write landed. |
| live | A locale change takes minutes to appear, and to revert | See Localisation. Nothing may be tested by setting the locale and reading straight after. |
| live | A labels write comes back sorted, and ADF normalises two things on the way in | See the ADF section. A byte difference between what you sent and what came back is not a failure. |

## Rate limiting

| Source | Fact | Consequence |
|---|---|---|
| live | **`X-RateLimit-Limit` and `X-RateLimit-Remaining` are on every response**, and their values differ per endpoint | A budget is per endpoint, not per site, and a client can slow down *before* a 429 rather than after one. Read them on success, not only on failure. |
| schema | `Retry-After` is not always a number of seconds — HTTP permits an absolute date, and Atlassian also sends **`X-RateLimit-Reset`** as an RFC 3339 instant | A client that only does `strconv.Atoi` on `Retry-After` silently falls back to its own backoff on both and waits the wrong amount. Read seconds, then an HTTP-date, then `X-RateLimit-Reset`, and clamp an instant already in the past to zero. |

Honour `Retry-After` on 429. Back off exponentially with jitter, cap concurrent requests, and pause
any poller on the first 429. Cost-based limits mean a burst of narrow requests beats one wide one.

## Still to confirm against live responses

- Every row marked `schema`.
- The Plans API end to end. It needs Administer Jira on every endpoint and the testbed account never
  exercised it.
- Bulk **move** in particular. Only bulk transition was run; the two share a queue, so the progress
  bodies above are trustworthy and the move's own caps, permissions and failure detail are not.
- The move payload itself: the mapping key format, the four required infer flags, the direction of
  `targetStatus` and the `{retain, type, value}` wrapper, all read off the published schema and the
  published examples, never sent to a site. So is the 1000 cap and the 1,500,000-field limit beside
  it. The 1000 **counts every subtask that travels with a named issue**, which is a number no caller
  knows before it submits, so the local refusal can only be a floor.
- Whether a mid-run queue body really omits `totalIssueCount` and `processedAccessibleIssues`. The
  documented running body carries neither, and every committed fixture carries both, so the branch
  that reports counts absent is reached only by an inline body in `bulkmove_test.go`. A fixture in
  the documented shape, and one with a non-zero `invalidOrInaccessibleIssueCount`, belong in
  `pkg/jira/jiratest/fixtures`. Both counts or neither is now relied on: `counts()` builds its
  sentence only when `totalIssueCount` is there, so a body that listed processed issues without a
  total would report nothing rather than a count out of nothing.
- Whether the queue ever answers a state without `taskId`, or the task registry one without `id`.
  `pkg/jira/cloud/bulkmove.go` refuses a progress body that carries neither, because that is what
  tells the two registries' answers apart, and every fixture and documented example has one.
- Every row marked `assumed` under *Attachments, versions and sprint writes* above: the shape of
  `GET /rest/agile/1.0/sprint/{id}`, the statuses the three sprint writes other than the create
  answer, whether the attachment content endpoint really answers 206 and 416 the way the fixture
  server does, which keys an upload's answer drops, and whether a `CANCEL_REQUESTED` body omits
  `result`.
- Whether `Retry-After` ever arrives as an HTTP-date. No 429 was provoked.
- The status, body and 403 shape of `DELETE /rest/api/3/attachment/{id}`, and whether `?redirect=false`
  honours a `Range`. The download asks for both and reads a 200, a 206 and a 303, because one site was
  watched redirecting even with a `Range` and nobody has watched it with `redirect=false`.
- What a `403` from the **media host** means. `pkg/jira/cloud` reports it as a transport failure on the
  assumption it is an expired signature rather than a permission, which nobody has watched happen.
- Whether a real site's `416` carries `Content-Range` at all. The row above depends on it, and a refusal
  that names no total is read as the offset being past the end, which is all the status alone says.
- Whether a site with attachments switched off refuses `DELETE /attachment/{id}` and how. The upload
  reads `/attachment/meta` and refuses locally; the delete cannot, because refusing it locally would
  block a clean-up the site may perfectly well allow. `jiratest.Fake` does refuse it, so a view tested
  against the fake meets a refusal the adapter cannot produce.
- Whether `isAvailable` on a transition is ever `false` without asking for unavailable ones.
- What `/user/search` answers a token that lacks **Browse users and groups**. The testbed account
  held it, so the 403 the `CapPeople` probe is written around has never been seen. A site that
  answers `200 []` instead would read as "nobody is here", which is the wrong sentence.
- Whether `/user/search` caps `maxResults` the way `/search/jql` silently caps at 100. The measured
  site had eleven accounts and never reached a cap, so `FindPeople` holds its own ceiling as well as
  asking for one.

Fixtures under `pkg/jira/jiratest/fixtures/` are corrected from a capture's **shape** and given
invented words; captures themselves stay in the gitignored `testdata/live/`. A shape here marked
`live` is a shape a fixture can now be held to.

Module paths, all confirmed against the proxy in August 2026: `charm.land/bubbletea/v2`,
`charm.land/lipgloss/v2`, `charm.land/bubbles/v2` and `github.com/lrstanley/bubblezone/v2` (the v1
bubblezone still depends on Bubble Tea v1 and must not be used).
