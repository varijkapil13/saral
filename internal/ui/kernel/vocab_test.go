package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCanon_IsMintedWithTheCanonicalKeysAndLabel(t *testing.T) {
	resetRegistry()
	b := Canon(ActAssign)
	m, ok := MintOf(b)
	if !ok || m.Action != ActAssign || m.Local {
		t.Fatalf("MintOf = %+v, %v; want the assign action", m, ok)
	}
	if got := b.Help().Key; got != "@" {
		t.Errorf("label = %q, want @", got)
	}
	if got := Canon(ActAssign, "assign the picked cards").Help().Desc; got != "assign the picked cards" {
		t.Errorf("desc = %q, want the override", got)
	}
	if errs := RegistrationErrors(); len(errs) != 0 {
		t.Errorf("errors = %v", errs)
	}
}

func TestCanon_AnUnknownActionIsAStartupError(t *testing.T) {
	resetRegistry()
	b := Canon("no-such-action")
	if b.Enabled() {
		t.Error("an unknown action produced a live binding")
	}
	errs := RegistrationErrors()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "no-such-action") {
		t.Errorf("errors = %v, want one naming the action", errs)
	}
}

func TestMint_BindAloneIsNotMintedAndTerseKeepsTheMint(t *testing.T) {
	if _, ok := MintOf(Bind([]string{"e"}, "e", "edit")); ok {
		t.Error("a binding spelling the keys of a canonical action was taken for one")
	}
	canon := Canon(ActEdit)
	m, ok := MintOf(Terse(canon, "fields"))
	if !ok || m.Action != ActEdit {
		t.Errorf("Terse lost the mint: %+v, %v", m, ok)
	}
	local := Local("probe", "zoom", []string{"+"}, "+", "zoom in")
	if m, ok := MintOf(Terse(local, "in")); !ok || !m.Local || m.Owner != "probe" || m.ID != "zoom" {
		t.Errorf("Terse lost the local mint: %+v, %v", m, ok)
	}
}

func TestMint_TheSameKeysMintedAsTwoThingsIsAStartupError(t *testing.T) {
	resetRegistry()
	keys := []string{"+"}
	Local("probe", "zoom", keys, "+", "zoom in")
	Local("probe", "other", keys, "+", "zoom in")
	errs := RegistrationErrors()
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "minted as both") {
		t.Errorf("errors = %v, want one collision", errs)
	}
}

func TestRegisterCommand_AnActionHasToBeOneTheVocabularyKnows(t *testing.T) {
	resetRegistry()
	RegisterCommand(Command{ID: "probe.sort", Action: "nonsense", Run: func(Deps) tea.Cmd { return nil }})
	if errs := RegistrationErrors(); len(errs) != 1 {
		t.Errorf("errors = %v, want one", errs)
	}
}
