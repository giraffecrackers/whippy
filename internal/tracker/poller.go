package tracker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"whippy/internal/auth"
	"whippy/internal/github"
)

const (
	idleInterval   = 60 * time.Second
	activeInterval = 30 * time.Second
	// authInterval is how often a missing or signed-out gh is looked for
	// again.
	authInterval = 10 * time.Second
	// quietInterval is how often a repository with nothing under way is
	// asked for its runs, and hotWindow how long after its last run's
	// last change it still counts as busy.
	quietInterval = 5 * time.Minute
	hotWindow     = 30 * time.Minute
	recentWindow  = 14 * 24 * time.Hour
	runsWindow    = 24 * time.Hour
	maxParallel   = 6
)

// API is the part of github.Client the poller uses.
type API interface {
	PullRequests(ctx context.Context, recentSince time.Time) (github.PullRequests, error)
	Runs(ctx context.Context, repo, actor string, since time.Time) ([]github.Run, error)
}

// Update is what the poller reports after each attempt.
type Update struct {
	Auth auth.Result
	// Snapshot is the last good one, and Loaded whether there has been one.
	Snapshot Snapshot
	Loaded   bool
	Events   []Event
	// Err is why this attempt failed, where it did.
	Err error
}

type Poller struct {
	Resolve  func(ctx context.Context) auth.Result
	NewAPI   func(token string) API
	OnUpdate func(Update)
	Now      func() time.Time

	mu         sync.Mutex
	extraRepos []string
	paused     bool
	retryToken bool
	forceAll   bool
	wake       chan struct{}
}

// repoState is what the poller keeps of a repository between polls.
type repoState struct {
	runs []github.Run
	// due is when to ask for its runs again.
	due time.Time
}

func NewPoller(onUpdate func(Update)) *Poller {
	return &Poller{
		Resolve:  auth.Resolve,
		NewAPI:   func(token string) API { return github.New(token) },
		OnUpdate: onUpdate,
		Now:      time.Now,
		wake:     make(chan struct{}, 1),
	}
}

// Refresh polls every repository now, and tries again a token GitHub
// rejected.
func (p *Poller) Refresh() {
	p.mu.Lock()
	p.retryToken, p.forceAll = true, true
	p.mu.Unlock()
	p.poke()
}

// SetPaused stops polling, as while the computer sleeps or is offline, or
// starts it again with a poll.
func (p *Poller) SetPaused(paused bool) {
	p.mu.Lock()
	changed := p.paused != paused
	p.paused = paused
	p.mu.Unlock()
	if changed {
		p.poke()
	}
}

// SetExtraRepos sets the repositories to watch for runs beside those of the
// viewer's pull requests.
func (p *Poller) SetExtraRepos(repos []string) {
	p.mu.Lock()
	changed := !slices.Equal(p.extraRepos, repos)
	p.extraRepos = slices.Clone(repos)
	p.mu.Unlock()
	if changed {
		p.poke()
	}
}

func (p *Poller) poke() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// wait returns after d, or sooner when poked. A d of 0 waits for a poke.
func (p *Poller) wait(ctx context.Context, d time.Duration) {
	var timeout <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-ctx.Done():
	case <-p.wake:
	case <-timeout:
	}
}

