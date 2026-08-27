package form

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestRender_DrawsTheIssueTypePicker(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(newFake(20)), 100, 24)
	golden(t, "types_100x24.golden", dr.view())
}

func TestRender_DrawsTheFormAtEveryWidth(t *testing.T) {
	t.Parallel()

	sizes := []struct {
		name string
		w, h int
	}{
		{"form_100x24.golden", 100, 24},
		{"form_140x30.golden", 140, 30},
		{"form_72x20.golden", 72, 20},
	}
	for _, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()

			dr := openOn(t, testDeps(newFake(20)), size.w, size.h, fakeStory)
			golden(t, size.name, dr.view())
		})
	}
}

func TestRender_DrawsWhatIsWrongOnTheFieldItIsAbout(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.focus("customfield_13401")
	dr.key("enter")
	dr.typeText("quite a few")
	dr.key("enter")
	dr.submitRow()
	dr.key("enter")

	golden(t, "problems_100x24.golden", dr.view())
}

func TestRender_DrawsAPickerOverTheFields(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.focus("priority")
	dr.key("enter")

	golden(t, "picker_100x24.golden", dr.view())
}

func TestRender_DrawsTheFieldsThatAreNotOfferedWhenTheyAreOpened(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.m.moveTo(len(dr.m.fields))
	dr.key("enter")

	golden(t, "notoffered_100x24.golden", dr.view())
}

func TestRender_KeepsEveryFrameTheSizeItWasGiven(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	for _, size := range [][2]int{{100, 24}, {72, 20}, {140, 30}, {40, 12}, {24, 5}} {
		dr.send(kernel.SizeMsg{Width: size[0], Height: size[1]})
		for _, open := range []string{"", "summary", "description", "priority"} {
			if open != "" {
				dr.focus(open)
				dr.key("enter")
			}
			if lines := strings.Split(dr.view(), "\n"); len(lines) != size[1] {
				t.Errorf("the frame is %d lines at %dx%d with %q open, want %d",
					len(lines), size[0], size[1], open, size[1])
			}
			dr.key("esc")
		}
	}
}

func TestRender_DrawsOnlyTheRowsThatFit(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.send(kernel.SizeMsg{Width: 100, Height: 6})

	body := strings.Split(dr.view(), "\n")
	if len(body) != 6 {
		t.Fatalf("the frame is %d lines, want 6", len(body))
	}
	// The last field is well past the window, so it must not have been drawn.
	last := dr.m.fields[len(dr.m.fields)-1]
	if strings.Contains(dr.view(), last.meta.Name) {
		t.Errorf("a row outside the window was rendered:\n%s", dr.view())
	}
}

func TestRender_ReusesARowItHasAlreadyDrawn(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.m.rows.reset()
	_ = dr.m.View()
	drawn := len(dr.m.rows.rows)
	if drawn == 0 {
		t.Fatal("nothing was memoized at all")
	}
	_ = dr.m.View()
	if got := len(dr.m.rows.rows); got != drawn {
		t.Errorf("a second frame built %d more rows, want every one to be a cache hit", got-drawn)
	}

	dr.focus("summary")
	dr.key("enter")
	dr.typeText("x")
	dr.key("enter")
	_ = dr.m.View()
	if got := len(dr.m.rows.rows); got <= drawn {
		t.Error("a changed field did not invalidate its memoized row")
	}
}

func TestPlanLayout_LeavesRoomForAValueAndWhatIsWrongWithIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		width       int
		label       int
		wantProblem bool
	}{
		{name: "a wide terminal", width: 140, label: 14, wantProblem: true},
		{name: "an ordinary one", width: 100, label: 14, wantProblem: true},
		{name: "a narrow one", width: 40, label: 14, wantProblem: false},
		{name: "one too narrow to be usable", width: 12, label: 14, wantProblem: false},
		{name: "a screen of very long field names", width: 100, label: 60, wantProblem: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lay := planLayout(tt.width, tt.label)
			if lay.label > maxLabel {
				t.Errorf("the label column is %d wide, want it capped at %d", lay.label, maxLabel)
			}
			if lay.value < minValue {
				t.Errorf("the value column is %d wide, want at least %d", lay.value, minValue)
			}
			if got := lay.problem > 0; got != tt.wantProblem {
				t.Errorf("a problem column %v, want %v", got, tt.wantProblem)
			}
		})
	}
}

// A hidden field's name comes from createmeta as the site spells it, so on a
// localised site it is not ASCII — and the label column every visible field is
// drawn in is sized by the widest of them. Measured in bytes, a Japanese name
// over-pads that column by eight and a combining mark by two.
func TestWidestLabel_MeasuresAHiddenFieldNameOnScreenRatherThanInBytes(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		field string
		want  int
	}{
		"a CJK name is three bytes to two columns":   {field: "課題の優先度設定", want: 16},
		"a combining mark costs bytes and no width":  {field: "Zu\u0308sammenfassung", want: 15},
		"an ASCII name measures the same either way": {field: "Story Points", want: 12},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dr := newDriver(t, testDeps(newFake(1)), 100, 24)
			dr.m.fields = nil
			dr.m.hidden = []hiddenField{{name: tc.field, reason: "not on the create screen"}}
			dr.m.relayout()

			if got := dr.m.widestLabel(); got != tc.want {
				t.Errorf("%q measured %d columns wide, want %d; len() is %d", tc.field, got, tc.want, len(tc.field))
			}
			if got := dr.m.lay.label; got != tc.want {
				t.Errorf("the label column every field is drawn in is %d wide, want %d", got, tc.want)
			}
		})
	}
}
