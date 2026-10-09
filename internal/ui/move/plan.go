package move

import (
	"strconv"

	appmove "github.com/varijkapil13/saral/internal/app/move"
	"github.com/varijkapil13/saral/pkg/jira"
)

// halfAnswered is why a set of mandatory fields cannot be submitted, in words.
func halfAnswered(fields []appmove.Pending) (string, bool) {
	at, blocked := appmove.HalfAnswered(fields)
	if !blocked {
		return "", false
	}
	f := &fields[at]
	if !f.Fillable() {
		return "setting " + f.Name() + " is not possible here — this site offered no values " +
			"for it — and naming any other value stops it being kept from the source; leave them all alone " +
			"or move these in the browser", true
	}
	return f.Name() + " has to be given a value too: naming one mandatory field stops every " +
		"other one being kept from the source", true
}

// plan is the move as it is on the confirm screen.
func (m *Model) plan() appmove.Plan {
	return appmove.Plan{
		Issues:  m.issues,
		Target:  m.target,
		TypeID:  m.targetType().ID,
		Targets: m.targetStatuses(),
		Remaps:  m.remaps,
		Fields:  m.fields,
		Notify:  m.notify,
	}
}

func (m *Model) request() jira.MoveRequest {
	p := m.plan()
	return p.Request()
}

// tooMany is why a selection cannot be submitted at all, in a sentence carrying
// both numbers: the count is the fact the answer turns on.
func tooMany(n int) (string, bool) {
	if !appmove.TooMany(n) {
		return "", false
	}
	return "Jira takes " + strconv.Itoa(appmove.MaxKeys) + " issues in one move and this is " +
		strconv.Itoa(n) + "; move them in smaller batches", true
}
