package tracker

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"whippy/internal/auth"
	"whippy/internal/github"
)

var t0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func pr(mod func(*github.PR)) github.PR {
	p := github.PR{Repo: "o/r", Number: 1, Title: "Add thing", URL: "https://github.com/o/r/pull/1", HeadSHA: "a1", Rollup: github.StatusPending, UpdatedAt: t0}
	if mod != nil {
		mod(&p)
	}
	return p
}

func run(id int64, status github.Status, mod func(*github.Run)) github.Run {
	r := github.Run{ID: id, Repo: "o/r", Workflow: "Deploy", Branch: "main", SHA: "m1", Status: status, CreatedAt: t0.Add(-time.Hour)}
	if mod != nil {
		mod(&r)
	}
	return r
}

func kinds(events []Event) []EventKind {
	var out []EventKind
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func TestDiffPullRequests(t *testing.T) {
	tests := []struct {
		name       string
		prev, next []github.PR
		closed     map[string]string
		want       []EventKind
	}{
		{"nothing changed", []github.PR{pr(nil)}, []github.PR{pr(nil)}, nil, nil},
		{"new pull request", nil, []github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusFailure })}, nil, nil},
		{"checks failed", []github.PR{pr(nil)}, []github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusFailure })}, nil, []EventKind{ChecksFailed}},
		{"checks passed", []github.PR{pr(nil)}, []github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusSuccess })}, nil, []EventKind{ChecksPassed}},
		{"still failing", []github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusFailure })}, []github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusFailure })}, nil, nil},
		{"failing again on a new commit",
			[]github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusFailure })},
			[]github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusFailure; p.HeadSHA = "a2" })}, nil, []EventKind{ChecksFailed}},
		{"new commit starts checks", []github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusSuccess })}, []github.PR{pr(func(p *github.PR) { p.HeadSHA = "a2" })}, nil, nil},
		{"approved", []github.PR{pr(nil)}, []github.PR{pr(func(p *github.PR) { p.Review = github.ReviewApproved })}, nil, []EventKind{Approved}},
		{"changes requested", []github.PR{pr(nil)}, []github.PR{pr(func(p *github.PR) { p.Review = github.ReviewChangesRequested })}, nil, []EventKind{ChangesRequested}},
		{"review dismissed", []github.PR{pr(func(p *github.PR) { p.Review = github.ReviewApproved })}, []github.PR{pr(func(p *github.PR) { p.Review = github.ReviewRequired })}, nil, nil},
		{"conflict", []github.PR{pr(nil)}, []github.PR{pr(func(p *github.PR) { p.Mergeable = github.MergeableConflicting })}, nil, []EventKind{Conflict}},
		{"still conflicting",
			[]github.PR{pr(func(p *github.PR) { p.Mergeable = github.MergeableConflicting })},
			[]github.PR{pr(func(p *github.PR) { p.Mergeable = github.MergeableConflicting })}, nil, nil},
		{"merged", []github.PR{pr(nil)}, nil, map[string]string{"o/r#1": github.PRMerged}, []EventKind{Merged}},
		{"closed", []github.PR{pr(nil)}, nil, map[string]string{"o/r#1": github.PRClosed}, []EventKind{Closed}},
		{"gone without a trace", []github.PR{pr(nil)}, nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diff(Snapshot{PRs: tt.prev, FetchedAt: t0}, Snapshot{PRs: tt.next, Closed: tt.closed, FetchedAt: t0.Add(time.Minute)})
			if !slices.Equal(kinds(got), tt.want) {
				t.Errorf("events %v, want %v", kinds(got), tt.want)
			}
			for _, e := range got {
				if e.URL == "" || e.Key == "" || e.Title == "" {
					t.Errorf("incomplete event %+v", e)
				}
			}
		})
	}
}

