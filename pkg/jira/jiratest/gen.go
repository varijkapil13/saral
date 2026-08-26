package jiratest

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const fakeBaseURL = "https://jira.example.invalid"

// fakeEpoch is the instant every fake starts on and the one Gen derives its
// timestamps from, so that generated data does not move when the day does.
var fakeEpoch = time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)

var fakeGenBase = fakeEpoch.AddDate(-1, 0, 0)

// The catalogues below deliberately avoid the names a stock Jira site ships
// with: code that hardcodes "In Progress" or a customfield_10016 must fail
// against the fake rather than in front of a user.
var fakeStatuses = []jira.Status{
	{ID: "10201", Name: "Triage", Category: jira.CategoryToDo},
	{ID: "10202", Name: "Building", Category: jira.CategoryInProgress},
	{ID: "10203", Name: "Shipped", Category: jira.CategoryDone},
}

// fakeSubtaskStatuses is the workflow the subtask type runs, which is not the
// one the other types run. Its middle status shares a display name with 10202
// and has a different id: that is what a team-managed project mints on a real
// site, and it is why an answer about statuses carries ids rather than names.
var fakeSubtaskStatuses = []jira.Status{
	{ID: "10201", Name: "Triage", Category: jira.CategoryToDo},
	{ID: "10204", Name: "Building", Category: jira.CategoryInProgress},
	{ID: "10203", Name: "Shipped", Category: jira.CategoryDone},
}

var fakeIssueTypes = []jira.IssueType{
	{ID: "10301", Name: "Story", IconURL: fakeBaseURL + "/icons/story.png"},
	{ID: "10302", Name: "Defect", IconURL: fakeBaseURL + "/icons/defect.png"},
	{ID: "10303", Name: "Chore", IconURL: fakeBaseURL + "/icons/chore.png"},
	{ID: "10304", Name: "Epic", IconURL: fakeBaseURL + "/icons/epic.png"},
	{ID: "10305", Name: "Offshoot", Subtask: true, IconURL: fakeBaseURL + "/icons/offshoot.png"},
}

var fakePriorities = []jira.Priority{
	{ID: "10401", Name: "Urgent"},
	{ID: "10402", Name: "Normal"},
	{ID: "10403", Name: "Whenever"},
}

var fakeResolutions = []jira.Resolution{
	{ID: "10501", Name: "Delivered"},
	{ID: "10502", Name: "Won't do"},
}

