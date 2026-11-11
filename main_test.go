package main

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"whippy/internal/auth"
	"whippy/internal/github"
	"whippy/internal/tracker"
)

var now = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

// testApp is an app showing one of each kind of thing.
func testApp() *app {
	a := newApp()
	a.now = func() time.Time { return now }
	a.loaded = true
	a.snap = tracker.Snapshot{
		Login: "ada",
		PRs: []github.PR{
			{Repo: "o/api", Number: 1, Title: "Broken build", URL: "https://github.com/o/api/pull/1", Branch: "fix", HeadSHA: "a1",
				Rollup: github.StatusFailure, UpdatedAt: now.Add(-5 * time.Minute)},
			{Repo: "o/api", Number: 2, Title: "Still building", URL: "https://github.com/o/api/pull/2", Branch: "feat", HeadSHA: "a2",
				Rollup: github.StatusPending, Checks: []github.Check{
					{Name: "unit", Status: github.StatusSuccess, URL: "https://ci/unit"},
					{Name: "e2e", Status: github.StatusPending, URL: "https://ci/e2e"},
				}},
			{Repo: "o/web", Number: 3, Title: "All good", URL: "https://github.com/o/web/pull/3", Branch: "docs", HeadSHA: "a3",
				Rollup: github.StatusSuccess, Review: github.ReviewApproved},
		},
		Runs: []github.Run{
			{ID: 10, Repo: "o/api", Workflow: "CI", Title: "Broken build", Branch: "fix", Event: "pull_request", SHA: "a1",
				Status: github.StatusFailure, URL: "https://github.com/o/api/actions/runs/10", CreatedAt: now.Add(-6 * time.Minute)},
			{ID: 11, Repo: "o/api", Workflow: "Deploy", Title: "Release 4", Branch: "main", Event: "workflow_dispatch", SHA: "m1",
				Status: github.StatusFailure, URL: "https://github.com/o/api/actions/runs/11", CreatedAt: now.Add(-2 * time.Hour)},
		},
		FetchedAt: now,
	}
	for i := range a.snap.PRs {
		if a.snap.PRs[i].UpdatedAt.IsZero() {
			a.snap.PRs[i].UpdatedAt = now
		}
	}
	return a
}

func index(texts []string, s string) int { return slices.Index(texts, s) }

func TestPopoverOrdersByUrgency(t *testing.T) {
	a := testApp()
	var opened []string
	a.openURL = func(url string) { opened = append(opened, url) }
	tt := ui.NewTester(a.popoverView, 420, 480)

	texts := tt.Texts()
	order := []string{"NEEDS ATTENTION", "Broken build", "Deploy — Release 4", "RUNNING", "Still building", "OPEN", "All good"}
	last := -1
	for _, s := range order {
		i := index(texts, s)
		if i <= last {
			t.Fatalf("%q at %d, after index %d; texts %q", s, i, last, texts)
		}
		last = i
	}
	// The run of a pull request's head commit shows as that pull request.
	if tt.HasText("CI — Broken build") {
		t.Errorf("a pull request's own run is listed apart; texts %q", texts)
	}

	if err := tt.Click("Broken build"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Deploy — Release 4"); err != nil {
		t.Fatal(err)
	}
	want := []string{"https://github.com/o/api/pull/1", "https://github.com/o/api/actions/runs/11"}
	if !slices.Equal(opened, want) {
		t.Errorf("opened %q, want %q", opened, want)
	}
}

func TestDismissFailedRun(t *testing.T) {
	a := testApp()
	// One dismissed long ago, to be forgotten.
	a.settings.DismissedRuns = map[int64]time.Time{99: now.Add(-100 * time.Hour)}
	saved := 0
	a.saveSettings = func() { saved++ }
	var opened []string
	a.openURL = func(url string) { opened = append(opened, url) }
	tt := ui.NewTester(a.popoverView, 420, 480)

	if err := tt.Click("Dismiss Deploy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := a.settings.DismissedRuns[11]; !ok || len(a.settings.DismissedRuns) != 1 || saved != 1 {
		t.Fatalf("dismissed %v, saved %d", a.settings.DismissedRuns, saved)
	}
	// The button does not also open the run.
	if len(opened) != 0 {
		t.Errorf("opened %q", opened)
	}
	if tt.HasText("Deploy — Release 4") {
		t.Errorf("still in the popover; texts %q", tt.Texts())
	}
	if got := a.trayTitle(); got != "✕1 ●1" {
		t.Errorf("title %q", got)
	}

	// It stays in the Runs tab, marked.
	a.tab = tabRuns
	dash := ui.NewTester(a.dashboardView, 720, 560)
	if !dash.HasText("Deploy — Release 4") || !dash.HasText("Dismissed") {
		t.Errorf("texts %q", dash.Texts())
	}
}

