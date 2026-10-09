package issue

import (
	"context"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Assigner is what an assignment that may name this session's own account
// runs on.
type Assigner interface {
	Editor
	jira.Identifier
}

// Assign gives target to accountID, or to this session's own account when me
// is set, checked against the assignee it was read with. It returns the
// account it went to; an empty id unassigns.
func Assign(ctx context.Context, c Assigner, target jira.Issue, accountID string, me bool) (jira.User, error) {
	who := jira.User{AccountID: accountID}
	if me {
		var err error
		if who, err = c.Me(ctx); err != nil {
			return jira.User{}, err
		}
	}
	id := who.AccountID
	return who, Save(ctx, c, target.Key, BaseOf(target, "assignee"), jira.IssuePatch{Assignee: &id})
}

// SetPriority sets target's priority, checked against the one it was read with.
func SetPriority(ctx context.Context, c Editor, target jira.Issue, priorityID string) error {
	return Save(ctx, c, target.Key, BaseOf(target, "priority"), jira.IssuePatch{PriorityID: &priorityID})
}

// MoveFrom transitions target with no fields, checked against the status it
// was read in.
func MoveFrom(ctx context.Context, c Mover, target jira.Issue, transitionID string) error {
	return Move(ctx, c, target.Key, transitionID, BaseOf(target, "status"), jira.IssuePatch{})
}
