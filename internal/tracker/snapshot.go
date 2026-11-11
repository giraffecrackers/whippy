// Package tracker polls GitHub and reports what changed between polls.
package tracker

import (
	"fmt"
	"strings"
	"time"

	"whippy/internal/github"
)

// Snapshot is what GitHub said at one poll.
type Snapshot struct {
	Login string
	PRs   []github.PR
	// Runs are the workflow runs the viewer triggered, newest first.
	Runs []github.Run
	// Closed maps PR.Key to github.PRMerged or github.PRClosed.
	Closed    map[string]string
	FetchedAt time.Time
}

// NeedsAttention reports whether the author has something to do about a
// pull request.
func NeedsAttention(pr github.PR) bool {
	return pr.Rollup == github.StatusFailure ||
		pr.Mergeable == github.MergeableConflicting ||
		pr.Review == github.ReviewChangesRequested
}

// RunsFor returns the runs of a pull request's head commit.
func (s Snapshot) RunsFor(pr github.PR) []github.Run {
	var runs []github.Run
	for _, r := range s.Runs {
		if r.Repo == pr.Repo && r.SHA == pr.HeadSHA {
			runs = append(runs, r)
		}
	}
	return runs
}

// CurrentRuns returns the latest run of each workflow on each branch,
// newest first: an older failure that a newer run replaced is history.
func (s Snapshot) CurrentRuns() []github.Run {
	seen := map[string]bool{}
	var runs []github.Run
	for _, r := range s.Runs {
		key := r.Repo + "\x00" + r.Workflow + "\x00" + queueEntry(r.Branch)
		if !seen[key] {
			seen[key] = true
			runs = append(runs, r)
		}
	}
	return runs
}

// queueEntry drops the commit from a merge queue's branch, which is
// gh-readonly-queue/<base>/pr-<number>-<sha>: each attempt to merge a pull
// request gets a branch of its own, and a later attempt replaces an earlier.
func queueEntry(branch string) string {
	if !strings.HasPrefix(branch, "gh-readonly-queue/") {
		return branch
	}
	if i := strings.LastIndexByte(branch, '-'); i >= 0 {
		return branch[:i]
	}
	return branch
}

func (s Snapshot) headSHAs() map[string]bool {
	heads := map[string]bool{}
	for _, pr := range s.PRs {
		heads[pr.Repo+"@"+pr.HeadSHA] = true
	}
	return heads
}

// staleAfter is how long something may run before it is taken to wait for
// a person rather than a machine: GitHub stops a hosted job after six hours.
const staleAfter = 6 * time.Hour

// RunActive reports whether a run is under way, rather than finished,
// waiting for an approval, or pending for so long that it must be stuck.
func RunActive(r github.Run, now time.Time) bool {
	started := r.StartedAt
	if started.IsZero() {
		started = r.CreatedAt
	}
	return r.Status == github.StatusPending && !r.Waiting && now.Sub(started) < staleAfter
}

// Counts returns how many things failed and how many are running. A run of
// a pull request's head commit counts once, as that pull request. Failed
// runs that skip reports true for, as those the user dismissed, are left
// out; skip may be nil.
func (s Snapshot) Counts(skip func(github.Run) bool) (failing, running int) {
	for _, pr := range s.PRs {
		switch {
		case NeedsAttention(pr):
			failing++
		case pr.Rollup == github.StatusPending:
			running++
		}
	}
	heads := s.headSHAs()
	for _, r := range s.CurrentRuns() {
		if heads[r.Repo+"@"+r.SHA] {
			continue
		}
		switch {
		case r.Status == github.StatusFailure && (skip == nil || !skip(r)):
			failing++
		case RunActive(r, s.FetchedAt):
			running++
		}
	}
	return failing, running
}

// prActive reports whether a pull request's checks are under way. One left
// pending and untouched for hours waits for a check that will not report.
func prActive(pr github.PR, now time.Time) bool {
	return pr.Rollup == github.StatusPending && now.Sub(pr.UpdatedAt) < staleAfter
}

