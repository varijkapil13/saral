// Package release is the two screens a project's versions need: the list of
// them, which creates, edits and archives one, and the flow that actually ships
// one.
//
// The two are separate views because releasing is a decision and not a
// keystroke. Jira's own API flips released over the top of whatever is still
// open on a version and reports nothing about it, so the port refuses to be
// asked without a policy and this package refuses to ask without a confirm the
// reader has to answer. The flow is pushed, so the only way into the write is
// through the screen that shows the count.
//
// Nothing here is matched by a name the site can rename. A version is carried
// by the id it was read under, a release decision is carried as
// jira.UnresolvedPolicy, and whether a version is released, archived or neither
// is read off the booleans the port hands over rather than off any word.
package release

// ViewID is the name the versions list registers its keys and its footer slot
// under.
const ViewID = "releases"

// FlowViewID is the name the release flow's keys are registered under. It is
// pushed with a version, which is why it registers no view spec: a registry
// constructor has no version to open over.
const FlowViewID = "release.flow"
