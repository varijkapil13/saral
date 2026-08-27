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
`bulkmove_task_*.json` (one per task state), `task_*.json` (five states on the generic task
endpoint, which answers a different shape), `validation_error.json` (400 with per-field errors),
`attachment_disabled.json` (the 403 a site with attachments switched off answers an upload with).

The `task_*.json` set describes one run of the operation Atlassian's docs point at this endpoint from:
replacing a custom-field select option across issues. It reuses the invented `Rollout Phase` field and
its options from `createmeta_task.json`, and numbers its task differently from the bulk-move one —
they are separate registries, and a shared id would read as one task answering at both endpoints.

`task_cancel_requested.json` is the fifth state and the reason the set is not four: a cancelled task
reads `CANCEL_REQUESTED` for as long as it takes to stop, so it is a state a poller sees rather than
an end state. It carries no `finished` and no `result`, because the task has not stopped. The
bulk queue reports the same seven names, so there is nothing special about the generic endpoint here:
the missing `bulkmove_task_cancel_requested.json` is a gap in this set, not a state that queue cannot
report.

## The Agile API pages three different ways

There is one envelope per shape here, because no endpoint announces which one it answers in and a
decoder that reads only the common one comes back empty rather than failing:

| fixture | array | `total` | `isLast` | stands for |
|---|---|---|---|---|
| `board_issues.json` | `issues` | yes | **no** | `/board/{id}/issue`, `/board/{id}/backlog` |
| `board_epics.json` | `values` | **no** | yes | `/board/{id}/epic`, `/board/{id}/version` |
| `sprint_page.json` | `values` | yes | yes | `/board/{id}/sprint`, `/board` |

`board_issues.json` is served on both the board and the backlog route: the two answer the same
envelope, and its issues carry an Agile `self` (`/rest/agile/1.0/issue/{id}`) with `/rest/api/2`
links inside them, which is what that endpoint really sends.

## Accounts, and the words a filter narrows by

Five more shapes, none of them the shape beside it:

| fixture | shape | what it is there to catch |
|---|---|---|
| `user_search.json` | a **bare array**, no envelope at all | neither paginator here reads it; it also holds all three `accountType`s, an inactive account, an id with a **colon** in it, and an app whose `emailAddress` is `""` |
| `user_assignable.json` | the same bare array, people only | the assignable endpoint drops app accounts without being asked, which is the difference between a readable picker and a page of robots |
| `user_bulk.json` / `user_bulk_page2.json` | the Agile offset envelope, on a platform endpoint | `values` carries a **JSON `null`** for an id the site does not know, and `total` counts the ids asked for — so the null has to be counted for the walk and dropped for the caller |
| `project_statuses.json` | a bare array of issue types, each with its own `statuses` | two types in one project run different workflows, and **two distinct ids share the display name `In Review`**, which is what a team-managed project mints |
| `priority_search.json` | the paged envelope, in ranking order | the order is not alphabetical, and `isDefault` is not always set on one of them |
| `labels.json` / `labels_page2.json` | a paged envelope whose `values` are **bare strings** | one label is not ASCII, because a label is whatever anybody typed and a width taken with `len()` over one is wrong |

`forbidden_browse_users.json` is the 403 a token without *Browse users and groups* is expected to
get. It is hand-authored: the testbed account held the permission, so the refusal has never been seen
and its wording is invented rather than corrected from a capture.

The account ids here are the older opaque form plus one that carries a colon. A real colon id is a
numeric prefix and a UUID, which is exactly the shape `TestFixtures_CarryNoRealAccountID` refuses, so
the one here keeps the prefix and the colon and is visibly not a UUID.

## A site refuses in two different envelopes

`not_found_board.json` is the Agile one: `errorMessages` empty, and the sentence in `errors` under
the name of a **URL parameter** rather than a field. Nothing keyed like that may become a form-field
error — no input is called `rapidViewId`.

`problem_no_endpoint.json` and `problem_method_not_allowed.json` are RFC 7807 `problem+json`, which is
what answers a request that reached the site and no handler. `detail` is the only part that says
anything; `title` is the status spelt out. The fixture server answers an unrouted path this way too,
because a real site does.

