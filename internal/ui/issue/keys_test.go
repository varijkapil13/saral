package issue

import (
	"fmt"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// TestLiveKeys_EveryStateGolden holds every stage of the two panes the detail
// view opens. A stage nothing covers is a stage whose keys can change without
// anybody noticing.
func TestLiveKeys_EveryStateGolden(t *testing.T) {
	t.Parallel()
	editStages := []struct {
		name  string
		stage editStage
	}{
		{"the field list", stageBrowse},
		{"a field taking typing", stageTyping},
		{"waiting for the go-ahead to save", stageConfirm},
		{"saving", stageSaving},
		{"somebody else changed it first", stageConflict},
	}
	moveStages := []struct {
		name  string
		stage moveStage
	}{
		{"the moves this issue can make", moveList},
		{"the transition screen", moveScreen},
		{"waiting for the go-ahead to move", moveConfirm},
		{"moving", moveDoing},
	}
	if len(editStages) != len(editLiveSets) || len(moveStages) != len(moveLiveSets) {
		t.Fatalf("the panes have %d and %d stages; this test names %d and %d",
			len(editLiveSets), len(moveLiveSets), len(editStages), len(moveStages))
	}

	var b strings.Builder
	b.WriteString("editing an issue's fields\n")
	for _, s := range editStages {
		fmt.Fprintf(&b, "  %s\n", s.name)
		writeKeySet(&b, editLiveSets[s.stage])
	}
	b.WriteString("changing an issue's status\n")
	for _, s := range moveStages {
		fmt.Fprintf(&b, "  %s\n", s.name)
		writeKeySet(&b, moveLiveSets[s.stage])
	}
	golden(t, "keys.golden", b.String())
}

func TestLiveKeys_FollowTheStageTheEditorIsIn(t *testing.T) {
	t.Parallel()
	m, ok := NewEdit(testDeps(nil), jira.Issue{Key: "PROJ-1"}).(*editModel)
	if !ok {
		t.Fatal("NewEdit no longer builds an *editModel")
	}
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		stage editStage
	}{
		{"browsing", stageBrowse},
		{"typing", stageTyping},
		{"confirming", stageConfirm},
		{"saving", stageSaving},
		{"conflicted", stageConflict},
	} {
		m.stage = tc.stage
		set, gen := m.LiveKeys()
		if gen != int(tc.stage) {
			t.Errorf("%s: generation %d, want %d", tc.name, gen, tc.stage)
		}
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s share generation %d, so the footer will not repaint between them",
				tc.name, other, gen)
		}
		seen[gen] = tc.name
		if tc.stage == stageSaving && !set.IsZero() {
			t.Errorf("a save in flight advertises %s, none of which answers", shortOf(set))
		}
	}
}

func TestLiveKeys_FollowTheStageThePickerIsIn(t *testing.T) {
	t.Parallel()
	m, ok := NewMove(testDeps(nil), jira.Issue{Key: "PROJ-1"}).(*moveModel)
	if !ok {
		t.Fatal("NewMove no longer builds a *moveModel")
	}
	seen := map[int]string{}
	for _, tc := range []struct {
		name  string
		stage moveStage
	}{
		{"choosing a move", moveList},
		{"filling the screen in", moveScreen},
		{"confirming", moveConfirm},
		{"moving", moveDoing},
	} {
		m.stage = tc.stage
		_, gen := m.LiveKeys()
		if gen != int(tc.stage) {
			t.Errorf("%s: generation %d, want %d", tc.name, gen, tc.stage)
		}
		if other, clash := seen[gen]; clash {
			t.Errorf("%s and %s share generation %d, so the footer will not repaint between them",
				tc.name, other, gen)
		}
		seen[gen] = tc.name
	}
	if !strings.Contains(shortOf(moveLiveSets[moveScreen]), "next value") {
		t.Error("the transition screen does not advertise the key that fills a field in")
	}
	if strings.Contains(shortOf(moveLiveSets[moveList]), "next value") {
		t.Error("the list of moves advertises a key that only the screen answers")
	}
}

// y means go ahead with the save in one stage and re-read the issue in another.
// The label has to come from the stage, or one of the two is a lie.
func TestLiveKeys_YIsNamedForTheQuestionItIsAnswering(t *testing.T) {
	t.Parallel()
	confirm := shortOf(editLiveSets[stageConfirm])
	conflict := shortOf(editLiveSets[stageConflict])
	switch {
	case !strings.Contains(confirm, "y go ahead"):
		t.Errorf("a save waiting to be confirmed does not name y: %s", confirm)
	case !strings.Contains(conflict, "re-read"):
		t.Errorf("a conflict does not say what y would do: %s", conflict)
	case confirm == conflict:
		t.Errorf("both questions are advertised the same way: %s", confirm)
	}
}

// AllocsPerRun measures the whole process, so this one cannot run beside
// anything else.
func TestLiveKeys_CostNothingToAskFor(t *testing.T) {
	edit, ok := NewEdit(testDeps(nil), jira.Issue{Key: "PROJ-1"}).(*editModel)
	if !ok {
		t.Fatal("NewEdit no longer builds an *editModel")
	}
	move, ok := NewMove(testDeps(nil), jira.Issue{Key: "PROJ-1"}).(*moveModel)
	if !ok {
		t.Fatal("NewMove no longer builds a *moveModel")
	}
	for name, ask := range map[string]func(){
		"the field editor":      func() { _, _ = edit.LiveKeys() },
		"the transition picker": func() { _, _ = move.LiveKeys() },
	} {
		if got := testing.AllocsPerRun(100, ask); got != 0 {
			t.Errorf("%s allocates %.0f times to report its keys; chromeFor asks on every frame, so the sets must be stored",
				name, got)
		}
	}
}

func shortOf(set kernel.KeySet) string {
	return strings.Join(labels(set.Short), " · ")
}

func labels(bindings []kernel.Binding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.Help().Key+" "+b.Help().Desc)
	}
	return out
}

func writeKeySet(b *strings.Builder, set kernel.KeySet) {
	if set.IsZero() {
		b.WriteString("    nothing of its own; the globals are all that answer\n")
		return
	}
	fmt.Fprintf(b, "    short  %s\n", shortOf(set))
	for _, column := range set.Full {
		fmt.Fprintf(b, "    full   [%s]\n", strings.Join(labels(column), ", "))
	}
}
