package app

import (
	"context"
	"errors"
	"slices"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Ranking is one issue whose rank has been changed on screen ahead of the site,
// with S what a refusal restores besides its place. next is the issue that
// followed it in the read before the first step, which is where a refusal puts
// it back; later steps taken while one is out only mark it dirty, and the
// position the issue has on screen once the site answers is what is sent next,
// so two quick steps cannot reach the site out of order. sent is the issue that
// followed it when the step now out was sent, which becomes next once the site
// accepts that step.
type Ranking[S any] struct {
	step *rankStep[S]
	gen  int
	stop context.CancelFunc
}

type rankStep[S any] struct {
	key      string
	next     string
	snap     S
	sent     string
	sentSnap S
	dirty    bool
	anchor   string
	after    bool
}

// RankStart is what Step made of a step.
type RankStart uint8

const (
	// RankFresh is a new step: send it.
	RankFresh RankStart = iota
	// RankQueued is a further step for the issue already out; it goes once the site answers.
	RankQueued
	// RankBusy is a step refused because another issue is still out.
	RankBusy
)

// RankOutcome is what Answer made of an answer.
type RankOutcome uint8

// An answer is stale (to a send since superseded or dropped), a refusal, a
// success with steps queued behind it, or the rank done.
const (
	RankStale RankOutcome = iota
	RankRefused
	RankResend
	RankDone
)

// RankAnswer is what the site said to one send.
type RankAnswer struct {
	Gen int
	Key string
	Err error
}

// RankResult is what an answer means. Next and Snap are set when refused,
// Anchor and After when done.
type RankResult[S any] struct {
	Outcome RankOutcome
	Key     string
	Next    string
	Snap    S
	Anchor  string
	After   bool
}

// Key is the issue in flight, or "".
func (r *Ranking[S]) Key() string {
	if r.step == nil {
		return ""
	}
	return r.step.key
}

// Step takes a step for key; next and snap are what a refusal restores, and
// are kept only when the step is fresh.
func (r *Ranking[S]) Step(key, next string, snap S) RankStart {
	switch {
	case r.step == nil:
		r.step = &rankStep[S]{key: key, next: next, snap: snap}
		return RankFresh
	case r.step.key != key:
		return RankBusy
	}
	r.step.dirty = true
	return RankQueued
}

// Send asks the site to put the issue in flight at at, cancelling any earlier
// send. sent and snap are the follower and snapshot on screen now. run is
// nil when nothing is in flight.
func (r *Ranking[S]) Send(ranker jira.Ranker, at jira.RankPosition, sent string, snap S) (run func() RankAnswer, cancel context.CancelFunc) {
	if r.step == nil {
		return nil, nil
	}
	r.step.anchor, r.step.after = at.Anchor()
	r.step.sent, r.step.sentSnap = sent, snap
	r.Stop()
	r.gen++
	ctx, cancel := context.WithCancel(context.Background())
	r.stop = cancel
	gen, key := r.gen, r.step.key
	return func() RankAnswer {
		return RankAnswer{Gen: gen, Key: key, Err: RankOne(ctx, ranker, key, at)}
	}, cancel
}

// Answer reads the site's answer to a send. A refusal or a finished rank
// leaves nothing in flight; a resend promotes what was sent to what a later
// refusal restores, and the caller sends the position on screen again.
func (r *Ranking[S]) Answer(a RankAnswer) RankResult[S] {
	s := r.step
	if a.Gen != r.gen || s == nil || s.key != a.Key {
		return RankResult[S]{Outcome: RankStale}
	}
	r.stop = nil
	if a.Err != nil {
		r.step = nil
		return RankResult[S]{Outcome: RankRefused, Key: s.key, Next: s.next, Snap: s.snap}
	}
	if s.dirty {
		s.dirty, s.next, s.snap = false, s.sent, s.sentSnap
		return RankResult[S]{Outcome: RankResend, Key: s.key}
	}
	r.step = nil
	return RankResult[S]{Outcome: RankDone, Key: s.key, Anchor: s.anchor, After: s.after}
}

// Stop cancels the send that is out, keeping the issue in flight.
func (r *Ranking[S]) Stop() {
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
}

// Drop forgets the issue in flight without putting anything back; an answer
// still out is then stale.
func (r *Ranking[S]) Drop() {
	r.Stop()
	r.gen++
	r.step = nil
}

// RankOne ranks one issue. A partial answer that names it among the ranked is
// a success.
func RankOne(ctx context.Context, ranker jira.Ranker, key string, at jira.RankPosition) error {
	err := ranker.RankIssues(ctx, []string{key}, at)
	var partial *jira.PartialRankError
	if errors.As(err, &partial) && slices.Contains(partial.Ranked, key) {
		return nil
	}
	return err
}

// RankBeside is the position named by an issue's neighbours in its own column
// or section: before the one after it, else after the one before it. ok is
// false when it has neither.
func RankBeside(prev, next, fieldID string) (at jira.RankPosition, ok bool) {
	switch {
	case next != "":
		at = jira.RankBefore(next)
	case prev != "":
		at = jira.RankAfter(prev)
	default:
		return jira.RankPosition{}, false
	}
	at.FieldID = fieldID
	return at, true
}

// ShiftIssue moves issues[from] to just before or just after the issue keyed
// anchor, in place.
func ShiftIssue(issues []jira.Issue, from int, anchor string, after bool) []jira.Issue {
	iss := issues[from]
	issues = slices.Delete(issues, from, from+1)
	at := slices.IndexFunc(issues, func(i jira.Issue) bool { return i.Key == anchor })
	if at < 0 {
		return slices.Insert(issues, from, iss)
	}
	if after {
		at++
	}
	return slices.Insert(issues, at, iss)
}

// PutBack moves issues[from] to just before the issue keyed next, or to the
// end when next is "" or gone, in place.
func PutBack(issues []jira.Issue, from int, next string) []jira.Issue {
	iss := issues[from]
	issues = slices.Delete(issues, from, from+1)
	at := len(issues)
	if next != "" {
		if i := slices.IndexFunc(issues, func(i jira.Issue) bool { return i.Key == next }); i >= 0 {
			at = i
		}
	}
	return slices.Insert(issues, at, iss)
}