func TestWaitingRunsAreSetApart(t *testing.T) {
	a := testApp()
	a.snap.Runs = append(a.snap.Runs,
		github.Run{ID: 12, Repo: "o/api", Workflow: "Release", Title: "Ship it", Branch: "main", SHA: "m2",
			Status: github.StatusPending, Waiting: true, URL: "https://github.com/o/api/actions/runs/12", CreatedAt: now.Add(-time.Hour)},
		github.Run{ID: 13, Repo: "o/api", Workflow: "Nightly", Title: "Stuck", Branch: "main", SHA: "m3",
			Status: github.StatusPending, URL: "https://github.com/o/api/actions/runs/13", CreatedAt: now.Add(-9 * time.Hour)},
	)
	tt := ui.NewTester(a.popoverView, 420, 560)
	texts := tt.Texts()
	running, waiting := index(texts, "RUNNING"), index(texts, "WAITING")
	for _, s := range []string{"Release — Ship it", "Nightly — Stuck"} {
		if i := index(texts, s); waiting < 0 || i < waiting || i < running {
			t.Errorf("%q at %d, WAITING at %d; texts %q", s, i, waiting, texts)
		}
	}
	if !tt.HasText("Waiting") || !tt.HasText("Stalled") {
		t.Errorf("badges missing; texts %q", texts)
	}
	// Neither counts as running.
	if got := a.trayTitle(); got != "✕2 ●1" {
		t.Errorf("title %q", got)
	}
}

func TestPopoverButtons(t *testing.T) {
	a := testApp()
	var refreshed, dashboard int
	a.refresh = func() { refreshed++ }
	a.openDashboard = func() { dashboard++ }
	tt := ui.NewTester(a.popoverView, 420, 480)
	if err := tt.Click("Refresh"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Open Dashboard"); err != nil {
		t.Fatal(err)
	}
	if refreshed != 1 || dashboard != 1 {
		t.Errorf("refreshed %d, dashboard %d", refreshed, dashboard)
	}
}

func TestSetupScreens(t *testing.T) {
	tests := []struct {
		name    string
		auth    auth.Result
		title   string
		command string
	}{
		{"gh missing", auth.Result{State: auth.GhNotInstalled}, "GitHub CLI not found", "brew install gh"},
		{"gh signed out", auth.Result{State: auth.GhNotLoggedIn, Detail: "no oauth token found"}, "GitHub CLI is not signed in", "gh auth login"},
	}
	for _, tc := range tests {
		for _, view := range []string{"popover", "dashboard"} {
			t.Run(tc.name+" in "+view, func(t *testing.T) {
				a := testApp()
				a.auth = tc.auth
				checked := 0
				a.refresh = func() { checked++ }
				fn := a.popoverView
				if view == "dashboard" {
					fn = a.dashboardView
				}
				tt := ui.NewTester(fn, 520, 520)
				if !tt.HasText(tc.title) || !tt.HasText(tc.command) {
					t.Fatalf("texts %q", tt.Texts())
				}
				// The lists give way to the screen: stale data is not shown
				// as current.
				if tt.HasText("Broken build") {
					t.Errorf("lists shown beside the setup screen")
				}
				if tc.auth.Detail != "" && !tt.HasText(tc.auth.Detail) {
					t.Errorf("gh's message missing; texts %q", tt.Texts())
				}
				if err := tt.Click("Check again"); err != nil {
					t.Fatal(err)
				}
				if checked != 1 {
					t.Errorf("checked again %d times", checked)
				}
			})
		}
	}
}

func TestDashboardPullRequests(t *testing.T) {
	a := testApp()
	tt := ui.NewTester(a.dashboardView, 720, 560)
	texts := tt.Texts()
	for _, s := range []string{"o/api", "o/web", "Broken build", "All good", "Approved"} {
		if index(texts, s) < 0 {
			t.Errorf("%q missing; texts %q", s, texts)
		}
	}
	// Checks that still run are listed; those that passed are not. Without
	// check runs, the head commit's workflow runs stand in.
	if !tt.HasText("e2e") || tt.HasText("unit") {
		t.Errorf("checks of #2; texts %q", texts)
	}
	if !tt.HasText("CI") {
		t.Errorf("the failed run of #1 is missing; texts %q", texts)
	}
}

func TestDashboardRunsAndProblem(t *testing.T) {
	a := testApp()
	a.tab = tabRuns
	a.err = errors.New("boom")
	tt := ui.NewTester(a.dashboardView, 720, 560)
	for _, s := range []string{"CI — Broken build", "Deploy — Release 4", "Last refresh failed: boom"} {
		if !tt.HasText(s) {
			t.Errorf("%q missing; texts %q", s, tt.Texts())
		}
	}
}