// Active reports whether anything is under way, so worth polling sooner.
func (s Snapshot) Active() bool {
	for _, pr := range s.PRs {
		if prActive(pr, s.FetchedAt) {
			return true
		}
	}
	for _, r := range s.Runs {
		if RunActive(r, s.FetchedAt) {
			return true
		}
	}
	return false
}

type EventKind int

const (
	ChecksFailed EventKind = iota
	ChecksPassed
	RunFailed
	RunSucceeded
	Approved
	ChangesRequested
	Conflict
	Merged
	Closed
)

// Event is one change worth a notification.
type Event struct {
	Kind EventKind
	// Key names the event's subject and kind, so that a later one of the
	// same replaces its notification.
	Key      string
	Repo     string
	Title    string
	Subtitle string
	Body     string
	URL      string
}

// Diff returns what changed from one snapshot to the next. Things that are
// new in next are not events, except runs that started and finished
// between the two.
func Diff(prev, next Snapshot) []Event {
	var events []Event

	before := map[string]github.PR{}
	for _, pr := range prev.PRs {
		before[pr.Key()] = pr
	}
	still := map[string]bool{}
	for _, pr := range next.PRs {
		still[pr.Key()] = true
		old, ok := before[pr.Key()]
		if !ok {
			continue
		}
		add := func(kind EventKind, name, title string) {
			events = append(events, Event{
				Kind: kind, Key: pr.Key() + "/" + name, Repo: pr.Repo,
				Title: title, Subtitle: pr.Key(), Body: pr.Title, URL: pr.URL,
			})
		}
		// A new head commit arms the checks again, even into the same state.
		if pr.Rollup != old.Rollup || pr.HeadSHA != old.HeadSHA {
			switch pr.Rollup {
			case github.StatusFailure:
				add(ChecksFailed, "checks", "Checks failed")
			case github.StatusSuccess:
				add(ChecksPassed, "checks", "Checks passed")
			}
		}
		if pr.Review != old.Review {
			switch pr.Review {
			case github.ReviewApproved:
				add(Approved, "review", "Approved")
			case github.ReviewChangesRequested:
				add(ChangesRequested, "review", "Changes requested")
			}
		}
		if pr.Mergeable == github.MergeableConflicting && old.Mergeable != github.MergeableConflicting {
			add(Conflict, "conflict", "Merge conflict")
		}
	}
	for _, old := range prev.PRs {
		if still[old.Key()] {
			continue
		}
		ev := Event{Key: old.Key() + "/state", Repo: old.Repo, Subtitle: old.Key(), Body: old.Title, URL: old.URL}
		switch next.Closed[old.Key()] {
		case github.PRMerged:
			ev.Kind, ev.Title = Merged, "Merged"
		case github.PRClosed:
			ev.Kind, ev.Title = Closed, "Closed"
		default:
			continue // out of the search's reach; say nothing
		}
		events = append(events, ev)
	}

	// The pull request's own event covers the runs of its head commit.
	heads := next.headSHAs()
	for k := range prev.headSHAs() {
		heads[k] = true
	}
	ran := map[int64]github.Run{}
	for _, r := range prev.Runs {
		ran[r.ID] = r
	}
	for _, r := range next.Runs {
		if r.Status == github.StatusPending || r.Cancelled || heads[r.Repo+"@"+r.SHA] {
			continue
		}
		old, ok := ran[r.ID]
		if ok && old.Status != github.StatusPending {
			continue
		}
		if !ok && !r.CreatedAt.After(prev.FetchedAt) {
			continue
		}
		ev := Event{
			Key: fmt.Sprintf("%s/run/%d", r.Repo, r.ID), Repo: r.Repo,
			Subtitle: fmt.Sprintf("%s · %s", r.Repo, r.Branch), Body: r.Title, URL: r.URL,
		}
		switch r.Status {
		case github.StatusFailure:
			ev.Kind, ev.Title = RunFailed, r.Workflow+" failed"
		case github.StatusSuccess:
			ev.Kind, ev.Title = RunSucceeded, r.Workflow+" succeeded"
		default:
			continue
		}
		events = append(events, ev)
	}
	return events
}