func TestDiffRuns(t *testing.T) {
	tests := []struct {
		name       string
		prev, next []github.Run
		prs        []github.PR
		want       []EventKind
	}{
		{"finished with a failure", []github.Run{run(1, github.StatusPending, nil)}, []github.Run{run(1, github.StatusFailure, nil)}, nil, []EventKind{RunFailed}},
		{"finished well", []github.Run{run(1, github.StatusPending, nil)}, []github.Run{run(1, github.StatusSuccess, nil)}, nil, []EventKind{RunSucceeded}},
		{"still running", []github.Run{run(1, github.StatusPending, nil)}, []github.Run{run(1, github.StatusPending, nil)}, nil, nil},
		{"already finished", []github.Run{run(1, github.StatusFailure, nil)}, []github.Run{run(1, github.StatusFailure, nil)}, nil, nil},
		{"cancelled", []github.Run{run(1, github.StatusPending, nil)}, []github.Run{run(1, github.StatusSkipped, func(r *github.Run) { r.Cancelled = true })}, nil, nil},
		{"old run first seen", nil, []github.Run{run(1, github.StatusFailure, nil)}, nil, nil},
		{"started and finished between polls", nil, []github.Run{run(1, github.StatusFailure, func(r *github.Run) { r.CreatedAt = t0.Add(10 * time.Second) })}, nil, []EventKind{RunFailed}},
		{"run of a pull request's head", []github.Run{run(1, github.StatusPending, func(r *github.Run) { r.SHA = "a1" })},
			[]github.Run{run(1, github.StatusFailure, func(r *github.Run) { r.SHA = "a1" })}, []github.PR{pr(nil)}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Diff(Snapshot{Runs: tt.prev, PRs: tt.prs, FetchedAt: t0}, Snapshot{Runs: tt.next, PRs: tt.prs, FetchedAt: t0.Add(time.Minute)})
			if !slices.Equal(kinds(got), tt.want) {
				t.Errorf("events %v, want %v", kinds(got), tt.want)
			}
		})
	}
}

func TestCounts(t *testing.T) {
	s := Snapshot{
		PRs: []github.PR{
			pr(nil), // running
			pr(func(p *github.PR) { p.Number = 2; p.Rollup = github.StatusFailure }),
			pr(func(p *github.PR) {
				p.Number = 3
				p.Rollup = github.StatusSuccess
				p.Mergeable = github.MergeableConflicting
			}),
			pr(func(p *github.PR) { p.Number = 4; p.Rollup = github.StatusSuccess }),
		},
		Runs: []github.Run{
			run(1, github.StatusPending, func(r *github.Run) { r.SHA = "a1" }), // the first pull request's
			run(2, github.StatusSuccess, nil),                                  // replaces the failure below
			run(3, github.StatusFailure, nil),
			run(4, github.StatusFailure, func(r *github.Run) { r.Workflow = "Release" }),
			run(5, github.StatusPending, func(r *github.Run) { r.Branch = "next" }),
		},
	}
	s.FetchedAt = t0
	if failing, running := s.Counts(nil); failing != 3 || running != 2 {
		t.Errorf("failing %d, running %d; want 3 and 2", failing, running)
	}
	dismissed := func(r github.Run) bool { return r.ID == 4 }
	if failing, _ := s.Counts(dismissed); failing != 2 {
		t.Errorf("failing %d with a run dismissed, want 2", failing)
	}
	if !s.Active() {
		t.Error("not active with a run pending")
	}
}

func TestCountsMergeQueueRetry(t *testing.T) {
	queued := func(id int64, status github.Status, branch string) github.Run {
		return run(id, status, func(r *github.Run) { r.Branch = "gh-readonly-queue/main/" + branch })
	}
	s := Snapshot{Runs: []github.Run{
		queued(3, github.StatusSuccess, "pr-7-bbb"), // the retry of the failure below
		queued(2, github.StatusFailure, "pr-8-ccc"),
		queued(1, github.StatusFailure, "pr-7-aaa"),
	}}
	if failing, _ := s.Counts(nil); failing != 1 {
		t.Errorf("failing %d, want only the pull request that was not retried", failing)
	}
}

func TestWaitingAndStalledAreNotActive(t *testing.T) {
	waiting := run(1, github.StatusPending, func(r *github.Run) { r.Waiting = true })
	stalled := run(2, github.StatusPending, func(r *github.Run) { r.Branch = "old"; r.CreatedAt = t0.Add(-7 * time.Hour) })
	// Run again an hour ago: the attempt is young though the run is old.
	rerun := run(3, github.StatusPending, func(r *github.Run) {
		r.Branch = "again"
		r.CreatedAt, r.StartedAt = t0.Add(-7*time.Hour), t0.Add(-time.Hour)
	})
	forgotten := pr(func(p *github.PR) { p.UpdatedAt = t0.Add(-7 * time.Hour) })

	s := Snapshot{PRs: []github.PR{forgotten}, Runs: []github.Run{waiting, stalled}, FetchedAt: t0}
	if s.Active() {
		t.Error("active with only a waiting run, a stalled run and a forgotten pull request")
	}
	if _, running := s.Counts(nil); running != 1 {
		t.Errorf("running %d, want the pull request alone", running)
	}
	s.Runs = append(s.Runs, rerun)
	if !s.Active() {
		t.Error("not active with a run under way")
	}
}

