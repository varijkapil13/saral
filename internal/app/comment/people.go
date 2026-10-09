package comment

import (
	"context"

	"github.com/varijkapil13/saral/pkg/jira"
)

// peopleLimit is how many accounts one @mention lookup asks for.
const peopleLimit = 8

// People finds the accounts whose names match what was typed after an @,
// scoped to a project when one is named.
func People(ctx context.Context, f jira.PeopleFinder, match, project string) ([]jira.User, error) {
	return f.FindPeople(ctx, jira.PeopleQuery{Match: match, Project: project, Limit: peopleLimit})
}