// Run polls until ctx is done.
func (p *Poller) Run(ctx context.Context) {
	var (
		api      API
		token    string
		rejected string
		snap     Snapshot
		loaded   bool
		repos    = map[string]*repoState{}
	)
	for ctx.Err() == nil {
		p.mu.Lock()
		paused := p.paused
		if p.retryToken {
			p.retryToken, rejected = false, ""
		}
		force := p.forceAll
		p.forceAll = false
		p.mu.Unlock()
		if paused {
			p.wait(ctx, 0)
			continue
		}

		if api == nil {
			res := p.Resolve(ctx)
			if res.State == auth.Ready && res.Token == rejected {
				res = auth.Result{State: auth.GhNotLoggedIn, Detail: "GitHub rejected the token gh holds."}
			}
			if res.State != auth.Ready {
				p.OnUpdate(Update{Auth: res, Snapshot: snap, Loaded: loaded})
				p.wait(ctx, authInterval)
				continue
			}
			api, token = p.NewAPI(res.Token), res.Token
		}

		next, err := p.poll(ctx, api, snap, repos, force)
		if err != nil && force {
			// What was asked for is still owed.
			p.mu.Lock()
			p.forceAll = true
			p.mu.Unlock()
		}
		var limited *github.RateLimitError
		switch {
		case err == nil:
			var events []Event
			if loaded {
				events = Diff(snap, next)
			}
			snap, loaded, rejected = next, true, ""
			p.OnUpdate(Update{Snapshot: snap, Loaded: true, Events: events})
			if snap.Active() {
				p.wait(ctx, activeInterval)
			} else {
				p.wait(ctx, idleInterval)
			}
		case ctx.Err() != nil:
		case errors.Is(err, github.ErrUnauthorized):
			// gh may hold a newer token: ask it again at once.
			api, rejected = nil, token
		case errors.As(err, &limited):
			p.OnUpdate(Update{Snapshot: snap, Loaded: loaded, Err: err})
			p.wait(ctx, max(limited.RetryAt.Sub(p.Now()), activeInterval))
		default:
			p.OnUpdate(Update{Snapshot: snap, Loaded: loaded, Err: err})
			p.wait(ctx, idleInterval)
		}
	}
}

// heads maps each repository to its pull requests and their head commits,
// a change of which means new runs there.
func heads(prs []github.PR) map[string]string {
	out := map[string]string{}
	for _, pr := range prs {
		out[pr.Repo] += fmt.Sprintf("%d@%s ", pr.Number, pr.HeadSHA)
	}
	return out
}

// poll asks for the pull requests, and for the runs of the repositories
// that are due: all of them when forced, else the busy ones each time and
// the quiet ones every quietInterval, or sooner when a pull request there
// has a new head commit. The others keep the runs of their last answer.
func (p *Poller) poll(ctx context.Context, api API, prev Snapshot, states map[string]*repoState, force bool) (Snapshot, error) {
	now := p.Now()
	prs, err := api.PullRequests(ctx, now.Add(-recentWindow))
	if err != nil {
		return Snapshot{}, err
	}
	p.mu.Lock()
	repos := slices.Concat(prs.RecentRepos, p.extraRepos)
	p.mu.Unlock()
	sort.Strings(repos)
	repos = slices.Compact(repos)

	before, after := heads(prev.PRs), heads(prs.Open)
	building := map[string]bool{}
	for _, pr := range prs.Open {
		if prActive(pr, now) {
			building[pr.Repo] = true
		}
	}
	for repo := range states {
		if !slices.Contains(repos, repo) {
			delete(states, repo)
		}
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		slots    = make(chan struct{}, maxParallel)
	)
	for _, repo := range repos {
		st := states[repo]
		if st == nil {
			st = &repoState{}
			states[repo] = st
		} else if !force && now.Before(st.due) && before[repo] == after[repo] {
			continue
		}
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			got, err := api.Runs(ctx, repo, prs.Login, now.Add(-runsWindow))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				st.runs = got
				hot := building[repo]
				for _, r := range got {
					hot = hot || RunActive(r, now) || now.Sub(r.UpdatedAt) < hotWindow
				}
				if hot {
					st.due = now
				} else {
					st.due = now.Add(quietInterval)
				}
			case errors.Is(err, github.ErrNoAccess):
				// The token reads this repository's pull requests but not
				// its Actions: show the rest.
				st.runs, st.due = nil, now.Add(quietInterval)
			case firstErr == nil:
				firstErr = err
			}
		})
	}
	wg.Wait()
	if firstErr != nil {
		return Snapshot{}, firstErr
	}
	var runs []github.Run
	for _, repo := range repos {
		runs = append(runs, states[repo].runs...)
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	return Snapshot{Login: prs.Login, PRs: prs.Open, Runs: runs, Closed: prs.Closed, FetchedAt: now}, nil
}
