# The fields on an issue

The sidebar lists every field the site defines that the issue carries a value for, sorted by display
name. On a real Jira Software project that means the plugin's own bookkeeping arrives with the rest:

```
  Fields
    Epic Color      ghx-label-5
    Epic Status     To Do
    Issue color     dark_teal
    Rank            0|i034ri:
```

None of those four is written for a person. They are how Jira's own UI colours a card and orders a
backlog, and they crowd out the fields somebody put there on purpose.

Three packets, in the order they pay off. P5 is worth doing alone; P7 is the one that answers "show
me *my* fields".

## Why not the pinned fields Jira itself has

The Jira Cloud issue view lets a user pin fields to the top. **There is no public API for it.**
Neither the v3 platform spec nor the Software spec carries a pinned-fields resource; the feature is a
per-user preference served by an internal `/rest/internal/…` route that is not published, not
supported and free to change. `docs/API-NOTES.md` marks every trap with whether a real site confirmed
it, and an undocumented route cannot honestly carry that mark. So the pin list is this program's own,
which P7 builds, and the site's own answer about which fields matter comes from P6 instead.

## P5 — Leave out the plugin's own bookkeeping

The discriminator is `jira.FieldSchema.Custom`, the plugin key a custom field declares — for the four
above, `com.pyxis.greenhopper.jira:gh-lexo-rank` and its siblings.

**A plugin key is not instance data.** It is the same string on every Jira Cloud site, which is what
separates it from a field id, a field name or a status name.

No real site has been read for this packet, and none may be. The denylist is seeded from what this
repo's own fixtures already confirm — `gh-lexo-rank`, minted by `pkg/jira/jiratest/gen.go` — plus this
program's best knowledge of Jira Software's other presentation and ordering fields. Everything past
`gh-lexo-rank` is unconfirmed until somebody runs `scripts/capture.sh` against a real site and checks
it; `internal/ui/issue/fields.go`'s `bookkeepingFields` marks the difference per entry, and so does the
table below.

| Plugin key | Field | Confirmed by |
|---|---|---|
| `com.pyxis.greenhopper.jira:gh-lexo-rank` | Rank | `pkg/jira/jiratest/gen.go` |
| `com.pyxis.greenhopper.jira:gh-epic-color` | Epic Colour | best knowledge, unconfirmed |
| `com.pyxis.greenhopper.jira:jsw-issue-color` | Issue colour | best knowledge, unconfirmed |
| `com.pyxis.greenhopper.jira:gh-epic-status` | Epic Status | best knowledge, unconfirmed |
| `com.atlassian.jira.ext.charting:timeinstatus` | Time in Status | best knowledge, unconfirmed |
| `com.atlassian.jira.plugins.jira-development-integration-plugin:devsummarycf` | Development | best knowledge, unconfirmed |
| `com.atlassian.servicedesk:vp-origin` | (internal) | best knowledge, unconfirmed |

**What is deliberately kept**, because the list is easier to get wrong in this direction: `gh-sprint`,
`gh-epic-link`, `gh-epic-label` (which is the Epic *Name*, not a colour), story points and every
`customfieldtypes:*`, which are the fields somebody defined on purpose. And all three
`com.atlassian.jpo:jpo-custom-field-*` — Advanced Roadmaps' baseline start, baseline end and parent
link — because the timeline resolves a bar's dates from exactly those, so hiding them would hide what
a bar is drawn from. Service Management's SLA, participants, organisations and approvals stay, and so
does every `jira.polaris:*`: a Product Discovery field is somebody's research, not bookkeeping.

A wrong key here is inert: it matches nothing on any site, so it hides nothing that should have been
drawn, which is the direction a mistake in this table is safe to fall in. A wrong *pattern* — matching
on a field's name or id instead of its plugin key — is not, so a test asserts every entry contains a
colon and none reads like a `customfield_NNNNN` id.

- Hidden fields are **counted, not dropped**: the "N more, all empty" line gets a sibling that says how
  many were left out and why, because a value on the issue that the program silently discards is the
  failure this repo already avoids for a value it cannot label.
- The `issue.bookkeeping` settings row turns the whole thing off for somebody who does want to see the
  rank. It is session-scoped — nothing this packet owns persists it the way the theme or the mouse are
  — so it lasts until the program restarts.
- The denylist is a belt, not the trousers: it goes stale as Atlassian adds fields, which is what P6
  is for.

## P6 — Let the site say which fields belong on this issue

`GET /rest/api/3/issue/{key}/editmeta` returns the fields on this issue's screen, resolved through
the screen scheme, the field configuration and each custom field's context — the site's own answer to
what belongs on this issue type, needing only *Browse projects*.

- It answers with **editable** fields, so a field that is read-only on the view screen is absent from
  it. That makes it an ordering and relevance signal and **not** the sole rule about what to draw; a
  field with a value that editmeta does not mention is still drawn, below the ones it does.
- One extra request per issue opened. It is coalesced with the read already in flight and cached
  against the issue, and it is not fetched at all where the sidebar is not drawn.
- A port method, so `pkg/jira/port.go` grows one — which `docs/PARALLEL.md` says needs an issue
  labelled `contract` and a fast review, because it unblocks or blocks everyone.

## P7 — Pin your own

A list of field ids kept **per profile**, in `config.toml` beside the timeline field names. Per
profile and not per machine, because a field id is a site's own — `customfield_13401` means nothing
on another site — which is the opposite of the reason `ui.toml` holds the pane split.

```toml
[profiles.work]
pinned = ["customfield_13401", "duedate", "customfield_13402"]
```

- Pinned fields draw first, in the order they were pinned, under their own heading; everything else
  follows as it does now.
- Editing the list is a settings row that opens **the multi-select picker F2 built**, over the site's
  own field catalogue. That is the whole of the UI, and it is a picker that already exists.
- Pinning from the issue itself is not in this packet: the sidebar has no per-field cursor, and
  giving it one is a bigger change than the list is worth.

## Definition of done, beyond `docs/PARALLEL.md`

- A capture was taken and the plugin keys in the denylist appear in it; `scripts/checkleak.py
  --require-capture` was run and the diff read. **No field name, status name or project key in the
  diff** — only plugin keys, and a test asserts every entry of the denylist is a plugin key rather
  than a field id.
- A test that a hidden field is counted rather than lost, and that the setting brings it back.
- editmeta failing — 403, 429, transport — leaves the sidebar drawing what it drew before, because a
  relevance signal that did not arrive is not a reason to show nothing.
- A pinned field id that no longer exists on the site is dropped from the drawing and kept in the
  file, so a profile shared between two sites does not lose its list.
- Golden files for the sidebar: nothing pinned, three pinned, and one pinned field absent from the
  site.
