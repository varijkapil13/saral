# ADR 0001 — Ports and adapters, with a registry-based UI

- **Status:** accepted
- **Date:** 2026-08-19

## Context

Kanso must run against any Jira Cloud site, be testable without a Jira instance, and be worked on by
several agents in parallel without constant merge conflicts. It should also be able to grow a Jira
Data Center backend and a Confluence surface later without a rewrite.

Two problems dominate:

1. Jira's API is wide, inconsistent and instance-dependent. Letting its shape leak into the UI would
   make the UI untestable and every instance-specific quirk viral.
2. In a typical TUI, adding a view means editing a central dispatcher — a `switch` over view IDs, a
   keymap table, a footer definition. Those files become permanent merge conflicts when several
   people add views at the same time.

## Decision

**Ports and adapters** for the Jira boundary: `pkg/jira` defines a narrow port in domain terms with
adapters for Cloud REST, an in-memory fake, and (later) Data Center. The port deliberately does not
mirror HTTP — for example it exposes `StartSprint` rather than a sprint `PUT`, because the underlying
call nulls omitted fields.

**Registries instead of dispatchers** in the UI: views, commands and keybindings self-register from a
file inside the view's own package. The kernel iterates registrations; it never enumerates views.

**Capabilities as data**: a probe produces a `Capabilities` value with a human-readable reason for
each negative, and views are gated on it rather than discovering 403s at the point of use.

## Consequences

Good:

- Adding a feature is additive — new directory, new registration, no shared file touched. This is
  what makes many parallel agents viable.
- Everything above the adapter is testable with no network, so CI needs no credentials and agents
  need no shared sandbox.
- Instance quirks are contained in one adapter; a second backend is an added file tree, not a rewrite.
- Degradation is designed rather than emergent, because a missing capability has a reason attached.

Costs:

- The port is a shared file and a genuine bottleneck. Extending it needs a small, fast-reviewed PR
  labelled `contract`, and it is frozen after Batch 0 for signature changes.
- Registration via `init()` means import side effects; the kernel needs a blank-import list in one
  place (`internal/ui/views.go`) which is the one file that grows per view. It is a single line each
  and conflicts trivially resolve.
- A layer of mapping code between wire types and domain types that a direct client would not need.

## Alternatives rejected

- **Thin wrapper over an existing Jira Go client.** None model capabilities, and the sprint `PUT`
  footgun is exposed by all of them.
- **Direct HTTP calls from views.** Fastest to start, untestable, and every instance quirk would
  spread across the UI.
- **Central view dispatcher.** Simpler to read, but it is precisely the file that makes parallel work
  painful.