type fakeAPI struct {
	mu      sync.Mutex
	clock   time.Time
	prs     github.PullRequests
	runs    map[string][]github.Run
	prErr   error
	runsErr map[string]error
	asked   []string
}

func (f *fakeAPI) PullRequests(context.Context, time.Time) (github.PullRequests, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prs, f.prErr
}

func (f *fakeAPI) Runs(_ context.Context, repo, actor string, _ time.Time) ([]github.Run, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, repo)
	return f.runs[repo], f.runsErr[repo]
}

// startPoller runs a poller whose updates arrive on the channel returned.
func startPoller(t *testing.T, api *fakeAPI, resolve func() auth.Result) (*Poller, <-chan Update) {
	t.Helper()
	updates := make(chan Update, 16)
	p := NewPoller(func(u Update) { updates <- u })
	p.Resolve = func(context.Context) auth.Result { return resolve() }
	p.NewAPI = func(string) API { return api }
	p.Now = func() time.Time {
		api.mu.Lock()
		defer api.mu.Unlock()
		if api.clock.IsZero() {
			return t0
		}
		return api.clock
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return p, updates
}

func next(t *testing.T, updates <-chan Update) Update {
	t.Helper()
	select {
	case u := <-updates:
		return u
	case <-time.After(5 * time.Second):
		t.Fatal("no update")
		return Update{}
	}
}

func ready() auth.Result { return auth.Result{State: auth.Ready, Token: "tok"} }

func TestPollerSeedsThenReportsEvents(t *testing.T) {
	api := &fakeAPI{
		prs: github.PullRequests{Login: "ada", Open: []github.PR{pr(nil)}, RecentRepos: []string{"o/r", "o/locked"}},
		runs: map[string][]github.Run{
			"o/r":     {run(1, github.StatusPending, nil)},
			"o/extra": {run(2, github.StatusSuccess, func(r *github.Run) { r.Repo = "o/extra"; r.CreatedAt = t0 })},
		},
		runsErr: map[string]error{"o/locked": github.ErrNoAccess},
	}
	p, updates := startPoller(t, api, ready)

	first := next(t, updates)
	if !first.Loaded || first.Err != nil || len(first.Events) != 0 || first.Snapshot.Login != "ada" {
		t.Fatalf("first update: %+v", first)
	}
	if len(first.Snapshot.Runs) != 1 {
		t.Fatalf("runs %+v: a repository without access must not fail the poll", first.Snapshot.Runs)
	}

	api.mu.Lock()
	// A new slice: the poller holds the last one in its snapshot.
	api.prs.Open = []github.PR{pr(func(p *github.PR) { p.Rollup = github.StatusFailure })}
	api.runs["o/r"] = []github.Run{run(1, github.StatusFailure, nil)}
	api.mu.Unlock()
	p.SetExtraRepos([]string{"o/extra", "o/r"})

	second := next(t, updates)
	if got := kinds(second.Events); !slices.Equal(got, []EventKind{ChecksFailed, RunFailed}) {
		t.Errorf("events %v", got)
	}
	// Newest first across repositories.
	if len(second.Snapshot.Runs) != 2 || second.Snapshot.Runs[0].Repo != "o/extra" {
		t.Errorf("runs %+v", second.Snapshot.Runs)
	}
}

func TestPollerWaitsForGh(t *testing.T) {
	var mu sync.Mutex
	state := auth.Result{State: auth.GhNotInstalled}
	api := &fakeAPI{prs: github.PullRequests{Login: "ada"}}
	p, updates := startPoller(t, api, func() auth.Result { mu.Lock(); defer mu.Unlock(); return state })

	if u := next(t, updates); u.Auth.State != auth.GhNotInstalled || u.Loaded {
		t.Fatalf("update %+v", u)
	}
	mu.Lock()
	state = auth.Result{State: auth.GhNotLoggedIn, Detail: "no oauth token"}
	mu.Unlock()
	p.Refresh()
	if u := next(t, updates); u.Auth.State != auth.GhNotLoggedIn || u.Auth.Detail != "no oauth token" {
		t.Fatalf("update %+v", u)
	}
	mu.Lock()
	state = ready()
	mu.Unlock()
	p.Refresh()
	if u := next(t, updates); u.Auth.State != auth.Ready || !u.Loaded {
		t.Fatalf("update %+v", u)
	}
}

func TestPollerRejectedToken(t *testing.T) {
	api := &fakeAPI{prErr: github.ErrUnauthorized}
	p, updates := startPoller(t, api, ready)

	// gh hands back the token GitHub just rejected: the user must sign in.
	if u := next(t, updates); u.Auth.State != auth.GhNotLoggedIn {
		t.Fatalf("update %+v", u)
	}
	api.mu.Lock()
	api.prErr = nil
	api.mu.Unlock()
	// Check again gives the same token another go.
	p.Refresh()
	if u := next(t, updates); u.Auth.State != auth.Ready || !u.Loaded {
		t.Fatalf("update %+v", u)
	}
}

func TestPollerKeepsSnapshotOnError(t *testing.T) {
	api := &fakeAPI{prs: github.PullRequests{Login: "ada", Open: []github.PR{pr(nil)}}}
	p, updates := startPoller(t, api, ready)
	next(t, updates)

	api.mu.Lock()
	api.prErr = errors.New("boom")
	api.mu.Unlock()
	p.Refresh()
	u := next(t, updates)
	if u.Err == nil || !u.Loaded || len(u.Snapshot.PRs) != 1 {
		t.Errorf("update %+v", u)
	}
}

func TestPollerPauses(t *testing.T) {
	api := &fakeAPI{prs: github.PullRequests{Login: "ada"}}
	p, updates := startPoller(t, api, ready)
	next(t, updates)

	p.SetPaused(true)
	p.Refresh()
	select {
	case u := <-updates:
		t.Fatalf("polled while paused: %+v", u)
	case <-time.After(100 * time.Millisecond):
	}
	p.SetPaused(false)
	next(t, updates)
}

func (f *fakeAPI) takeAsked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	asked := f.asked
	f.asked = nil
	slices.Sort(asked)
	return asked
}

