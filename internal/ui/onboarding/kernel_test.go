package onboarding

import (
	"runtime"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"
	"github.com/zalando/go-keyring"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// TestKernel_RunsTheWholeFlowThroughTheRegisteredView drives the view the way
// the program does: through the registry, the kernel's key routing and its
// frame, with the fake underneath. It is not parallel because both the kernel
// registry and the connector are process-wide.
func TestKernel_RunsTheWholeFlowThroughTheRegisteredView(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)

	fake := testFake()
	SetConnector(func(string, string, string) (jira.Client, error) { return fake, nil })
	t.Cleanup(func() { SetConnector(nil) })

	if errs := kernel.RegistrationErrors(); len(errs) > 0 {
		t.Fatalf("the view did not register cleanly: %v", errs)
	}
	spec, ok := kernel.LookupView(ViewID)
	if !ok {
		t.Fatal("the view is not in the registry, so no build can open it")
	}
	if spec.Slot != 0 {
		t.Errorf("the view claims footer slot %d; setup is not a place to navigate to", spec.Slot)
	}
	if kernel.KeysFor(ViewID).IsZero() {
		t.Error("no keys are registered, so the footer can say nothing about this view")
	}

	root, err := kernel.New(testDeps(), kernel.WithSize(100, 30), kernel.WithInitialView(ViewID), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	k := &kernelDriver{t: t, model: root}
	k.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	k.run(root.Init())

	k.typeIn(testSite)
	k.press("enter")
	k.typeIn(testEmail)
	k.press("enter")
	k.typeIn(testToken)
	k.press("enter")
	k.press("enter")
	// Typed, not clicked: PROJ contains an R, which the kernel would have
	// matched as its own refresh binding before the field ever saw it.
	k.typeIn("PROJ")
	k.press("enter")

	if !strings.Contains(k.frame(), "What this token can do in PROJ") {
		t.Fatalf("the probe result never reached the frame:\n%s", k.frame())
	}
	k.press("enter")
	if !strings.Contains(k.frame(), "The profile is written") {
		t.Fatalf("the flow did not finish:\n%s", k.frame())
	}
	for i, frame := range k.frames {
		if strings.Contains(frame, testToken) {
			t.Fatalf("kernel frame %d shows the token:\n%s", i, frame)
		}
	}
}

// A credential is typed, not pasted, and every Atlassian token contains digits.
// If the kernel's own bindings reach the form first, the token that gets
// verified is not the token that was typed — and the user is sent back to
// re-enter one that was already correct.
func TestKernel_TheFormReceivesEveryCharacterOfACredential(t *testing.T) {
	var got struct{ site, email, token string }
	SetConnector(func(site, email, token string) (jira.Client, error) {
		got.site, got.email, got.token = site, email, token
		return testFake(), nil
	})
	t.Cleanup(func() { SetConnector(nil) })

	root, err := kernel.New(testDeps(), kernel.WithSize(100, 30), kernel.WithInitialView(ViewID), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	k := &kernelDriver{t: t, model: root}
	k.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	k.run(root.Init())

	// Every one of these carries a character the kernel binds globally: the
	// digits are slot keys, r and R are refresh, q is quit, ? is help.
	const (
		site  = "acme1.atlassian.net"
		email = "rita.quinn+jira2@acme.com"
		token = "ATATT3xFfGF0Rq9Quick27?"
	)
	k.typeIn(site)
	k.press("enter")
	k.typeIn(email)
	k.press("enter")
	k.typeIn(token)
	k.press("enter")
	k.press("enter")

	if got.site != site {
		t.Errorf("the site arrived as %q, want %q", got.site, site)
	}
	if got.email != email {
		t.Errorf("the email arrived as %q, want %q", got.email, email)
	}
	if got.token != token {
		t.Errorf("the token arrived as %q, want %q", got.token, token)
	}
}

// A view holding the keyboard must still be quittable by the one key that always
// means it.
func TestKernel_CtrlCStillQuitsWhileTheFormHasTheKeyboard(t *testing.T) {
	SetConnector(func(string, string, string) (jira.Client, error) { return testFake(), nil })
	t.Cleanup(func() { SetConnector(nil) })

	root, err := kernel.New(testDeps(), kernel.WithSize(100, 30), kernel.WithInitialView(ViewID), kernel.WithMouse(false))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	k := &kernelDriver{t: t, model: root}
	k.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	k.typeIn("acme")

	if _, cmd := k.model.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Error("ctrl+c produced no command while the form had the keyboard")
	}
}

func TestKernel_ClickingASuggestionAndACompletedStepWorks(t *testing.T) {
	zones := zone.New()
	t.Cleanup(zones.Close)

	deps := testDeps()
	deps.Zones = zones
	d := newDriver(t, testFake(), func(x *kernel.Deps) { x.Zones = zones })
	d.credentials()
	d.press("enter")
	d.atStep(stepProject)

	prefix := d.model().zone
	scan(t, zones, d.view.View())
	eventually(t, func() bool { return !zones.Get(prefix + "project:PROJ").IsZero() })

	info := zones.Get(prefix + "project:PROJ")
	d.send(tea.MouseClickMsg{X: info.StartX + 2, Y: info.StartY, Button: tea.MouseLeft})
	if got := d.model().value(fieldProject); got != "PROJ" {
		t.Errorf("clicking a suggestion left the field at %q", got)
	}

	scan(t, zones, d.view.View())
	eventually(t, func() bool { return !zones.Get(prefix + "step:email").IsZero() })
	info = zones.Get(prefix + "step:email")
	d.send(tea.MouseClickMsg{X: info.StartX + 4, Y: info.StartY, Button: tea.MouseLeft})
	d.atStep(stepEmail)
	if got := d.model().value(fieldEmail); got != testEmail {
		t.Errorf("clicking back to a finished step lost what was in it: %q", got)
	}
}

func TestKernel_ClickingATokenStoreChoosesIt(t *testing.T) {
	zones := zone.New()
	t.Cleanup(zones.Close)

	d := newDriver(t, testFake(), func(x *kernel.Deps) { x.Zones = zones })
	d.credentials()
	d.atStep(stepStorage)

	prefix := d.model().zone
	scan(t, zones, d.view.View())
	eventually(t, func() bool { return !zones.Get(prefix + "store:command").IsZero() })

	info := zones.Get(prefix + "store:command")
	d.send(tea.MouseClickMsg{X: info.StartX + 2, Y: info.StartY, Button: tea.MouseLeft})
	if got := d.model().store; got != storeCommand {
		t.Errorf("the store is %v after clicking the command row", got)
	}
}

// scan hands a frame to the zone manager the way the kernel does when the mouse
// is on, which is what turns a marked string into coordinates.
func scan(t *testing.T, zones *zone.Manager, frame string) {
	t.Helper()
	_ = zones.Scan(frame)
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("the zone never turned up; the scan is asynchronous but not this slow")
		}
		runtime.Gosched()
	}
}

type kernelDriver struct {
	t      *testing.T
	model  tea.Model
	frames []string
}

func (k *kernelDriver) send(msg tea.Msg) {
	k.t.Helper()
	next, cmd := k.model.Update(msg)
	k.model = next
	k.frames = append(k.frames, k.frame())
	k.run(cmd)
}

func (k *kernelDriver) run(cmd tea.Cmd) {
	k.t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			k.run(c)
		}
	case spinner.TickMsg:
	default:
		k.send(msg)
	}
}

func (k *kernelDriver) typeIn(s string) {
	k.t.Helper()
	for _, r := range s {
		k.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (k *kernelDriver) press(keys ...string) {
	k.t.Helper()
	for _, s := range keys {
		k.send(keyPress(s))
	}
}

func (k *kernelDriver) frame() string {
	return ansi.Strip(k.model.(kernel.Model).Frame())
}