func TestSettings(t *testing.T) {
	a := testApp()
	a.tab = tabSettings
	saved := 0
	a.saveSettings = func() { saved++ }
	var login []bool
	a.setOpenAtLogin = func(on bool) error { login = append(login, on); return nil }
	tt := ui.NewTester(a.dashboardView, 720, 1000)

	if err := tt.Click("Runs succeeded"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.NotifyRunSucceeded || saved != 1 {
		t.Errorf("run notifications %v, saved %d", a.settings.NotifyRunSucceeded, saved)
	}

	if err := tt.Click("Launch at login"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(login, []bool{true}) || !a.openAtLogin {
		t.Errorf("launch at login asked %v, shown %v", login, a.openAtLogin)
	}

	if err := tt.Click("Repository"); err != nil {
		t.Fatal(err)
	}
	tt.Type("not a repo")
	if err := tt.Click("Add repository"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.settings.ExtraRepos) != 0 || !tt.HasText("Write it as owner/name.") {
		t.Fatalf("repos %v, texts %q", a.settings.ExtraRepos, tt.Texts())
	}
	a.newRepo = ""
	tt.Frame()
	if err := tt.Click("Repository"); err != nil {
		t.Fatal(err)
	}
	tt.Type("https://github.com/o/infra/")
	if err := tt.Click("Add repository"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !slices.Equal(a.settings.ExtraRepos, []string{"o/infra"}) || saved != 2 || !tt.HasText("o/infra") {
		t.Fatalf("repos %v, saved %d, texts %q", a.settings.ExtraRepos, saved, tt.Texts())
	}
	if err := tt.Click("Remove o/infra"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.settings.ExtraRepos) != 0 || saved != 3 {
		t.Errorf("repos %v, saved %d", a.settings.ExtraRepos, saved)
	}
}

func TestLaunchAtLoginFailureIsShown(t *testing.T) {
	a := testApp()
	a.tab = tabSettings
	a.setOpenAtLogin = func(bool) error { return errors.New("not allowed") }
	tt := ui.NewTester(a.dashboardView, 720, 1000)
	if err := tt.Click("Launch at login"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.openAtLogin || !tt.HasText("not allowed") {
		t.Errorf("shown on %v, texts %q", a.openAtLogin, tt.Texts())
	}
}

func TestTrayTitle(t *testing.T) {
	a := testApp()
	// #1 and the Deploy run fail; #2 runs.
	if got := a.trayTitle(); got != "✕2 ●1" {
		t.Errorf("title %q", got)
	}
	a.snap = tracker.Snapshot{}
	if got := a.trayTitle(); got != "" {
		t.Errorf("title %q with nothing to show", got)
	}
	a.auth.State = auth.GhNotLoggedIn
	if got := a.trayTitle(); got != "!" {
		t.Errorf("title %q signed out", got)
	}
}

func TestWants(t *testing.T) {
	a := newApp()
	for kind, want := range map[tracker.EventKind]bool{
		tracker.ChecksFailed: true, tracker.ChecksPassed: true, tracker.RunFailed: true, tracker.RunSucceeded: false,
		tracker.Approved: true, tracker.Conflict: true, tracker.Merged: true,
	} {
		if got := a.wants(tracker.Event{Kind: kind}); got != want {
			t.Errorf("kind %v: wants %v", kind, got)
		}
	}
	a.settings.NotifyReviews = false
	if a.wants(tracker.Event{Kind: tracker.ChangesRequested}) {
		t.Error("reviews notify while turned off")
	}
}

func TestAppearanceSettings(t *testing.T) {
	a := testApp()
	a.tab = tabSettings
	saved := 0
	a.saveSettings = func() { saved++ }
	var modes []string
	a.setAppearance = func(mode string) { modes = append(modes, mode) }
	tt := ui.NewTester(a.dashboardView, 720, 900)

	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Appearance != "dark" || !slices.Equal(modes, []string{"dark"}) || saved != 1 {
		t.Fatalf("appearance %q, set %q, saved %d", a.settings.Appearance, modes, saved)
	}

	// A color of the user's replaces the theme's, and Reset brings the
	// theme's back.
	a.settings.AccentColor, a.settings.TitleBarColor = "#e11d48", "not a color"
	tt.Frame()
	if a.accent != ui.Hex("#e11d48") {
		t.Errorf("accent well shows %v", a.accent)
	}
	if tt.HasText("Reset Title bar") {
		t.Errorf("a damaged color counts as chosen")
	}
	if err := tt.Click("Reset Accent"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.AccentColor != "" || saved != 2 || a.accent == ui.Hex("#e11d48") {
		t.Errorf("accent %q, saved %d, well %v", a.settings.AccentColor, saved, a.accent)
	}
}
