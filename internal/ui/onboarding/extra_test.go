package onboarding

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestProfileName_IsDerivedFromTheSiteAndAlwaysABareKey(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"example.atlassian.net":     "example",
		"ACME-Corp.atlassian.net":   "acme-corp",
		"jira.internal.example.com": "jira",
		"...":                       "default",
	}

	for site, want := range tests {
		t.Run(site, func(t *testing.T) {
			t.Parallel()

			if got := profileNameFor(site); got != want {
				t.Errorf("profileNameFor(%q) = %q, want %q", site, got, want)
			}
		})
	}
}

func TestHostOf_DropsThePathPeopleActuallyCopy(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"https://example.atlassian.net/jira/software/projects/PROJ/boards/1": "https://example.atlassian.net",
		"example.atlassian.net/browse/PROJ-1":                                "example.atlassian.net",
		"https://example.atlassian.net":                                      "https://example.atlassian.net",
		"example.atlassian.net":                                              "example.atlassian.net",
		"example.atlassian.net?a=b":                                          "example.atlassian.net",
	}

	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			if got := hostOf(raw); got != want {
				t.Errorf("hostOf(%q) = %q, want %q", raw, got, want)
			}
		})
	}
}

// TestStaleAnswersAreDropped covers the case a slow site produces: the user
// went back and started again, and the first answer lands afterwards.
func TestStaleAnswersAreDropped(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.credentials()
	d.atStep(stepStorage)

	d.send(connectFailedMsg{seq: d.model().seq - 1, err: errNotReached})
	d.atStep(stepStorage)
	if got := d.model().problem; got != "" {
		t.Errorf("an answer to an older question was shown: %q", got)
	}
}

var errNotReached = &staleError{}

type staleError struct{}

func (*staleError) Error() string { return "this should never be shown" }

func TestStoreKind_EveryChoiceExplainsItselfAndNamesItsField(t *testing.T) {
	t.Parallel()

	for kind := storeKind(0); kind < storeCount; kind++ {
		if kind.title() == "" || kind.label() == "" || kind.explain() == "" {
			t.Errorf("%v is missing a title, a label or an explanation", kind)
		}
		if !strings.Contains(kind.explain(), "Saral") {
			t.Errorf("%v does not say what Saral itself does: %q", kind, kind.explain())
		}
	}
}

func TestResize_KeepsTheFieldsInsideTheBox(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	for _, w := range []int{80, 120, 200, 40} {
		d.send(kernel.SizeMsg{Width: w, Height: 30})
		if got := d.model().input[fieldSite].Width(); got < 8 || got > w {
			t.Errorf("at width %d the site field is %d wide", w, got)
		}
	}
}