`field_localised.json` is the field catalogue as a site whose language is not English sends it: display
names translated, `untranslatedName` not, one field spelling it as a single run-together word so it
matches neither the translated name nor the English one, two fields whose display names have collapsed
into a single string, and a field with an empty `clauseNames`, which is the site saying it cannot be
named in JQL at all.

## Attachments answer in three JSON shapes, and the content in none of them

| fixture | shape | what it is there to catch |
|---|---|---|
| `attachment_meta.json` | one object, `id` a **JSON number** | the same identifier is a **string** on the issue read and in the upload's answer, so nothing may compare one against another without normalising first |
| `attachment_upload.json` | a **bare array** of attachment objects | a two-file upload answers two rows and no envelope; the non-image row carries **no `thumbnail` key**, which the image row does |
| `attachment_disabled.json` | the classic `errorMessages` envelope | a site with attachments switched off refuses in the shape a Jira handler writes, not in RFC 7807 — that one answers a malformed multipart instead |

The content of an attachment is not a fixture at all: `GET /rest/api/3/attachment/content/{id}`
streams bytes and honours `Range`, so the server answers it from a handler over
`jiratest.AttachmentContent`. It 303-redirects to a signed media URL unless the request carries
`redirect=false`, and it redirects a `Range` request just the same — the `206` comes from the host the
redirect points at, which the server stands in for on a `/media/` route of its own. The upload route
enforces the two things a real site enforces before it reads a byte: the `X-Atlassian-Token: no-check`
header, whose absence is a **404 with a plain-text body**, and a part named exactly `file`, whose
absence is an RFC 7807 400.

## Versions: the number a release turns on is a second request

`version_unresolved_count.json` is the only fixture that carries `issuesUnresolvedCount`, because no
version read carries it — not the paged list, not a single-version read, not the answer to a write.
It counts by **resolution** while `issuesStatusForFixVersion` in `versions.json` buckets by **status
category**, and the two deliberately disagree: an issue can sit in the done bucket with no resolution,
so only the count is the gate.

Three fixtures are the same version at three moments, so an adapter test can list, read and release
without the id changing state under it: `versions.json` and `version_one.json` hold 10100 unreleased —
the paged list with its status buckets, the single read without them — and `version_released.json` is
that same 10100 after the `PUT` that shipped it. `version_created.json` is a different version, 10101,
because a create answers an id the list has never seen.

The four also differ on a key that is not `released`: `overdue` is sent explicitly as `false` on an
unreleased version and is **absent** from a released one. Read `released` for whether it shipped —
absence of `overdue` is not a second way of saying so, and a version trimmed into an issue read or a
createmeta answer omits the key whatever its state.
`TestFixtures_EveryVersionSaysWhetherItShippedTheWayASiteDoes` sweeps every version object in every
fixture for both rules, and for `projectId` being the JSON **number** a site sends.

## Sprints, and the date spelling that moves between two endpoints

`sprint_one.json` is one sprint at `GET /rest/agile/1.0/sprint/{id}` — the endpoint a timeline
resolves an issue's sprint id through, having no board to reach it by. It is the same sprint the
board's page lists, with the same dates written the same way.

`sprint_updated.json` is what the partial update answers: `POST /rest/agile/1.0/sprint/{id}` sends
back the sprint as the transition left it, so the default route answers a sprint **mid-life** —
active, dated, with no `completeDate`. Only `CompleteSprint` gets a terminal sprint back, and a test
for that one overrides the route:

    WithFixture(http.MethodPost, "/rest/agile/1.0/sprint/{id}", "sprint_one.json")

`sprint_created.json` writes its dates the other way a site sends them: UTC-normalised with a `Z`,
which is what a write answers whatever offset it was given. Both spellings are here on purpose,
because a decoder holding one layout constant parses one of them and reads the other as a zero time.
Neither parses with the platform layout that has no colon in its offset; a sprint boundary needs
`time.RFC3339`.

There is **no route for `PUT /rest/agile/1.0/sprint/{id}`**, and a test holds it that way. That call
is the full replace: it nulls every field the request omits, which is why the port exposes
`UpdateSprint`, `StartSprint` and `CompleteSprint` instead. An adapter reaching for it finds no
endpoint here rather than a success it will not get from a site.
