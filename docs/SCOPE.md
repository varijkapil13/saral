# Scope

## In scope

- **Tickets** — search and browse via JQL, read, create, edit, transition. Forms generated from
  `createmeta`, so they are correct on any instance.
- **Comments** — add, edit, delete, with `$EDITOR` handoff and markdown ⇄ ADF.
- **Attachments** — list, upload, ranged download with progress, delete, inline image preview.
- **Releases** — list versions with unresolved counts, create, edit, archive, bulk fix-version
  assignment, and *releasing* a version with the unresolved-issue decision the web app forces.
- **Sprints and boards** — board columns from board configuration, backlog, create/start/complete
  sprints, move issues between sprint and backlog, rank-aware reorder.
- **Cross-project move** — the bulk-move wizard with status and field remapping.
- **Timeline** — derived from start and end dates (see the resolution cascade in `ROADMAP.md`), with
  sprint and version markers.
- **Plans** — real plans where the token has Administer Jira, locally defined plans otherwise.

## Out of scope

| Item | Why |
|---|---|
| Release-note generation | No API exists; `ReleaseNote.jspa` is a rendered web page. Decided out. |
| Live push updates | Webhooks need a Connect/OAuth app and a public URL. An optional scoped poller is the supported equivalent. |
| Confluence | Deferred, not excluded — it arrives behind its own port. Its storage format is not Jira ADF. |
| Jira Data Center | Cloud only in v1; DC is a second adapter behind the same port. It has no bulk-move and no plans API. |
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