func TestPollerAsksQuietRepositoriesLessOften(t *testing.T) {
	quiet := run(1, github.StatusSuccess, func(r *github.Run) { r.Repo = "o/quiet"; r.UpdatedAt = t0.Add(-2 * time.Hour) })
	api := &fakeAPI{
		prs: github.PullRequests{Login: "ada", RecentRepos: []string{"o/busy", "o/quiet"}},
		runs: map[string][]github.Run{
			"o/busy":  {run(2, github.StatusPending, func(r *github.Run) { r.Repo = "o/busy" })},
			"o/quiet": {quiet},
		},
	}
	p, updates := startPoller(t, api, ready)
	poll := func(why string, want ...string) {
		t.Helper()
		u := next(t, updates)
		if got := api.takeAsked(); !slices.Equal(got, want) {
			t.Errorf("%s: asked %v, want %v", why, got, want)
		}
		// The quiet repository's runs stay in the snapshot all along.
		if len(u.Snapshot.Runs) < 2 {
			t.Errorf("%s: runs %+v", why, u.Snapshot.Runs)
		}
	}
	poll("first poll", "o/busy", "o/quiet")

	// Changing the extra repositories polls without forcing, as the timer
	// does.
	p.SetExtraRepos([]string{"o/new"})
	poll("a new repository, soon after", "o/busy", "o/new")

	api.mu.Lock()
	api.prs.Open = []github.PR{pr(func(p *github.PR) { p.Repo = "o/quiet"; p.Rollup = github.StatusSuccess })}
	api.mu.Unlock()
	p.SetExtraRepos(nil)
	poll("a new head commit", "o/busy", "o/quiet")

	p.Refresh()
	poll("refresh", "o/busy", "o/quiet")

	p.SetExtraRepos([]string{"o/new"})
	poll("still soon after", "o/busy", "o/new")

	api.mu.Lock()
	api.clock = t0.Add(quietInterval)
	api.mu.Unlock()
	p.SetExtraRepos(nil)
	poll("once the interval passed", "o/busy", "o/quiet")
}
