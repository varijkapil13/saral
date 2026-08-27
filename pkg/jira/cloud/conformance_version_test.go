package cloud

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// The release rules, run against both adapters through jira.Releaser. The whole
// release flow is tested against the fake, so a rule only the cloud adapter
// keeps is a rule the flow never meets: it would refuse a release the fake let
// through, on a site, with the version already shipped.
//
// The cases are properties rather than answers. The two adapters describe
// different sites — three versions of a fixture project against three the fake
// mints — so what can be asserted of both is what the port actually promises.

// releaser opens one adapter, and names the version the case acts on: the ids
// are each site's own and there is no id both adapters have.
type releaser func(*testing.T) (jira.Releaser, string)

func releaserFromSite(t *testing.T, opts ...jiratest.ServerOption) (client jira.Releaser, version string) {
	t.Helper()

	// The count and the sweep's search are made to agree about what is open, the
	// way the fake's two answers agree by construction. A site whose count and
	// search disagree is its own case, in version_test.go.
	c, _ := versionClient(t, append(sweeping(versionOpenKeys()...), opts...)...)
	return c, testVersionID
}

// releaserFake opens the fake on the version it has not released yet, which is
// the only kind a release case can act on.
func releaserFake(t *testing.T, opts ...jiratest.Option) (client jira.Releaser, version string) {
	t.Helper()

	f := conformFake(t, opts...)
	versions, err := f.Versions(t.Context(), conformProject)
	if err != nil {
		t.Fatalf("reading the fake's versions: %v", err)
	}
	for i := range versions {
		if !versions[i].Released {
			return f, versions[i].ID
		}
	}
	t.Fatal("the fake has no unreleased version to release")
	return nil, ""
}

// releaserFakeCarrying opens the fake twice: once to learn which version it has
// not released, and again with one issue seeded onto that version. The fake
// derives its version ids from the project key, so the second one mints the same
// ids as the first.
func releaserFakeCarrying(t *testing.T, issue func(version string) jira.Issue) (client jira.Releaser, version string) {
	t.Helper()

	_, version = releaserFake(t)
	return conformFake(t, jiratest.WithIssues([]jira.Issue{issue(version)})), version
}

// openIssueOn is an issue nothing has resolved, in a status category that says
// so too — open by either measure, so both adapters have to count it.
func openIssueOn(version string) jira.Issue {
	return jira.Issue{
		ID: "90001", Key: conformProject + "-9001",
		Project:     jira.ProjectRef{Key: conformProject},
		Summary:     "Still being worked on",
		Status:      jira.Status{ID: "10201", Name: "Doing", Category: jira.CategoryInProgress},
		FixVersions: []jira.Version{{ID: version}},
	}
}

// doneButUnresolvedOn is an issue in the done category that nobody resolved,
// which docs/API-NOTES.md records as a real shape: the two measures disagree.
func doneButUnresolvedOn(version string) jira.Issue {
	return jira.Issue{
		ID: "90002", Key: conformProject + "-9002",
		Project:     jira.ProjectRef{Key: conformProject},
		Summary:     "Closed by somebody who set no resolution",
		Status:      jira.Status{ID: "10203", Name: "Shipped", Category: jira.CategoryDone},
		FixVersions: []jira.Version{{ID: version}},
	}
}

func bothReleasers(t *testing.T, cloud, fake releaser, run func(*testing.T, jira.Releaser, string)) {
	t.Helper()

	for _, adapter := range []struct {
		name string
		open releaser
	}{
		{name: "cloud", open: cloud},
		{name: "fake", open: fake},
	} {
		t.Run(adapter.name, func(t *testing.T) {
			t.Parallel()

			client, version := adapter.open(t)
			run(t, client, version)
		})
	}
}

func TestVersionList_BothAdaptersLeaveTheCountToWhoeverAsksForIt(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, _ string) {
			got, err := client.Versions(t.Context(), conformProject)
			if err != nil {
				t.Fatalf("listing versions: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("a project with versions came back with none")
			}
			for _, v := range got {
				if v.ID == "" || v.Name == "" {
					t.Errorf("a version came back as %+v; a picker needs an id and something to draw", v)
				}
				if v.Unresolved != nil {
					t.Errorf("version %s arrived with a count of %d nobody asked for; nil is how the port says nobody did, and a number here is a release gate somebody did not pass",
						v.ID, *v.Unresolved)
				}
			}
		})
}

func TestUnresolvedCount_BothAdaptersNameAVersionThatIsNotThere(t *testing.T) {
	t.Parallel()

	const missingID = "40404"
	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) {
			client, _ := releaserFromSite(t, jiratest.WithStatus(http.MethodGet, unresolvedRoute,
				http.StatusNotFound, "problem_no_endpoint.json"))
			return client, missingID
		},
		func(t *testing.T) (jira.Releaser, string) {
			client, _ := releaserFake(t)
			return client, missingID
		},
		func(t *testing.T, client jira.Releaser, version string) {
			got, err := client.UnresolvedCount(t.Context(), version)
			var missing *jira.NotFoundError
			if !errors.As(err, &missing) {
				t.Fatalf("got %d and %T (%v), want a *jira.NotFoundError", got, err, err)
			}
			if missing.Kind != "version" || missing.ID != version {
				t.Errorf("the failure names %s %s rather than the version asked about", missing.Kind, missing.ID)
			}
			if got != 0 {
				t.Errorf("a failed count came back with %d", got)
			}
		})
}

