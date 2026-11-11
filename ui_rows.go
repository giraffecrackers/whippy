package main

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"whippy/internal/github"
	"whippy/internal/tracker"
)

// sectionTitle heads a list in the popover: a dot of the section's tone,
// its name, and how many it holds.
func sectionTitle(c *ui.Context, title string, tn tone, count int) {
	t := c.Theme()
	ui.Row(c).Gap(10).Padding(12, 8, 4, 16).AlignItems(ui.Center).Children(func() {
		dot(c, tn)
		ui.Text(c, strings.ToUpper(title)).FontSize(10).Bold().LetterSpacing(0.4).TextColor(t.TextMuted)
		ui.Text(c, fmt.Sprint(count)).FontSize(10).TextColor(t.TextMuted.Alpha(0.7))
	})
}

// band heads a group in the dashboard, across the whole width, and stays
// at the top while its group scrolls under it.
func band(c *ui.Context, title string, count int) {
	t := c.Theme()
	ui.Row(c.Key("band:"+title)).Gap(8).Padding(6, 20).AlignItems(ui.Center).
		Background(t.Surface).BorderWidth(0, 0, 1, 0).BorderColor(hairline(t)).
		Sticky().Top(0).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(600).Grow(1)
		ui.Text(c, fmt.Sprint(count)).FontSize(11).TextColor(t.TextMuted)
	})
}

// clickableRow is a row that opens a URL, lit while the pointer is over it.
func (a *app) clickableRow(c *ui.Context, key, url string, roomy bool, children func()) {
	t := c.Theme()
	row := ui.Row(c.Key(key)).Gap(10).Padding(6, 8).Radius(8).AlignItems(ui.Center).
		OnClick(func() { a.openURL(url) })
	if roomy {
		row.Padding(10, 12)
	}
	if row.Hovered() {
		row.Background(t.Text.Alpha(0.05))
	}
	row.Children(children)
}

// prRow shows a pull request. With details, as in the dashboard, it says
// its state in words and the checks that failed or still run follow it.
func (a *app) prRow(c *ui.Context, pr github.PR, details bool) {
	t := c.Theme()
	a.clickableRow(c, "pr:"+pr.Key(), pr.URL, details, func() {
		statusMark(c, pr.Rollup)
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			ui.Text(c, pr.Title).FontWeight(500).MaxLines(1)
			sub := fmt.Sprintf("%s · %s", pr.Key(), pr.Branch)
			if details {
				sub = fmt.Sprintf("#%d · %s", pr.Number, pr.Branch)
			}
			if ago := a.ago(pr.UpdatedAt); ago != "" {
				sub += " · " + ago
			}
			ui.Text(c, sub).FontSize(11).TextColor(t.TextMuted).MaxLines(1)
		})
		if pr.Draft {
			pill(c, "Draft", toneNeutral)
		}
		if pr.Mergeable == github.MergeableConflicting {
			pill(c, "Conflict", toneRed)
		}
		switch pr.Review {
		case github.ReviewApproved:
			pill(c, "Approved", toneGreen)
		case github.ReviewChangesRequested:
			pill(c, "Changes requested", toneRed)
		case github.ReviewRequired:
			if details {
				pill(c, "Review required", toneNeutral)
			}
		}
	})
	if !details {
		return
	}
	// A token that may not read check runs leaves Checks empty: the
	// workflow runs of the head commit stand in for them.
	checks := pr.Checks
	if len(checks) == 0 {
		for _, r := range a.snap.RunsFor(pr) {
			checks = append(checks, github.Check{Name: r.Workflow, Status: r.Status, URL: r.URL})
		}
	}
	for i, check := range checks {
		if check.Status != github.StatusFailure && check.Status != github.StatusPending {
			continue
		}
		_, label, tn := statusLook(check.Status)
		// Under the title, past the row's mark.
		ui.Row(c.Key(fmt.Sprintf("check:%s:%d", pr.Key(), i))).Gap(8).Padding(0, 12, 6, 44).AlignItems(ui.Center).Children(func() {
			dot(c, tn)
			if check.URL != "" {
				ui.Link(c, check.Name, check.URL).FontSize(12)
			} else {
				ui.Text(c, check.Name).FontSize(12)
			}
			ui.Text(c, strings.ToLower(label)).FontSize(11).TextColor(tintOf(t, tn).text)
		})
	}
}

// runRow shows a workflow run. With details, as in the dashboard, it says
// its state in words; without, a failed one has a button that takes it out
// of what needs attention.
func (a *app) runRow(c *ui.Context, r github.Run, details bool) {
	t := c.Theme()
	// Pending, but not under way: waiting for an approval, or stuck.
	idle := r.Status == github.StatusPending && !tracker.RunActive(r, a.now())
	failed := r.Status == github.StatusFailure
	a.clickableRow(c, fmt.Sprintf("run:%s:%d", r.Repo, r.ID), r.URL, details, func() {
		switch {
		case r.Waiting:
			mark(c, "●", "Waiting", toneBlue)
		case idle:
			mark(c, "●", "Stalled", toneNeutral)
		default:
			statusMark(c, r.Status)
		}
		ui.Column(c).Grow(1).Shrink(1).Gap(2).Children(func() {
			title := r.Workflow
			if r.Title != "" {
				title += " — " + r.Title
			}
			ui.Text(c, title).FontWeight(500).MaxLines(1)
			sub := fmt.Sprintf("%s · %s · %s #%d", r.Repo, r.Branch, r.Event, r.Number)
			if ago := a.ago(r.CreatedAt); ago != "" {
				sub += " · " + ago
			}
			ui.Text(c, sub).FontSize(11).TextColor(t.TextMuted).MaxLines(1)
		})
		switch {
		case r.Cancelled:
			pill(c, "Cancelled", toneNeutral)
		case r.Waiting:
			pill(c, "Waiting", toneBlue)
		case idle:
			pill(c, "Stalled", toneNeutral)
		case failed && a.dismissed(r):
			pill(c, "Dismissed", toneNeutral)
		case details:
			_, label, tn := statusLook(r.Status)
			pill(c, label, tn)
		case failed:
			ui.Button(c, "Dismiss").Label("Dismiss " + r.Workflow).OnClick(func() { a.dismiss(r) })
		}
	})
}

// problem shows why the last poll failed, beside the data of the one
// before.
func (a *app) problem(c *ui.Context) {
	if a.err != nil {
		ui.Text(c, "Last refresh failed: "+a.err.Error()).FontSize(11).TextColor(tintOf(c.Theme(), toneAmber).text).MaxLines(2)
	}
}

// updated says when the data shown was fetched.
func (a *app) updated() string {
	if a.snap.FetchedAt.IsZero() {
		return ""
	}
	return "Updated " + a.snap.FetchedAt.Local().Format("15:04")
}
