// Package comment is an issue's comment thread: reading and paging it, adding,
// editing and deleting a comment, the drafts kept while one is written, and the
// people lookup behind an @mention.
package comment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// editorOptions is how a document is rendered for somebody to edit, and how the
// edited markdown is read back. The two must be the same value or reconciling
// the edit against the original matches nothing: a block that renders one way
// and parses another reads as a block the author rewrote.
//
// TableWidth is deliberately zero. A width-bounded render truncates a table's
// cells with an ellipsis, and an edit anywhere in that table would write the
// truncation back.
var editorOptions = adf.Options{}

// Load reads the first page of an issue's thread.
func Load(ctx context.Context, r jira.CommentReader, key string) (jira.Page[jira.Comment], error) {
	return r.Comments(ctx, key)
}

// More reads the page after the one in hand.
func More(ctx context.Context, page jira.Page[jira.Comment]) (jira.Page[jira.Comment], error) {
	return page.Next(ctx)
}

// Add writes a new comment and returns it as the site stored it.
func Add(ctx context.Context, w jira.Commenter, key string, body adf.Doc) (jira.Comment, error) {
	return w.AddComment(ctx, key, body)
}

// Edit replaces a comment's body and returns it as the site stored it.
func Edit(ctx context.Context, w jira.Commenter, key, id string, body adf.Doc) (jira.Comment, error) {
	return w.EditComment(ctx, key, id, body)
}

// Delete removes a comment.
func Delete(ctx context.Context, w jira.Commenter, key, id string) error {
	return w.DeleteComment(ctx, key, id)
}

// Markdown is a comment body as somebody edits it.
func Markdown(body adf.Doc) string { return adf.MarkdownWith(body, editorOptions) }

// Compose reads the markdown of a new comment. A new comment has no original to
// reconcile against, so it is parsed on its own.
func Compose(text string) (adf.Doc, error) { return adf.ParseMarkdown(text) }

// Revise reads the markdown of an edit back against the original, reusing the
// original node for every block the author did not touch — the only way an
// account id behind a mention, a lozenge's colour or a node type this client
// has never heard of survives being edited.
func Revise(original adf.Doc, text string) (adf.Doc, error) {
	return adf.ParseMarkdownInto(original, text, editorOptions)
}

// Fingerprint is what an edit's draft is checked against: a body the site has
// since changed has another one.
func Fingerprint(body adf.Doc) string {
	raw, _ := adf.Marshal(body)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:12])
}

// OneWay names the constructs in a document that markdown alone cannot carry,
// read out of pkg/adf so that nothing ends up warning about something the
// parser has since learned to keep.
func OneWay(body adf.Doc) []string {
	losses := adf.LossyConstructs(body, editorOptions)
	seen := make(map[string]bool, len(losses))
	out := make([]string, 0, len(losses))
	for _, l := range losses {
		if !seen[l.Construct] {
			seen[l.Construct] = true
			out = append(out, l.Construct)
		}
	}
	return out
}
