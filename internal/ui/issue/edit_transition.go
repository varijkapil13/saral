package issue

import (
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// moveField is one required field of a transition screen. A screen field with
// no allowed values is one this pane cannot fill: there is nothing to choose
// from and no way to know what the site would accept.
type moveField struct {
	meta    jira.FieldMeta
	options []jira.Option
	chosen  int
}

func (f *moveField) fillable() bool { return len(f.options) > 0 }

func (f *moveField) value() jira.Option {
	if !f.fillable() {
		return jira.Option{}
	}
	return f.options[f.chosen]
}

func (f *moveField) name() string {
	if strings.TrimSpace(f.meta.Name) != "" {
		return f.meta.Name
	}
	return f.meta.Field.ID
}

// requiredFields is what the transition screen insists on. An optional field is
// left alone: not filling one is a legitimate answer, and guessing a value for
// it is not.
func requiredFields(tr jira.Transition) []moveField {
	out := make([]moveField, 0, len(tr.Fields))
	for i := range tr.Fields {
		meta := tr.Fields[i]
		if !meta.Required {
			continue
		}
		out = append(out, moveField{meta: meta, options: meta.AllowedValues})
	}
	return out
}
