# Scope

## In scope

- **Tickets** — search and browse via JQL, read, create, edit (custom fields included, from the
  issue's own edit screen), transition, clone. Forms generated from `createmeta`, so they are correct
  on any instance.
- **Around an issue** — links, worklogs, watchers, copy key or link, open in the browser.
- **Comments** — add, edit, delete, with `$EDITOR` handoff, markdown ⇄ ADF and `@mention`
  autocomplete that writes real mention nodes.
- **Attachments** — list, upload, ranged download with progress, delete, inline image preview.
- **Releases** — list versions with unresolved counts, create, edit, archive, bulk fix-version
  assignment over a JQL query (`b` on the release list), and *releasing* a version with the
  unresolved-issue decision the web app forces.
- **Sprints and boards** — board columns from board configuration, backlog, create/start/complete
  sprints (completion asks where the open issues go), move issues between sprint and backlog,
  rank reorder on the board and the backlog.
- **Cross-project move** — the bulk-move wizard with status and field remapping.
- **Timeline** — derived from start and end dates (see the resolution cascade in `ROADMAP.md`), with
  sprint and version markers.
- **Plans** — real plans where the token has Administer Jira, locally defined plans otherwise.
- **Scripting** — non-interactive `issue`, `search`, `transition`, `comment`, `assign` and `open`
  commands with a stable JSON schema (`docs/CLI.md`), and `saral doctor` for bug reports.

## Out of scope

| Item | Why |
|---|---|
| Release-note generation | No API exists; `ReleaseNote.jspa` is a rendered web page. Decided out. |
| Live push updates | Webhooks need a Connect/OAuth app and a public URL. An optional scoped poller is the supported equivalent. |
| Confluence | Deferred, not excluded — it arrives behind its own port. Its storage format is not Jira ADF. |
| Jira Data Center and Server | Cloud only for now; setup reads `serverInfo` and refuses a site that is not Cloud. DC is a second adapter behind the same port. It has no bulk-move and no plans API. |
| Scoped API tokens, OAuth 2.0, SSO-only accounts | Not yet. Authentication is an API token and the account's email over basic auth, against the site's own host. Scoped tokens and OAuth go through `api.atlassian.com` with a cloud id, which the adapter does not build. |
| Project / workflow administration | Large surface, wrong character for a daily driver. |
| Multiple sites at once | Profiles in config, one active at a time. |

## Non-negotiables

1. **No instance assumptions.** No project keys, field IDs, statuses, issue types or permissions
   baked into code. Everything discovered at runtime and cached.
2. **Graceful degradation.** Capabilities are probed once and cached; a missing one hides or disables
   its feature *with the reason shown*. A 403 is a capability answer, never a crash and never a
   silent empty list.
3. **Testable without Jira.** The whole suite runs against an in-memory fake and recorded fixtures.
4. **No telemetry.** Frecency and history are local files. Nothing leaves the machine except Jira
   API calls.
