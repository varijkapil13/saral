package card

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestLook_CyclesRoomyCompactLinesAndParsesItsOwnWords(t *testing.T) {
	t.Parallel()

	if Default() != Roomy || Look(0) != Roomy {
		t.Fatalf("the default is %s and the zero value %s, want roomy for both", Default().Word(), Look(0).Word())
	}
	for _, tc := range []struct {
		look  Look
		next  Look
		lines int
		cards bool
		word  string
	}{
		{Roomy, Compact, 5, true, "roomy"},
		{Compact, Lines, 3, true, "compact"},
		{Lines, Roomy, 1, false, "lines"},
	} {
		if got := tc.look.Next(); got != tc.next {
			t.Errorf("%s.Next() = %s, want %s", tc.word, got.Word(), tc.next.Word())
		}
		if got := tc.look.Lines(); got != tc.lines {
			t.Errorf("%s.Lines() = %d, want %d", tc.word, got, tc.lines)
		}
		if got := tc.look.Cards(); got != tc.cards {
			t.Errorf("%s.Cards() = %v, want %v", tc.word, got, tc.cards)
		}
		if got := tc.look.Word(); got != tc.word {
			t.Errorf("Word() = %q, want %q", got, tc.word)
		}
		if got := Parse(" " + tc.word + " "); got != tc.look {
			t.Errorf("Parse(%q) = %s", tc.word, got.Word())
		}
	}
	for _, s := range []string{"", "cards", "ROOMY", "Compact"} {
		want := Roomy
		if s == "Compact" {
			want = Compact
		}
		if got := Parse(s); got != want {
			t.Errorf("Parse(%q) = %s, want %s", s, got.Word(), want.Word())
		}
	}
}

func TestRecall_FallsBackToRoomyOnAMissingOrUnknownLook(t *testing.T) {
	for _, tc := range []struct {
		saved string
		want  Look
	}{
		{"", Roomy},
		{"sparkly", Roomy},
		{"compact", Compact},
		{"lines", Lines},
	} {
		if err := config.SaveLook(tc.saved); err != nil {
			t.Fatal(err)
		}
		if got := Recall(); got != tc.want {
			t.Errorf("with %q saved, Recall() = %s, want %s", tc.saved, got.Word(), tc.want.Word())
		}
	}
	if err := config.SaveLook(""); err != nil {
		t.Fatal(err)
	}
}

func runAll(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, runAll(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func broadcastLook(t *testing.T, msgs []tea.Msg) (look Look, rest []tea.Msg) {
	t.Helper()
	found := false
	for _, m := range msgs {
		if b, ok := m.(kernel.BroadcastMsg); ok {
			if lm, ok := b.Msg.(LookMsg); ok {
				found, look = true, lm.Look
				continue
			}
		}
		rest = append(rest, m)
	}
	if !found {
		t.Fatalf("no LookMsg was broadcast among %#v", msgs)
	}
	return look, rest
}

func TestCycle_BroadcastsTheNextLookAndKeepsIt(t *testing.T) {
	t.Cleanup(func() { _ = config.SaveLook("") })

	look, rest := broadcastLook(t, runAll(Cycle(Roomy)))
	if look != Compact {
		t.Errorf("cycling from roomy broadcast %s, want compact", look.Word())
	}
	if len(rest) != 0 {
		t.Errorf("a save that worked said %#v", rest)
	}
	if got := Recall(); got != Compact {
		t.Errorf("after the save Recall() = %s, want compact", got.Word())
	}

	cmd := commandRun(t)
	if look, _ := broadcastLook(t, runAll(cmd)); look != Lines {
		t.Errorf("the palette cycled compact to %s, want lines", look.Word())
	}
	if look, _ := broadcastLook(t, runAll(commandRun(t))); look != Roomy {
		t.Errorf("the palette cycled lines to %s, want roomy", look.Word())
	}
}

func commandRun(t *testing.T) tea.Cmd {
	t.Helper()
	i := slices.IndexFunc(kernel.Commands(), func(c kernel.Command) bool { return c.ID == CommandID })
	if i < 0 {
		t.Fatalf("no %s command is registered", CommandID)
	}
	c := kernel.Commands()[i]
	if !slices.Equal(c.Keys, []string{"V"}) || c.Title == "" {
		t.Errorf("the command teaches %v as %q, want V", c.Keys, c.Title)
	}
	return c.Run(kernel.Deps{})
}

func TestCycle_AFailedSaveWarnsOnceAndStillBroadcasts(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SARAL_CACHE_DIR", filepath.Join(blocker, "cache"))
	saveFailed.Store(false)
	t.Cleanup(func() { saveFailed.Store(false) })

	look, rest := broadcastLook(t, runAll(Cycle(Compact)))
	if look != Lines {
		t.Errorf("the failed save broadcast %s, want lines", look.Word())
	}
	if len(rest) != 1 {
		t.Fatalf("the first failed save said %#v, want one warning", rest)
	}
	if st, ok := rest[0].(kernel.StatusMsg); !ok || st.Level != kernel.LevelWarn {
		t.Errorf("the first failed save said %#v, want a warning", rest[0])
	}
	if _, rest := broadcastLook(t, runAll(Cycle(Lines))); len(rest) != 0 {
		t.Errorf("the second failed save said %#v, want nothing more", rest)
	}
}
