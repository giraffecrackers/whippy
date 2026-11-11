package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"whippy/internal/auth"
	"whippy/internal/github"
	"whippy/internal/tracker"
)

// popoverView is the panel under the menu bar icon: what needs attention
// first, then what runs, then the rest.
func (a *app) popoverView(c *ui.Context) {
	if c.Shortcut(0, ui.KeyEscape) || c.Shortcut(ui.Cmd, ui.KeyW) {
		a.hidePopover()
	}
	c.OnShortcut(ui.Cmd, ui.KeyQ, a.quit)
	c.OnShortcut(ui.Cmd, ui.KeyR, a.refresh)
	t := a.theme(c)
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Padding(12, 12, 10, 16).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, logo).FontSize(20).TextColor(t.Accent)
			ui.Column(c).Grow(1).Shrink(1).Gap(1).Children(func() {
				ui.Text(c, "Whippy").FontWeight(600)
				if sub := a.byline(); sub != "" {
					ui.Text(c, sub).FontSize(11).TextColor(t.TextMuted).MaxLines(1)
				}
			})
			ui.Button(c, "Refresh").OnClick(a.refresh)
		})
		ui.Box(c).Height(1).Background(hairline(t))

		switch {
		case a.auth.State != auth.Ready:
			a.setupView(c)
		case !a.loaded:
			ui.Row(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Spinner(c).Label("Loading")
				ui.Text(c, "Loading…").TextColor(t.TextMuted)
			})
		default:
			a.popoverLists(c)
		}

		ui.Box(c).Height(1).Background(hairline(t))
		ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Shrink(1).Children(func() { a.problem(c) })
			ui.Button(c, "Open Dashboard").OnClick(a.openDashboard)
			ui.Button(c, "Quit").OnClick(a.quit)
		})
	})
}

func (a *app) popoverLists(c *ui.Context) {
	var attention, running, rest []github.PR
	for _, pr := range a.snap.PRs {
		switch {
		case tracker.NeedsAttention(pr):
			attention = append(attention, pr)
		case pr.Rollup == github.StatusPending:
			running = append(running, pr)
		default:
			rest = append(rest, pr)
		}
	}
	var failedRuns, activeRuns, waitingRuns []github.Run
	for _, r := range a.standaloneRuns() {
		switch {
		case r.Status == github.StatusFailure && !a.dismissed(r):
			failedRuns = append(failedRuns, r)
		case tracker.RunActive(r, a.now()):
			activeRuns = append(activeRuns, r)
		case r.Status == github.StatusPending:
			waitingRuns = append(waitingRuns, r)
		}
	}
	if len(a.snap.PRs)+len(failedRuns)+len(activeRuns)+len(waitingRuns) == 0 {
		ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
			mark(c, "✓", "All clear", toneGreen)
			ui.Text(c, "No open pull requests, and nothing running.").TextColor(c.Theme().TextMuted)
		})
		return
	}
	section := func(title string, tn tone, prs []github.PR, runs []github.Run) {
		if len(prs)+len(runs) == 0 {
			return
		}
		sectionTitle(c, title, tn, len(prs)+len(runs))
		for _, pr := range prs {
			a.prRow(c, pr, false)
		}
		for _, r := range runs {
			a.runRow(c, r, false)
		}
	}
	ui.Scroll(c).Grow(1).Padding(0, 4, 8).Children(func() {
		section("Needs attention", toneRed, attention, failedRuns)
		section("Running", toneAmber, running, activeRuns)
		section("Waiting", toneBlue, nil, waitingRuns)
		section("Open", toneGreen, rest, nil)
	})
}

// byline says whose things these are, and as of when.
func (a *app) byline() string {
	if a.auth.State != auth.Ready {
		return ""
	}
	parts := []string{}
	if a.snap.Login != "" {
		parts = append(parts, a.snap.Login)
	}
	if u := a.updated(); u != "" {
		parts = append(parts, strings.ToLower(u))
	}
	return strings.Join(parts, " · ")
}