func TestUnresolvedCount_BothAdaptersLeaveOpenExactlyWhatTheyCounted(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFakeCarrying(t, openIssueOn) },
		func(t *testing.T, client jira.Releaser, version string) {
			open, err := client.UnresolvedCount(t.Context(), version)
			if err != nil {
				t.Fatalf("counting what is open: %v", err)
			}
			if open < 1 {
				t.Fatalf("a version with an unresolved issue on it counted %d, so this case proves nothing", open)
			}
			got, err := client.ReleaseVersion(t.Context(), version, jira.ReleaseInput{Unresolved: jira.ReleaseAnyway})
			if err != nil {
				t.Fatalf("releasing anyway: %v", err)
			}
			if got.Unresolved == nil || *got.Unresolved != open {
				t.Errorf("a release anyway reports %v open issues left on the version, want the %d it counted before releasing",
					got.Unresolved, open)
			}
		})
}

// TestUnresolvedCount_BothAdaptersCountAnIssueWithNoResolution FAILS ON THE FAKE,
// deliberately, and pkg/jira/jiratest is not this packet's to edit.
//
// Open means unresolved: an issue can sit in the done category with no
// resolution, and only the count gates a release. The cloud adapter forwards the
// site's unresolvedIssueCount, which counts that way, and sweeps
// `resolution IS EMPTY`; jiratest.Fake counts by status category, so it reports
// nothing open on a version a real site would refuse to release.
func TestUnresolvedCount_BothAdaptersCountAnIssueWithNoResolution(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) {
			return releaserFromSite(t, jiratest.WithHandler(http.MethodGet, unresolvedRoute, unresolvedAnswering(1)))
		},
		func(t *testing.T) (jira.Releaser, string) { return releaserFakeCarrying(t, doneButUnresolvedOn) },
		func(t *testing.T, client jira.Releaser, version string) {
			got, err := client.UnresolvedCount(t.Context(), version)
			if err != nil {
				t.Fatalf("counting what is open: %v", err)
			}
			if got < 1 {
				t.Errorf("a version carrying an issue with no resolution counted %d open; the status category is a different question, and only the count gates a release",
					got)
			}
		})
}

func TestSaveVersion_BothAdaptersRefuseAVersionWithNoName(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, _ string) {
			got, err := client.SaveVersion(t.Context(), jira.VersionInput{ProjectKey: conformProject, Name: "  "})
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %+v and %T (%v), want a *jira.ValidationError", got, err, err)
			}
			if _, named := invalid.For("name"); !named {
				t.Errorf("the refusal does not name the field a form would annotate: %v", invalid)
			}
		})
}

func TestSaveVersion_BothAdaptersReleaseNothingThroughASave(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, version string) {
			got, err := client.SaveVersion(t.Context(), jira.VersionInput{ID: version, Name: "renamed"})
			if err != nil {
				t.Fatalf("renaming a version: %v", err)
			}
			if got.Released {
				t.Error("a save released the version; releasing is the one thing a save cannot do, because it cannot be told what happens to the open issues")
			}
		})
}

func TestReleaseVersion_BothAdaptersRefuseAMoveWithNowhereToMoveTo(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		input func(version string) jira.ReleaseInput
	}{
		{
			name: "no version named at all",
			input: func(string) jira.ReleaseInput {
				return jira.ReleaseInput{Unresolved: jira.MoveUnresolved}
			},
		},
		{
			name: "the version being released",
			input: func(version string) jira.ReleaseInput {
				return jira.ReleaseInput{Unresolved: jira.MoveUnresolved, MoveToVersionID: version}
			},
		},
		{
			name: "a version the site does not have",
			input: func(string) jira.ReleaseInput {
				return jira.ReleaseInput{Unresolved: jira.MoveUnresolved, MoveToVersionID: "40404"}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bothReleasers(t,
				func(t *testing.T) (jira.Releaser, string) {
					return releaserFromSite(t, jiratest.WithStatus(http.MethodGet, versionRoute,
						http.StatusNotFound, "problem_no_endpoint.json"))
				},
				func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
				func(t *testing.T, client jira.Releaser, version string) {
					got, err := client.ReleaseVersion(t.Context(), version, tt.input(version))
					if err == nil {
						t.Fatalf("a move with nothing to move to released %s anyway", version)
					}
					if got.Released {
						t.Errorf("a refused release still came back released: %+v", got)
					}
				})
		})
	}
}

func TestReleaseVersion_BothAdaptersDateAReleaseNobodyDated(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, version string) {
			got, err := client.ReleaseVersion(t.Context(), version, jira.ReleaseInput{Unresolved: jira.ReleaseAnyway})
			if err != nil {
				t.Fatalf("releasing: %v", err)
			}
			if !got.Released {
				t.Error("a released version came back unreleased")
			}
			if got.ReleaseDate.IsZero() {
				t.Error("a release nobody dated came back with no date; a version shipped today is dated today")
			}
			if got.Unresolved == nil {
				t.Error("a release came back without saying what it left open, which is the number the release was decided on")
			}
		})
}

