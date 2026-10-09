package search

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

var freqNow = time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)

func freqSave(f *Frecency) {
	if s, ok := f.Pending(); ok {
		f.Write(s)
	}
}

func freqBlockedPath(t *testing.T) string {
	t.Helper()
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(blocked, "usage.json")
}

func TestFrecency_ScoresCountAgainstHowLongAgoItWas(t *testing.T) {
	t.Parallel()

	f := OpenFrecency("", "commands")
	for range 3 {
		f.Ran("old", freqNow.Add(-14*24*time.Hour))
	}
	f.Ran("recent", freqNow)

	if old, recent := f.Score("old", freqNow), f.Score("recent", freqNow); recent <= old {
		t.Errorf("one run today scores %.3f and three a fortnight ago score %.3f", recent, old)
	}
	if never := f.Score("never", freqNow); never != 0 {
		t.Errorf("an item never run scores %.3f, want 0", never)
	}
}

func TestFrecency_ManyRunsStillBeatOneWhenTheyAreAsRecent(t *testing.T) {
	t.Parallel()

	f := OpenFrecency("", "commands")
	for range 5 {
		f.Ran("often", freqNow.Add(-time.Hour))
	}
	f.Ran("once", freqNow.Add(-time.Hour))
	if f.Score("often", freqNow) <= f.Score("once", freqNow) {
		t.Error("count counts for nothing, so the table is recency and not frecency")
	}
}

func TestFrecency_CountsEachRunAndIgnoresAnEmptyID(t *testing.T) {
	t.Parallel()

	f := OpenFrecency("", "commands")
	for want := 1; want <= 4; want++ {
		if got := f.Ran("issue.edit", freqNow); got != want {
			t.Errorf("run %d was counted as %d", want, got)
		}
	}
	if got := f.Ran("  ", freqNow); got != 0 {
		t.Errorf("an item with no ID was counted as %d", got)
	}
}

func TestFreqDecay_NeverGrowsAUseDatedInTheFutureAndHalvesOverAWeek(t *testing.T) {
	t.Parallel()

	if got := freqDecay(-24 * time.Hour); got != 1 {
		t.Errorf("decay of a future use is %.3f, want 1", got)
	}
	if got := freqDecay(freqHalfLife); got < 0.49 || got > 0.51 {
		t.Errorf("decay over one half-life is %.3f, want a half", got)
	}
}

func TestFrecency_SurvivesTheProcessThatWroteIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "palette", "usage.json")
	first := OpenFrecency(path, "commands")
	first.Ran("issues.mine", freqNow)
	first.Ran("issues.mine", freqNow)
	freqSave(first)

	second := OpenFrecency(path, "commands")
	if got := second.Ran("issues.mine", freqNow); got != 3 {
		t.Errorf("a new table counted the next run as %d, want 3", got)
	}
}

func TestFrecency_WritesTheFileFormatOtherBuildsRead(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "usage.json")
	f := OpenFrecency(path, "projects")
	f.Ran("PROJ", freqNow)
	freqSave(f)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(map[string]map[string]freqUse{"projects": {"PROJ": {Count: 1, Last: freqNow}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("file = %s, want %s", raw, want)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v (%v), want 0600", info, err)
	}
}

func TestFrecency_ReadsOnlyItsOwnPartOfTheFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "usage.json")
	f := OpenFrecency(path, "commands")
	f.Ran("issue.edit", freqNow)
	freqSave(f)

	if got := OpenFrecency(path, "projects").Score("issue.edit", freqNow); got != 0 {
		t.Errorf("a projects table read a commands entry: %.3f", got)
	}
}

func TestFrecency_KeepsRankingWhenThereIsNowhereToWrite(t *testing.T) {
	t.Parallel()

	f := OpenFrecency("", "commands")
	f.Ran("theme.dark", freqNow)
	freqSave(f)
	if got := f.Score("theme.dark", freqNow); got <= 0 {
		t.Errorf("a session with nowhere to keep the table ranks nothing: %.3f", got)
	}
	if _, ok := f.Warning(); ok {
		t.Error("the absence of a path was treated as a failed write")
	}
}

