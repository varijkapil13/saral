package main

import (
	"fmt"
	"strings"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// A URL for another site is named rather than opened: the same key on this
// profile's site may be somebody else's issue.
func argument(arg, site string) (opts []kernel.Option, notice string, err error) {
	if key, ok := app.ParseKey(arg); ok {
		return []kernel.Option{openIssue(key)}, "", nil
	}
	if key, host, ok := app.ParseIssueURL(arg); ok {
		here, serr := config.NormalizeSite(site)
		if serr == nil && !strings.EqualFold(here, host) {
			return nil, fmt.Sprintf("%s is on %s and this profile is on %s, so it was not opened", key, host, here), nil
		}
		return []kernel.Option{openIssue(key)}, "", nil
	}
	if id, ok := viewNamed(arg); ok {
		return []kernel.Option{kernel.WithInitialView(id)}, "", nil
	}
	msg := fmt.Sprintf("%q is not an issue key, a Jira URL, or a view (%s)", arg, strings.Join(openableViewIDs(), ", "))
	if near, ok := closestView(arg); ok {
		msg += fmt.Sprintf("; did you mean %q?", near)
	}
	return nil, "", usageErrorf("%s", msg)
}

func openIssue(key string) kernel.Option {
	return kernel.WithInitialPush(issue.ViewID, key, func(d kernel.Deps) kernel.View {
		return issue.New(d, jira.Issue{Key: key})
	})
}

// The views off a footer slot, bar settings, are pushed by something else.
func openable(spec kernel.ViewSpec) bool {
	return spec.Slot > 0 || spec.ID == kernel.SettingsViewID
}

func openableViews() []kernel.ViewSpec {
	var out []kernel.ViewSpec
	for _, spec := range kernel.Views() {
		if openable(spec) {
			out = append(out, spec)
		}
	}
	return out
}

func openableViewIDs() []string {
	specs := openableViews()
	ids := make([]string, len(specs))
	for i := range specs {
		ids[i] = specs[i].ID
	}
	return ids
}

func viewNamed(name string) (string, bool) {
	if _, ok := kernel.LookupView(name); ok {
		return name, true
	}
	for _, spec := range openableViews() {
		if strings.EqualFold(spec.ID, name) || strings.EqualFold(spec.Title, name) {
			return spec.ID, true
		}
	}
	return "", false
}

func closestView(typed string) (string, bool) {
	typed = strings.ToLower(typed)
	best, bestDist := "", -1
	for _, spec := range openableViews() {
		for _, name := range []string{spec.ID, strings.ToLower(spec.Title)} {
			if d := editDistance(typed, name); bestDist < 0 || d < bestDist {
				best, bestDist = spec.ID, d
			}
		}
	}
	limit := max(2, len([]rune(typed))/3)
	return best, bestDist >= 0 && bestDist <= limit
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func tooManyArguments(words []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "saral opens one thing, and %q is not a command", words[0])
	for _, w := range words {
		if key, ok := app.ParseKey(w); ok {
			fmt.Fprintf(&b, "; to open %s, run: saral %s", key, key)
			break
		}
	}
	fmt.Fprintf(&b, ". Commands: %s. To scope a session to a project, use --project", subcommandList())
	return usageErrorf("%s", b.String())
}