// fakeUsers are the accounts the generated issues are assigned to and reported
// by. An issue read states the kind of account it names, so these carry one and
// have to agree with the directory below about the accounts that are in both.
var fakeUsers = []jira.User{
	{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Active: true, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/ada", Kind: jira.AccountPerson},
	{AccountID: "acct-grace", DisplayName: "Grace Hopper", Active: true, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/grace", Kind: jira.AccountPerson},
	{AccountID: "acct-alan", DisplayName: "Alan Turing", Active: false, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/alan", Kind: jira.AccountPerson},
}

var fakeDefaultMe = jira.User{
	AccountID:   "acct-me",
	DisplayName: "Sam Tester",
	Email:       "sam@example.invalid",
	TimeZone:    time.UTC,
	Active:      true,
	AvatarURL:   fakeBaseURL + "/avatar/me",
	Kind:        jira.AccountPerson,
}

// fakePeople is the fake site's account directory, and deliberately a longer
// list than fakeUsers: a site holds accounts that no generated issue names, and
// an assignment to one of them answers with what the directory knows about it.
//
// It holds an account that is not a person because a real site does — on the one
// this was measured against, ten of the eleven accounts were apps — and one whose
// id contains a colon, because two of those eleven did and anything that splits
// an id on a separator or builds a JQL clause out of one has to survive it.
var fakePeople = []jira.User{
	{AccountID: "acct-ada", DisplayName: "Ada Lovelace", Email: "ada@example.invalid", Active: true, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/ada", Kind: jira.AccountPerson},
	{AccountID: "acct-alan", DisplayName: "Alan Turing", Active: false, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/alan", Kind: jira.AccountPerson},
	{AccountID: "acct-grace", DisplayName: "Grace Hopper", Email: "grace@example.invalid", Active: true, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/grace", Kind: jira.AccountPerson},
	{AccountID: "acct-me", DisplayName: "Sam Tester", Email: "sam@example.invalid", Active: true, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/me", Kind: jira.AccountPerson},
	{AccountID: "acct:nightly-bot", DisplayName: "Nightly Runner", Active: true, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/bot", Kind: jira.AccountApp},
	{AccountID: "acct-reporter", DisplayName: "Rex Outside", Email: "rex@example.invalid", Active: true, TimeZone: time.UTC, AvatarURL: fakeBaseURL + "/avatar/rex", Kind: jira.AccountCustomer},
}

// fakeSiteLabelPool is what Labels answers from, over and above whatever the
// stored issues carry. Two of them are not ASCII: a label is whatever anybody
// typed, and a column width taken with len() over one is wrong.
var fakeSiteLabelPool = []string{
	"backend", "frontend", "infra", "tech-debt", "customer", "flaky",
	"prüfung", "検索",
}

var fakeDefaultFields = []jira.Field{
	fakeSystemField("summary", "Summary", jira.FieldSchema{Type: "string", System: "summary"}),
	fakeSystemField("description", "Description", jira.FieldSchema{Type: "doc", System: "description"}),
	fakeSystemField("issuetype", "Issue Type", jira.FieldSchema{Type: "issuetype", System: "issuetype"}),
	fakeSystemField("project", "Project", jira.FieldSchema{Type: "project", System: "project"}),
	fakeSystemField("status", "Status", jira.FieldSchema{Type: "status", System: "status"}),
	fakeSystemField("priority", "Priority", jira.FieldSchema{Type: "priority", System: "priority"}),
	fakeSystemField("resolution", "Resolution", jira.FieldSchema{Type: "resolution", System: "resolution"}),
	fakeSystemField("assignee", "Assignee", jira.FieldSchema{Type: "user", System: "assignee"}),
	fakeSystemField("reporter", "Reporter", jira.FieldSchema{Type: "user", System: "reporter"}),
	fakeSystemField("labels", "Labels", jira.FieldSchema{Type: "array", Items: "string", System: "labels"}),
	fakeSystemField("duedate", "Due Date", jira.FieldSchema{Type: "date", System: "duedate"}),
	fakeSystemField("created", "Created", jira.FieldSchema{Type: "datetime", System: "created"}),
	fakeSystemField("updated", "Updated", jira.FieldSchema{Type: "datetime", System: "updated"}),
	fakeSystemField("parent", "Parent", jira.FieldSchema{Type: "issuelink", System: "parent"}),
	fakeSystemField("fixVersions", "Fix versions", jira.FieldSchema{Type: "array", Items: "version", System: "fixVersions"}),
	fakeCustomField(13401, "Story Points", jira.FieldSchema{Type: "number", Custom: "com.atlassian.jira.plugin.system.customfieldtypes:float"}),
	fakeCustomField(13402, "Sprint", jira.FieldSchema{Type: "array", Items: "json", Custom: "com.pyxis.greenhopper.jira:gh-sprint"}),
	fakeCustomField(13403, "Epic Link", jira.FieldSchema{Type: "any", Custom: "com.pyxis.greenhopper.jira:gh-epic-link"}),
	fakeCustomField(13404, "Rank", jira.FieldSchema{Type: "any", Custom: "com.pyxis.greenhopper.jira:gh-lexo-rank"}),
	fakeCustomField(13405, "Target start", jira.FieldSchema{Type: "date", Custom: "com.atlassian.jpo:jpo-custom-field-baseline-start"}),
	fakeCustomField(13406, "Target end", jira.FieldSchema{Type: "date", Custom: "com.atlassian.jpo:jpo-custom-field-baseline-end"}),
}

var fakeSummaryVerbs = []string{"Fix", "Add", "Rework", "Investigate", "Document", "Retire", "Speed up"}

var fakeSummaryNouns = []string{
	"the login flow",
	"the nightly export",
	"webhook retries",
	"the onboarding wizard",
	"CSV import",
	"the audit trail",
	"session expiry",
	"the search index",
	"invoice rounding",
	"the mobile layout",
	"the release checklist",
}

var fakeDetails = []string{
	"Reproduced on staging twice in a row.",
	"Only happens for accounts created before the migration.",
	"The old ticket for this was closed as cannot reproduce.",
	"Needs a decision on the wording before it can ship.",
	"Small change, but it touches the shared client.",
}

var fakeLabelPool = []string{"backend", "frontend", "infra", "tech-debt", "customer", "flaky"}

func fakeSystemField(id, name string, schema jira.FieldSchema) jira.Field {
	return jira.Field{
		ID: id, Key: id, Name: name,
		Navigable: true, Searchable: true, Orderable: true,
		ClauseNames: []string{id},
		Schema:      schema,
	}
}

func fakeCustomField(customID int, name string, schema jira.FieldSchema) jira.Field {
	id := "customfield_" + strconv.Itoa(customID)
	schema.CustomID = customID
	return jira.Field{
		ID: id, Key: id, Name: name, Custom: true,
		Navigable: true, Searchable: true, Orderable: true,
		ClauseNames: []string{id, name},
		Schema:      schema,
	}
}

func fakeHash32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// fakeProjectID derives a project's ID from its key so that Gen and
// WithProject agree on it without having to be called in a particular order.
func fakeProjectID(key string) string {
	return strconv.Itoa(10000 + int(fakeHash32(strings.ToUpper(key))%9000))
}

func fakeBoardID(key string) int64 {
	return int64(100 + fakeHash32("board:"+strings.ToUpper(key))%900)
}

func fakeProjectRef(key string) jira.ProjectRef {
	return jira.ProjectRef{ID: fakeProjectID(key), Key: key, Name: key + " Project"}
}

// fakeVersionsFor is the version set every project gets, built from the key
// alone so that an issue generated by Gen carries the same version values the
// store holds.
func fakeVersionsFor(projectKey string) []jira.Version {
	pid := fakeProjectID(projectKey)
	up := strings.ToUpper(projectKey)
	return []jira.Version{
		{
			ID: "ver-" + up + "-0", ProjectID: pid, Name: "1.0",
			Description: "The first release", Released: true,
			StartDate:   jira.DateOf(fakeEpoch.AddDate(0, -4, 0)),
			ReleaseDate: jira.DateOf(fakeEpoch.AddDate(0, -2, 0)),
		},
		{
			ID: "ver-" + up + "-1", ProjectID: pid, Name: "2.0",
			Description: "The one being worked on",
			StartDate:   jira.DateOf(fakeEpoch.AddDate(0, -1, 0)),
			ReleaseDate: jira.DateOf(fakeEpoch.AddDate(0, 1, 0)),
		},
		{
			ID: "ver-" + up + "-2", ProjectID: pid, Name: "3.0",
			Description: "Not planned yet",
		},
	}
}

func fakeRefByName(fields []jira.Field, name string) (jira.FieldRef, bool) {
	f, ok := jira.FieldByName(fields, name)
	if !ok {
		return jira.FieldRef{}, false
	}
	return f.Ref(), true
}

// Gen builds n issues in project PROJ. The result is identical on every run,
// which is what lets golden files and race tests depend on it.
func Gen(n int) []jira.Issue { return GenFor("PROJ", n) }

// GenFor builds n issues in the named project, keyed KEY-1 to KEY-n. The issues
// rotate through every status category, issue type and priority, leave some
// unassigned, hang some off an epic and carry an ADF description, so that a
// view rendered against them looks like a view rendered against a real site.
func GenFor(projectKey string, n int) []jira.Issue {
	if n <= 0 {
		return nil
	}
	proj := fakeProjectRef(projectKey)
	versions := fakeVersionsFor(projectKey)
	points, hasPoints := fakeRefByName(fakeDefaultFields, "Story Points")
	rank, hasRank := fakeRefByName(fakeDefaultFields, "Rank")

	out := make([]jira.Issue, 0, n)
	for i := 1; i <= n; i++ {
		summary := fakeSummary(i)
		created := fakeGenBase.Add(time.Duration(i) * time.Hour)
		updated := created.Add(time.Duration(i%17+1) * time.Hour)
		status := fakeStatuses[i%len(fakeStatuses)]

		iss := jira.Issue{
			ID:          strconv.Itoa(20000 + i),
			Key:         fmt.Sprintf("%s-%d", projectKey, i),
			Project:     proj,
			Summary:     summary,
			Description: fakeDescription(i, fmt.Sprintf("%s-%d", projectKey, i), summary),
			Type:        fakeGenType(i),
			Status:      status,
			Labels:      fakeGenLabels(i),
			Created:     created,
			Updated:     updated,
		}
		reporter := fakeUsers[(i+1)%len(fakeUsers)]
		iss.Reporter = &reporter
		if i%4 != 0 {
			assignee := fakeUsers[i%len(fakeUsers)]
			iss.Assignee = &assignee
		}
		if i%13 != 0 {
			priority := fakePriorities[i%len(fakePriorities)]
			iss.Priority = &priority
		}
		if status.Category == jira.CategoryDone {
			resolved := updated
			iss.Resolved = &resolved
			resolution := fakeResolutions[i%len(fakeResolutions)]
			iss.Resolution = &resolution
		}
		if i%6 == 0 {
			iss.Due = jira.DateOf(created.AddDate(0, 0, 14))
		}
		switch i % 4 {
		case 0:
			iss.FixVersions = []jira.Version{versions[1]}
		case 2:
			iss.FixVersions = []jira.Version{versions[0]}
		}
		if parent, ok := fakeGenParent(i, projectKey); ok {
			iss.Parent = &parent
		}
		if i%9 == 0 {
			iss.TimeTracking = &jira.TimeTracking{
				OriginalEstimate:  int64(3600 * (i%5 + 1)),
				RemainingEstimate: int64(1800 * (i%5 + 1)),
				TimeSpent:         int64(1800 * (i%3 + 1)),
			}
		}
		var fs jira.FieldSet
		if hasPoints && i%3 == 1 {
			fs = fs.With(points, jira.FieldValue{Kind: jira.KindNumber, Number: float64(i%8 + 1)})
		}
		if hasRank {
			fs = fs.With(rank, jira.FieldValue{Kind: jira.KindText, Text: fmt.Sprintf("0|i%05d:", i)})
		}
		iss.Fields = fs
		out = append(out, iss)
	}
	return out
}

func fakeSummary(i int) string {
	return fakeSummaryVerbs[i%len(fakeSummaryVerbs)] + " " + fakeSummaryNouns[i%len(fakeSummaryNouns)]
}

func fakeGenType(i int) jira.IssueType {
	if i%10 == 1 {
		return fakeIssueTypes[3]
	}
	return fakeIssueTypes[i%3]
}

// fakeGenParent hangs every fifth issue off the nearest epic before it, which
// is always issue 1, 11, 21 and so on.
func fakeGenParent(i int, projectKey string) (jira.IssueRef, bool) {
	if i%10 == 1 || i%5 != 0 {
		return jira.IssueRef{}, false
	}
	e := ((i-1)/10)*10 + 1
	return jira.IssueRef{
		ID:      strconv.Itoa(20000 + e),
		Key:     fmt.Sprintf("%s-%d", projectKey, e),
		Summary: fakeSummary(e),
		Status:  fakeStatuses[e%len(fakeStatuses)],
		Type:    fakeIssueTypes[3],
	}, true
}

func fakeGenLabels(i int) []string {
	switch i % 3 {
	case 1:
		return []string{fakeLabelPool[i%len(fakeLabelPool)]}
	case 2:
		return []string{fakeLabelPool[i%len(fakeLabelPool)], fakeLabelPool[(i+2)%len(fakeLabelPool)]}
	default:
		return nil
	}
}

func fakeDescription(i int, key, summary string) adf.Doc {
	bullets := []adf.Node{
		adf.NewNode("listItem", adf.NewNode("paragraph", adf.NewText("Filed against "+key+"."))),
		adf.NewNode("listItem", adf.NewNode("paragraph", adf.NewText(fakeDetails[i%len(fakeDetails)]))),
	}
	if i%4 == 0 {
		bullets = append(bullets, adf.NewNode("listItem", adf.NewNode("paragraph", adf.NewText("Waiting on a decision."))))
	}
	return adf.NewDoc(
		adf.NewNode("paragraph", adf.NewText(summary+".")),
		adf.NewNode("bulletList", bullets...),
	)
}