func TestFrecency_CarriesOnInMemoryAfterAWriteFailsAndStopsTrying(t *testing.T) {
	t.Parallel()

	f := OpenFrecency(freqBlockedPath(t), "commands")
	f.Ran("issue.edit", freqNow)
	freqSave(f)

	f.Ran("issue.edit", freqNow)
	if _, ok := f.Pending(); ok {
		t.Error("a table whose write failed offered another write")
	}
	if got := f.Score("issue.edit", freqNow); got <= 0 {
		t.Errorf("ranking stopped with the writing: %.3f", got)
	}
}

func TestFrecency_StaysBounded(t *testing.T) {
	t.Parallel()

	f := OpenFrecency("", "commands")
	for i := range freqBound + 50 {
		f.Ran("cmd."+strconv.Itoa(i), freqNow.Add(time.Duration(i)*time.Minute))
	}
	if len(f.uses) > freqBound {
		t.Errorf("the table holds %d entries with a bound of %d", len(f.uses), freqBound)
	}
	if got := f.Score("cmd."+strconv.Itoa(freqBound+49), freqNow); got <= 0 {
		t.Error("the newest entry was dropped rather than the lowest ranked one")
	}
}

func TestFrecency_IgnoresAMissingOrCorruptFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	missing := OpenFrecency(filepath.Join(dir, "none.json"), "commands")
	if got := missing.Ran("a", freqNow); got != 1 {
		t.Errorf("a table over a missing file counted the first run as %d", got)
	}

	corrupt := filepath.Join(dir, "usage.json")
	if err := os.WriteFile(corrupt, []byte("{not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := OpenFrecency(corrupt, "commands").Ran("a", freqNow); got != 1 {
		t.Errorf("a table over an unreadable file counted the first run as %d", got)
	}
}

func TestFrecency_DropsStoredEntriesWithNoIDOrNoCount(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "usage.json")
	raw := `{"commands":{"":{"count":3,"last":"2026-08-20T09:00:00Z"},"zero":{"count":0,"last":"2026-08-20T09:00:00Z"},"ok":{"count":2,"last":"2026-08-20T09:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	f := OpenFrecency(path, "commands")
	if len(f.uses) != 1 || f.Score("ok", freqNow) <= 0 {
		t.Errorf("loaded %v, want only the entry with an ID and a count", f.uses)
	}
}

func TestFrecency_PendingIsEmptyWithNothingToWrite(t *testing.T) {
	t.Parallel()

	f := OpenFrecency(filepath.Join(t.TempDir(), "usage.json"), "commands")
	if _, ok := f.Pending(); ok {
		t.Error("Pending claimed a write over a table nothing has run against yet")
	}
}

func TestFrecency_PendingCoalescesARunThatLandsWhileAWriteIsInFlight(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "palette", "usage.json")
	f := OpenFrecency(path, "commands")
	f.Ran("issue.edit", freqNow)

	snap, ok := f.Pending()
	if !ok {
		t.Fatal("Pending returned nothing for a dirty table")
	}
	f.Ran("issue.create", freqNow)
	if _, again := f.Pending(); again {
		t.Error("Pending claimed a second write while the first was in flight")
	}

	f.Write(snap)
	freqSave(f)

	if got := OpenFrecency(path, "commands").Score("issue.create", freqNow); got <= 0 {
		t.Error("the run that landed mid-write never reached disk")
	}
}

func TestFrecency_WarningIsSaidOnceAfterAFailedWrite(t *testing.T) {
	t.Parallel()

	f := OpenFrecency(freqBlockedPath(t), "commands")
	f.Ran("issue.edit", freqNow)
	freqSave(f)

	if text, ok := f.Warning(); !ok || text == "" {
		t.Fatal("Warning did not report the failed write")
	}
	if _, ok := f.Warning(); ok {
		t.Error("Warning reported the same failure twice")
	}
}

func TestFrecency_WarningIsSilentWhenNothingFailed(t *testing.T) {
	t.Parallel()

	f := OpenFrecency(filepath.Join(t.TempDir(), "usage.json"), "commands")
	f.Ran("issue.edit", freqNow)
	freqSave(f)
	if _, ok := f.Warning(); ok {
		t.Error("Warning reported a failure after a good write")
	}
}
