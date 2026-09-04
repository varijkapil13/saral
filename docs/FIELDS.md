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
separates it from a field id, a field name or a status name; the tree already matches on one, in
`internal/app/dates.go`, to find the sprint field. **The exact keys must be read off
`GET /rest/api/3/field` on a real site rather than written from memory** — `scripts/capture.sh` exists
for that and has never been run, which is why this packet starts there.

- Hidden fields are **counted, not dropped**: the "N more, all empty" line becomes a line that says
  how many were left out and why, because a value on the issue that the program silently discards is
  the failure this repo already avoids for a value it cannot label.
- A settings row turns the whole thing off, for somebody who does want to see the rank.
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
