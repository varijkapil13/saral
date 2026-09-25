# Scripting Saral

Every command on this page runs without a terminal UI and needs no TTY. Each one resolves the
profile the way the TUI does: `--profile`, then `SARAL_PROFILE`, then the active profile, with
`SARAL_SITE`, `SARAL_EMAIL` and `SARAL_TOKEN` over the top. With no config file at all, those three
variables are a profile of their own, which is how a CI job runs these. Each command also takes
`--profile`, `--project` and `--log`, either before or after the command's name.

Flags may come before or after the arguments (`saral issue view PROJ-1 --json` works). `--` ends the
flags. `-h` on any command prints its flags and exits 0.

```sh
saral issue view PROJ-142 [--json]
saral search 'assignee = currentUser() AND resolution = EMPTY' [--json] [--limit N] [--fields F,G]
saral issue create --project PROJ --type Story --summary 'Title' [--description-file F] [--parent KEY] [--json]
saral transition PROJ-142 'In Review' [--field 'Resolution=Done']
saral comment add PROJ-142 (-m 'text' | --file F)
saral assign PROJ-142 (me | none | <account ID> | <name or email>)
saral open PROJ-142 [--print]
saral completion bash|zsh|fish
```

An issue argument can be a key in any case (`proj-142`) or a Jira link. A link to a site other than
the profile's is refused rather than read as a key on this one.

## Output

Plain output is tab-separated, one record per line. Every value is on one line: line breaks become
spaces, a tab becomes a space, and terminal escape sequences and bidi overrides are removed.

| Command | Plain output |
|---|---|
| `issue view` | `name<TAB>value` rows: `key`, `url`, then each platform field under its JSON member name (empty when unset), then every custom field that has a value, under the name this site gives it, sorted. After a blank line comes the description as Markdown, if there is one. |
| `search` | `KEY<TAB>...` with one column per field. With no `--fields` the columns are type, status, priority, assignee, updated and summary. With `--fields` they are the fields named, in that order. There is no header row. |
| `issue create` | `KEY<TAB>URL` |
| `transition` | `KEY<TAB>STATUS` (the status the issue moved to) |
| `comment add` | `KEY<TAB>COMMENT_ID` |
| `assign` | `KEY<TAB>DISPLAY NAME<TAB>ACCOUNT ID`, or `KEY<TAB>unassigned` |
| `open` | the issue's URL |

`--fields` takes field IDs (`labels`, `customfield_10016`) or the names this site gives its fields
(`Story Points`). Names are matched the way the rest of Saral matches them (see `jira.ResolveField`).
A name that matches no field, or matches two, is a usage error.

`search -` reads the JQL from stdin. `--file -` and `--description-file -` read the file from stdin.

## JSON schema

`issue view --json` prints one issue object. `search --json` prints this:

```json
{ "issues": [ <issue>, ... ], "more": false }
```

`more` is true when the site had more matches than `--limit` let through (the default limit is 50).
`issue create --json` prints `{"key", "id", "url"}`.

An issue object always has `key`, `id` and `url`. **Every other member is present only when that
field was asked for**, and it is `null` when it was asked for and Jira had nothing. So a missing
`assignee` means the field was not read, and `"assignee": null` means the issue is unassigned.
`issue view` asks for every member. `search` asks for its default columns or for `--fields`.

| Member | Field ID | Type |
|---|---|---|
| `key`, `id`, `url` | — | string |
| `project` | `project` | `{"key", "name"}` |
| `type` | `issuetype` | `{"id", "name", "subtask": bool}` |
| `status` | `status` | `{"id", "name", "category"}`; `category` is `new`, `indeterminate`, `done` or `unknown`, and is the same on every site whatever the status is called |
| `summary` | `summary` | string |
| `priority` | `priority` | `{"id", "name"}` or null |
| `resolution` | `resolution` | `{"id", "name"}` or null |
| `assignee`, `reporter` | `assignee`, `reporter` | `{"accountId", "displayName", "email"}` or null; `email` is `""` when the account hides it |
| `labels`, `components`, `fixVersions` | same | array of names; `[]` when empty |
| `parent` | `parent` | `{"key", "summary", "status"}` or null |
| `subtasks` | `subtasks` | array of `{"key", "summary", "status"}` |
| `links` | `issuelinks` | array of `{"type", "label", "key"}`; `label` is the phrase from this issue's side, e.g. `is blocked by` |
| `due` | `duedate` | `"YYYY-MM-DD"` or null |
| `created`, `updated`, `resolved` | `created`, `updated`, `resolutiondate` | RFC 3339 in UTC, or null |
| `timeTracking` | `timetracking` | `{"originalEstimate", "remainingEstimate", "timeSpent"}` in seconds, or null |
| `description` | `description` | Markdown string, or null |
| `fields` | any other | object keyed by field ID, each `{"name", "value"}`. It holds only the fields that have a value, and is left out when none does. |

A custom field's `value` depends on the field's type. Text is a string, a number is a number, a date
is `"YYYY-MM-DD"`, and a date-time is RFC 3339. Rich text is Markdown. A single select is
`{"id", "label"}` (with `children` for a cascading select), and a multi-select is an array of those.
A user is the user object above, and a multi-user field is an array of them. A type Saral does not
model arrives as Jira's own JSON. Field IDs differ per site, so look a field up by `name` rather than
by an ID copied from another site.

New members may be added to the schema. Existing members are not renamed and keep their types.

## Writing

- **`issue create`** takes the project from `--project`, then from the profile. `--type` is a name or
  an ID as this project has them. When two types share a name, pass the ID. The description is
  Markdown, converted to Jira's document format. A field the project requires that these flags do
  not cover is refused by Jira, and the refusal is printed.
- **`transition`** takes a transition's name or the name of the status it leads to. A transition's own
  name wins. When two transitions lead to the same status, name the transition. A transition whose
  screen requires a field is refused until `--field 'Name=Value'` fills it. Only fields with a fixed
  list of values (a resolution, a select) can be filled, by option label or ID.
- **`comment add`** posts Markdown.
- **`assign`** takes `me`, `none` (unassign), an account ID, or text to search for. A search covers
  the people who can be assigned in the issue's project. If exactly one matches, or exactly one
  display name or email is an exact match among several, that person is assigned. Otherwise the
  candidates are listed and nothing is written. Searching needs the Browse users and groups
  permission; `me`, `none` and an account ID do not.
- **`open`** needs only the profile's site: no token and no request. `--print` prints the link without
  starting a browser.

## Exit codes

The same as the TUI's (`saral --help`):

| Code | Meaning |
|---|---|
| 0 | success |
| 1 | anything else: the site could not be reached, it refused the request (403), it rate-limited (429), the issue does not exist |
| 2 | a flag or argument was not understood, including a status, type, field or person that matches nothing or more than one thing |
| 3 | config.toml or the profile is unusable, or there is no profile |
| 4 | the token could not be found, or the site refused it (401) |

Errors go to stderr as one `saral: ...` line. Nothing is printed to stdout on failure.

## Completion

```sh
source <(saral completion bash)     # ~/.bashrc
source <(saral completion zsh)      # ~/.zshrc, after compinit
saral completion fish | source      # ~/.config/fish/config.fish
```

The scripts are generated from the same registrations the commands parse, so they offer every
command, view name, sub-action and flag in the binary that wrote them.
