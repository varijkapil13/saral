package attach

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func newFake() *jiratest.Fake {
	return jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(2)),
	)
}

func fileOf(name, body string) jira.FileRef {
	return jira.FileRef{
		Name: name, Size: int64(len(body)),
		Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil },
	}
}

func seeded(t *testing.T, f *jiratest.Fake) jira.Attachment {
	t.Helper()
	added, err := Send(t.Context(), f, "PROJ-1", fileOf("notes.txt", strings.Repeat("n", 4096)), nil)
	if err != nil || len(added) != 1 {
		t.Fatalf("seeding: %v, %d added", err, len(added))
	}
	return added[0]
}

// drain takes every total a transfer reports and proves the progress ends.
func drain(p *Progress) <-chan []int64 {
	out := make(chan []int64, 1)
	go func() {
		var got []int64
		for {
			n, open := p.Next()
			if !open {
				out <- got
				return
			}
			got = append(got, n)
		}
	}()
	return out
}

func failures() map[string]error {
	return map[string]error{
		"refused":      &jira.CapabilityError{Capability: jira.CapAttachments, Reason: "you need Create Attachments here"},
		"rate limited": &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"transport":    &jira.TransportError{Op: "attachments", Err: errors.New("connection refused")},
	}
}

func TestList_AnswersWithTheIssuesFiles(t *testing.T) {
	t.Parallel()
	f := newFake()
	att := seeded(t, f)

	got, err := List(t.Context(), f, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != att.ID {
		t.Errorf("listed %+v, want the one seeded file", got)
	}
}

func TestSend_AnswersWithWhatWasStoredAndClosesTheProgress(t *testing.T) {
	t.Parallel()
	f := newFake()
	p := NewProgress()
	totals := drain(p)

	added, err := Send(t.Context(), f, "PROJ-1", fileOf("a.png", strings.Repeat("x", 300)), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0].Filename != "a.png" || added[0].Size != 300 {
		t.Errorf("stored %+v", added)
	}
	<-totals
}

// stepper reports every byte of the file one at a time, which is what a large
// file does to a callback that forwards every write.
type stepper struct {
	jira.Attacher
	p      *Progress
	posted int
}

func (s *stepper) Upload(_ context.Context, _ string, files []jira.FileRef) ([]jira.Attachment, error) {
	for sent := int64(1); sent <= files[0].Size; sent++ {
		files[0].Progress(sent)
		select {
		case <-s.p.steps:
			s.posted++
		default:
		}
	}
	return []jira.Attachment{{ID: "att-1", Filename: files[0].Name, Size: files[0].Size}}, nil
}

func TestSend_ReportsOnlyWhenThePercentageMoves(t *testing.T) {
	t.Parallel()
	p := NewProgress()
	double := &stepper{p: p}

	if _, err := Send(t.Context(), double, "PROJ-1", fileOf("big.bin", strings.Repeat("x", 1000)), p); err != nil {
		t.Fatal(err)
	}
	if double.posted != 101 {
		t.Errorf("a thousand writes posted %d steps, want one per percentage (101)", double.posted)
	}
	if _, open := p.Next(); open {
		t.Error("the progress was left open, so its waiter never ends")
	}
}

func TestSend_AFileOfNoSizeReportsNothing(t *testing.T) {
	t.Parallel()
	p := NewProgress()
	double := &stepper{p: p}
	file := fileOf("empty.txt", "")
	file.Size = 0

	if _, err := Send(t.Context(), double, "PROJ-1", file, p); err != nil {
		t.Fatal(err)
	}
	if double.posted != 0 {
		t.Errorf("a file of no size posted %d steps", double.posted)
	}
}

func TestFetch_StreamsTheBytesAndEndsWithTheWholeSize(t *testing.T) {
	t.Parallel()
	f := newFake()
	att := seeded(t, f)
	p := NewProgress()
	totals := drain(p)

	var buf bytes.Buffer
	if err := Fetch(t.Context(), f, att.ID, &buf, p); err != nil {
		t.Fatal(err)
	}
	if int64(buf.Len()) != att.Size {
		t.Errorf("wrote %d bytes of %d", buf.Len(), att.Size)
	}
	got := <-totals
	if len(got) > 0 && got[len(got)-1] > att.Size {
		t.Errorf("reported %d bytes of a %d byte file", got[len(got)-1], att.Size)
	}
}

func TestFetch_ACancelledDownloadEndsItsProgress(t *testing.T) {
	t.Parallel()
	f := newFake()
	att := seeded(t, f)
	f.Delay(2 * time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	p := NewProgress()
	totals := drain(p)

	if err := Fetch(ctx, f, att.ID, io.Discard, p); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled download answered %v", err)
	}
	<-totals
}

func TestRemove_TakesTheFileOff(t *testing.T) {
	t.Parallel()
	f := newFake()
	att := seeded(t, f)

	if err := Remove(t.Context(), f, att.ID); err != nil {
		t.Fatal(err)
	}
	got, err := List(t.Context(), f, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("%d files left after the delete", len(got))
	}
}

func TestEveryUseCase_PassesTheSitesErrorThroughUnchanged(t *testing.T) {
	t.Parallel()
	calls := map[string]func(context.Context, *jiratest.Fake, jira.Attachment) error{
		"list": func(ctx context.Context, f *jiratest.Fake, _ jira.Attachment) error {
			_, err := List(ctx, f, "PROJ-1")
			return err
		},
		"send": func(ctx context.Context, f *jiratest.Fake, _ jira.Attachment) error {
			p := NewProgress()
			totals := drain(p)
			_, err := Send(ctx, f, "PROJ-1", fileOf("b.txt", "body"), p)
			<-totals
			return err
		},
		"fetch": func(ctx context.Context, f *jiratest.Fake, att jira.Attachment) error {
			p := NewProgress()
			totals := drain(p)
			err := Fetch(ctx, f, att.ID, io.Discard, p)
			<-totals
			return err
		},
		"remove": func(ctx context.Context, f *jiratest.Fake, att jira.Attachment) error {
			return Remove(ctx, f, att.ID)
		},
	}
	for call, run := range calls {
		for kind, want := range failures() {
			t.Run(call+"/"+kind, func(t *testing.T) {
				t.Parallel()
				f := newFake()
				att := seeded(t, f)
				f.FailNext(want)

				if err := run(t.Context(), f, att); err != want { //nolint:errorlint // the very value, unwrapped
					t.Errorf("answered %v, want the site's own error %v", err, want)
				}
			})
		}
	}
}

func TestProgress_KeepsOnlyTheNewestTotal(t *testing.T) {
	t.Parallel()
	p := NewProgress()
	p.report(10)
	p.report(20)
	p.report(30)
	if n, open := p.Next(); !open || n != 30 {
		t.Errorf("took %d (open %v), want the newest total 30", n, open)
	}
	p.Close()
	p.Close()
	if _, open := p.Next(); open {
		t.Error("a closed progress still reported a total")
	}
}

func TestProgress_NilIsAccepted(t *testing.T) {
	t.Parallel()
	f := newFake()
	att := seeded(t, f)
	if err := Fetch(t.Context(), f, att.ID, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
}
