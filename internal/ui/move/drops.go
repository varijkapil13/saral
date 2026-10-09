package move

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	appmove "github.com/varijkapil13/saral/internal/app/move"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

type dropState uint8

const (
	// dropNone is a move with nothing to check: every issue is already in the
	// target project, or no target schema has been asked for yet.
	dropNone dropState = iota
	dropPending
	dropDone
	dropFailed
)

// dropped is one field the move loses as it is drawn.
type dropped struct {
	id    string
	name  string
	count int
}

type droppedMsg struct {
	gen     int
	fields  []dropped
	leaving int
	sources map[appmove.Pair]jira.Schema
	err     error
}

var errNoSourceShape = errors.New("some of these issues arrived without their project or issue type, " +
	"so which fields the move drops could not be checked")

var errOddKey = errors.New("an issue key here is not one JQL can name, so which fields the move drops could not be checked")

func checkDrops(ctx context.Context, client appmove.DropReader, leaving []jira.Issue, target jira.Schema,
	known map[appmove.Pair]jira.Schema, gen int,
) tea.Cmd {
	return func() tea.Msg {
		got, err := appmove.CheckDrops(ctx, client, leaving, target, known)
		switch {
		case errors.Is(err, appmove.ErrNoSourceShape):
			return droppedMsg{gen: gen, err: errNoSourceShape}
		case errors.Is(err, appmove.ErrOddKey):
			return droppedMsg{gen: gen, err: errOddKey}
		case err != nil:
			return droppedMsg{gen: gen, err: err}
		}
		var fields []dropped
		if got.Fields != nil {
			fields = make([]dropped, 0, len(got.Fields))
		}
		for i := range got.Fields {
			d := &got.Fields[i]
			fields = append(fields, dropped{id: d.Meta.Field.ID, name: fieldName(&d.Meta), count: d.Count})
		}
		return droppedMsg{gen: gen, fields: fields, leaving: got.Leaving, sources: got.Sources}
	}
}

// fieldName is a dropped field's name sanitized, because the site's own names
// are drawn.
func fieldName(meta *jira.FieldMeta) string {
	for _, n := range []string{meta.Name, meta.Field.Name} {
		if s := strings.TrimSpace(widget.Sanitize(n)); s != "" {
			return s
		}
	}
	return widget.Sanitize(meta.Field.ID)
}