func TestReleaseVersion_BothAdaptersTakeTheDateTheyWereGiven(t *testing.T) {
	t.Parallel()

	day := jira.Date{Year: 2026, Month: 2, Day: 28}
	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, version string) {
			got, err := client.ReleaseVersion(t.Context(), version,
				jira.ReleaseInput{Unresolved: jira.ReleaseAnyway, ReleaseDate: day})
			if err != nil {
				t.Fatalf("releasing: %v", err)
			}
			if got.ReleaseDate != day {
				t.Errorf("the version came back dated %v, want the %v it was given", got.ReleaseDate, day)
			}
		})
}

func TestReleaseVersion_BothAdaptersEmptyAVersionTheyStrip(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, version string) {
			got, err := client.ReleaseVersion(t.Context(), version, jira.ReleaseInput{Unresolved: jira.StripUnresolved})
			if err != nil {
				t.Fatalf("releasing: %v", err)
			}
			if got.Unresolved == nil || *got.Unresolved != 0 {
				t.Errorf("a release that stripped the version reports %v still open on it", got.Unresolved)
			}
			if !got.Released {
				t.Error("a released version came back unreleased")
			}
		})
}

// TestReleaseVersion_BothAdaptersRefuseAPolicyNeitherKnows FAILS ON THE FAKE,
// deliberately, and pkg/jira/jiratest is not this packet's to edit.
//
// jira.ReleaseAnyway is the zero value of UnresolvedPolicy, so a policy an
// adapter does not recognise must be refused rather than fallen through: the
// alternative is releasing over the open issues by default. The cloud adapter
// refuses; the fake's switch has no default and releases the version.
func TestReleaseVersion_BothAdaptersRefuseAPolicyNeitherKnows(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFakeCarrying(t, openIssueOn) },
		func(t *testing.T, client jira.Releaser, version string) {
			got, err := client.ReleaseVersion(t.Context(), version,
				jira.ReleaseInput{Unresolved: jira.UnresolvedPolicy(42)})
			var invalid *jira.ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("got %+v and %T (%v), want a *jira.ValidationError naming unresolved", got, err, err)
			}
			if _, named := invalid.For("unresolved"); !named {
				t.Errorf("the refusal does not name the field: %v", invalid)
			}
			if got.Released {
				t.Errorf("a policy the adapter does not know released the version anyway: %+v", got)
			}
		})
}

// conformCancelled is the one property that has nothing to do with versions: a
// caller that has gone away is told so, in its own words, by both adapters.
func TestReleaseVersion_BothAdaptersReturnTheCallersOwnCancellation(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, version string) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := client.ReleaseVersion(ctx, version, jira.ReleaseInput{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v, want the context's own error", err)
			}
		})
}

// A version id is a number on every Jira site, so the shape is a property of the
// port and not of one adapter: ReleaseVersion's sweep names the version in JQL,
// where a non-numeric fixVersion matches version *names* instead, so the cloud
// adapter refuses one outright. While the fake minted ids like ver-EX-1 that
// refusal could not be reached through it, and a view written against the fake's
// ids passed here and was turned down on a site.
func TestVersionList_BothAdaptersNameAVersionWithAnIdASiteCouldHaveMinted(t *testing.T) {
	t.Parallel()

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, version string) {
			if !isNumeric(version) {
				t.Errorf("the version this adapter offers to release is %q, which ReleaseVersion refuses", version)
			}
			got, err := client.Versions(t.Context(), conformProject)
			if err != nil {
				t.Fatalf("listing versions: %v", err)
			}
			if len(got) == 0 {
				t.Fatal("a project with versions came back with none")
			}
			for _, v := range got {
				if !isNumeric(v.ID) {
					t.Errorf("version %q (%s) is named by something no site could have minted, so it is a version this adapter will not release",
						v.ID, v.Name)
				}
			}
		})
}

// And handed one anyway, neither adapter releases anything. The cloud adapter
// refuses before it asks the site anything, which is the branch that stops the
// sweep from matching a name; the fake has no version by that id.
func TestReleaseVersion_NeitherAdapterReleasesAnIdNoSiteCouldHaveMinted(t *testing.T) {
	t.Parallel()

	// The shape the fake used to mint, which is how this arrived above the port.
	const notAnID = "ver-" + conformProject + "-1"

	bothReleasers(t,
		func(t *testing.T) (jira.Releaser, string) { return releaserFromSite(t) },
		func(t *testing.T) (jira.Releaser, string) { return releaserFake(t) },
		func(t *testing.T, client jira.Releaser, _ string) {
			got, err := client.ReleaseVersion(t.Context(), notAnID, releaseInput(jira.StripUnresolved, ""))
			if err == nil {
				t.Fatalf("releasing %q answered %+v and no error", notAnID, got)
			}
			if got.Released {
				t.Errorf("a refused release came back released: %+v", got)
			}
		})
}
