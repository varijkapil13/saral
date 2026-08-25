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
`bulkmove_task_*.json` (one per task state), `task_*.json` (the same four states on the generic task
endpoint, which answers a different shape), `validation_error.json` (400 with per-field errors).

The `task_*.json` set describes one run of the operation Atlassian's docs point at this endpoint from:
replacing a custom-field select option across issues. It reuses the invented `Rollout Phase` field and
its options from `createmeta_task.json`, and numbers its task differently from the bulk-move one —
they are separate registries, and a shared id would read as one task answering at both endpoints.

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
