package palette

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestTable_ScoresCountAgainstHowLongAgoItWas(t *testing.T) {
	t.Parallel()

	now := clockAt
	freq := memoryTable()
	for range 3 {
		freq.ran("old", now.Add(-14*24*time.Hour))
	}
	freq.ran("recent", now)

	old, recent := freq.score("old", now), freq.score("recent", now)
	if recent <= old {
		t.Errorf("one run today scores %.3f and three a fortnight ago score %.3f; a habit that stopped is still first",
			recent, old)
	}
	if never := freq.score("never", now); never != 0 {
		t.Errorf("a command never run scores %.3f, want 0", never)
	}
}

func TestTable_ManyRunsStillBeatOneWhenTheyAreAsRecent(t *testing.T) {
	t.Parallel()

	now := clockAt
	freq := memoryTable()
	for range 5 {
		freq.ran("often", now.Add(-time.Hour))
	}
	freq.ran("once", now.Add(-time.Hour))

	if freq.score("often", now) <= freq.score("once", now) {
		t.Error("count counts for nothing, so the table is recency and not frecency")
	}
}

func TestTable_CountsEachRunAndSaysHowManyThereHaveBeen(t *testing.T) {
	t.Parallel()

	freq := memoryTable()
	for want := 1; want <= 4; want++ {
		if got := freq.ran("issue.edit", clockAt); got != want {
			t.Errorf("run %d was counted as %d", want, got)
		}
	}
	if got := freq.ran("  ", clockAt); got != 0 {
		t.Errorf("a command with no ID was counted as %d", got)
	}
}

func TestDecay_NeverGrowsAUseThatIsDatedInTheFuture(t *testing.T) {
	t.Parallel()

	if got := decay(-24 * time.Hour); got != 1 {
		t.Errorf("decay of a future use is %.3f, want 1: a clock that moved backwards must not invent a ranking", got)
	}
	if got := decay(halfLife); got < 0.49 || got > 0.51 {
		t.Errorf("decay over one half-life is %.3f, want a half", got)
	}
}

func TestTable_SurvivesTheProcessThatWroteIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "palette", "usage.json")
	first := openTable(path, commandsPart)
	first.ran("issues.mine", clockAt)
	first.ran("issues.mine", clockAt)

	second := openTable(path, commandsPart)
	if got := second.ran("issues.mine", clockAt); got != 3 {
		t.Errorf("a new table counted the next run as %d, want 3: nothing was read back off disk", got)
	}
	if got := second.score("issues.mine", clockAt); got <= 0 {
		t.Errorf("the reloaded table scores the command at %.3f", got)
	}
}

func TestTable_KeepsRankingWhenThereIsNowhereToWrite(t *testing.T) {
	t.Parallel()

	freq := memoryTable()
	freq.ran("theme.dark", clockAt)
	freq.ran("theme.dark", clockAt)

	if got := freq.score("theme.dark", clockAt); got <= 0 {
		t.Errorf("a session with nowhere to keep the table ranks nothing: %.3f", got)
	}
	if freq.stopped {
		t.Error("a table with no path treated the absence of one as a failed write")
	}
}

// A cache directory that cannot be written to must not stop the palette from
// ranking what it has seen this session.
func TestTable_CarriesOnInMemoryAfterAWriteFails(t *testing.T) {
	t.Parallel()

	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	freq := openTable(filepath.Join(blocked, "usage.json"), commandsPart)

	if got := freq.ran("issue.edit", clockAt); got != 1 {
		t.Errorf("the run was counted as %d", got)
	}
	if !freq.stopped {
		t.Error("the failed write was not recorded, so every run will try it again")
	}
	if got := freq.score("issue.edit", clockAt); got <= 0 {
		t.Errorf("ranking stopped with the writing: %.3f", got)
	}
}

func TestTable_StaysBounded(t *testing.T) {
	t.Parallel()

	freq := memoryTable()
	for i := range bound + 50 {
		id := "cmd." + strconv.Itoa(i)
		freq.ran(id, clockAt.Add(time.Duration(i)*time.Minute))
	}
	if len(freq.uses) > bound {
		t.Errorf("the table holds %d entries with a bound of %d", len(freq.uses), bound)
	}
	if got := freq.score("cmd."+strconv.Itoa(bound+49), clockAt); got <= 0 {
		t.Error("the newest entry was dropped rather than the lowest ranked one")
	}
}

func TestTable_IgnoresAFileItCannotUnderstand(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(path, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	freq := openTable(path, commandsPart)
	if got := freq.ran("issue.edit", clockAt); got != 1 {
		t.Errorf("a table over an unreadable file counted the first run as %d", got)
	}
}
