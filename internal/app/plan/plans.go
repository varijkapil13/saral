// Package plan reads the site's Advanced Roadmaps plans and the releases they
// draw from, and turns the plans a profile defines into the same shape.
package plan

import (
	"context"
	"errors"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// ReadPlans reads the site's plans.
func ReadPlans(ctx context.Context, reader jira.PlanReader) ([]jira.Plan, error) {
	return reader.Plans(ctx)
}

// PlansRefused reports the refusal that names CapPlans, with the site's own
// reason, which may be empty. It is matched by capability and never by status
// code: a 403 from somewhere else in the chain is not a statement about the
// Plans API.
func PlansRefused(err error) (reason string, refused bool) {
	var capErr *jira.CapabilityError
	if !errors.As(err, &capErr) || capErr.Capability != jira.CapPlans {
		return "", false
	}
	return strings.TrimSpace(capErr.Reason), true
}

// ProjectRefs are the projects a plan draws from, as a version read takes them:
// a key where the profile defined the plan, a numeric id where the site did.
func ProjectRefs(plan *jira.Plan) []string {
	var out []string
	for _, s := range plan.Sources {
		if s.Type == jira.PlanSourceProject && strings.TrimSpace(s.Value) != "" {
			out = append(out, s.Value)
		}
	}
	return out
}

// HasReleaseSources reports whether the plan draws from any project or board,
// which are what releases are read from.
func HasReleaseSources(plan *jira.Plan) bool {
	for _, s := range plan.Sources {
		if (s.Type == jira.PlanSourceProject || s.Type == jira.PlanSourceBoard) && strings.TrimSpace(s.Value) != "" {
			return true
		}
	}
	return false
}
