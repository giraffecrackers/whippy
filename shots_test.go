package main

import (
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"whippy/internal/github"
)

// TestShots draws the views to PNG files in the directory SHOTS names, to
// look at a change of styling without opening the app:
//
//	SHOTS=/tmp/shots go test -run TestShots .
func TestShots(t *testing.T) {
	dir := os.Getenv("SHOTS")
	if dir == "" {
		t.Skip("SHOTS is not set")
	}
	a := testApp()
	a.snap.PRs[2].Draft = true
	a.snap.PRs = append(a.snap.PRs, github.PR{Repo: "o/web", Number: 7, Title: "Rework the onboarding flow for new accounts", URL: "u",
		Branch: "onboarding", HeadSHA: "a7", Rollup: github.StatusSuccess, Review: github.ReviewChangesRequested,
		Mergeable: github.MergeableConflicting, UpdatedAt: now.Add(-3 * time.Hour)})
	a.snap.Runs = append(a.snap.Runs,
		github.Run{ID: 12, Repo: "o/api", Workflow: "Release", Title: "Ship it", Branch: "main", Event: "workflow_dispatch", SHA: "m2",
			Status: github.StatusPending, Waiting: true, CreatedAt: now.Add(-time.Hour), StartedAt: now.Add(-time.Hour)},
		github.Run{ID: 13, Repo: "o/api", Workflow: "Nightly", Title: "Build images", Branch: "main", Event: "schedule", SHA: "m3",
			Status: github.StatusPending, CreatedAt: now.Add(-4 * time.Minute), StartedAt: now.Add(-4 * time.Minute)},
		github.Run{ID: 14, Repo: "o/web", Workflow: "Deploy", Title: "Release 3", Branch: "main", Event: "push", SHA: "m4",
			Status: github.StatusSuccess, CreatedAt: now.Add(-20 * time.Hour)},
	)
	shots := []struct {
		name string
		view func(*ui.Context)
		w, h int
		prep func()
	}{
		{"popover", a.popoverView, 420, 480, func() {}},
		{"prs", a.dashboardView, 720, 560, func() { a.tab = tabPRs }},
		{"runs", a.dashboardView, 720, 560, func() { a.tab, a.err = tabRuns, errors.New("rate limited") }},
		{"settings", a.dashboardView, 720, 900, func() { a.tab, a.err = tabSettings, nil }},
		{"custom", a.dashboardView, 720, 560, func() {
			a.tab = tabPRs
			a.settings.AccentColor, a.settings.BackgroundColor, a.settings.TitleBarColor = "#e11d48", "#fff7ed", "#1f2937"
		}},
	}
	for _, s := range shots {
		for _, dark := range []bool{false, true} {
			s.prep()
			tt := ui.NewTester(s.view, s.w, s.h)
			tt.SetScale(2)
			tt.SetDark(dark)
			tt.Frame()
			name := s.name + "-light.png"
			if dark {
				name = s.name + "-dark.png"
			}
			f, err := os.Create(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
}
