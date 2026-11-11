package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"whippy/internal/auth"
	"whippy/internal/github"
	"whippy/internal/tracker"
)

const (
	tabPRs = iota
	tabRuns
	tabSettings
)

// maxRunRows bounds the Runs tab: the rows are built whether in view or not.
const maxRunRows = 200

func (a *app) dashboardView(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyR, a.refresh)
	t := a.theme(c)
	ready := a.auth.State == auth.Ready && a.loaded
	// The window has no title bar of its own: this row is it, under the
	// window's buttons, in the color the user chose for it.
	bar := c.TitleBar()
	barColor, ink, faint := t.Background, t.Text, t.TextMuted
	if v, ok := parseColor(a.settings.TitleBarColor); ok {
		barColor, ink = v, inkOn(v)
		faint = ink.Alpha(0.7)
	}
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Height(max(bar.Height, 48)).Padding(0, bar.Right+20, 0, max(bar.Left+10, 20)).Gap(10).
			AlignItems(ui.Center).Background(barColor).DragWindow().Children(func() {
			ui.Icon(c, logo).FontSize(22).TextColor(t.Accent)
			ui.Text(c, "Whippy").FontSize(15).FontWeight(600).TextColor(ink).Grow(1)
			if ready && a.snap.Login != "" {
				ui.Text(c, a.snap.Login).FontSize(12).TextColor(faint)
				ui.Avatar(c, a.snap.Login, nil).Size(26, 26)
			}
		})
		// The tabs stand on the line under the row; the button keeps clear
		// of it.
		ui.Row(c).Padding(10, 20, 0).Gap(12).AlignItems(ui.End).
			BorderWidth(0, 0, 1, 0).BorderColor(hairline(t)).Children(func() {
			ui.Tabs(c, &a.tab, "Pull requests", "Runs", "Settings")
			ui.Spacer(c)
			ui.Row(c).Gap(12).AlignItems(ui.Center).Padding(0, 0, 10).Children(func() {
				if ready {
					ui.Text(c, a.updated()).FontSize(11).TextColor(t.TextMuted)
				}
				ui.Button(c, "Refresh").OnClick(a.refresh)
			})
		})
		if a.err != nil {
			k := tintOf(t, toneAmber)
			ui.Row(c).Padding(8, 20).Background(k.fill).BorderWidth(0, 0, 1, 0).BorderColor(k.ring).Children(func() {
				a.problem(c)
			})
		}

		switch {
		case a.tab == tabSettings:
			a.settingsTab(c)
		case a.auth.State != auth.Ready:
			a.setupView(c)
		case !a.loaded:
			ui.Row(c).Grow(1).Center().Gap(8).Children(func() {
				ui.Spinner(c).Label("Loading")
				ui.Text(c, "Loading…").TextColor(t.TextMuted)
			})
		case a.tab == tabPRs:
			a.stats(c)
			a.prsTab(c)
		default:
			a.stats(c)
			a.runsTab(c)
		}
	})
}

// stats is the strip of counts over the lists: the numbers stay plain,
// and the note beside each says in color whether it wants anything.
func (a *app) stats(c *ui.Context) {
	t := c.Theme()
	failing, running := a.snap.Counts(a.dismissed)
	waiting := 0
	for _, r := range a.standaloneRuns() {
		if r.Status == github.StatusPending && !tracker.RunActive(r, a.now()) {
			waiting++
		}
	}
	approved := 0
	for _, pr := range a.snap.PRs {
		if pr.Review == github.ReviewApproved {
			approved++
		}
	}
	stat := func(label string, n int, note string, tn tone) {
		ui.Column(c).Grow(1).Basis(0).Gap(4).Padding(14, 20).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(t.TextMuted).Grow(1).MaxLines(1)
				if note != "" {
					ui.Text(c, note).FontSize(11).FontWeight(500).TextColor(tintOf(t, tn).text).SingleLine()
				}
			})
			ui.Text(c, fmt.Sprint(n)).FontSize(28).FontWeight(500).Label(fmt.Sprintf("%s: %d", label, n))
		})
	}
	// note picks what to say of a count: one thing while there are some,
	// another while there are none.
	note := func(n int, some string, tn tone, none string) (string, tone) {
		if n > 0 {
			return some, tn
		}
		return none, toneGreen
	}
	ui.Row(c).AlignItems(ui.Stretch).Gap(1).Dividers(1, hairline(t)).
		BorderWidth(0, 0, 1, 0).BorderColor(hairline(t)).Children(func() {
		s, tn := note(failing, "needs you", toneRed, "all clear")
		stat("Failing", failing, s, tn)
		s, tn = note(running, "in progress", toneAmber, "")
		stat("Running", running, s, tn)
		s, tn = note(waiting, "held up", toneBlue, "")
		stat("Waiting", waiting, s, tn)
		s, tn = note(approved, fmt.Sprintf("%d approved", approved), toneGreen, "")
		stat("Open", len(a.snap.PRs), s, tn)
	})
}

func empty(c *ui.Context, text string) {
	ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
		mark(c, "✓", "Nothing here", toneGreen)
		ui.Text(c, text).TextColor(c.Theme().TextMuted)
	})
}

// group lays out the rows under a band, a line between each two.
func group(c *ui.Context, key string, rows func()) {
	ui.Column(c.Key("group:"+key)).Padding(4, 8).Gap(1).Dividers(1, hairline(c.Theme())).Children(rows)
}

func (a *app) prsTab(c *ui.Context) {
	if len(a.snap.PRs) == 0 {
		empty(c, "You have no open pull requests.")
		return
	}
	prs := slices.Clone(a.snap.PRs)
	slices.SortStableFunc(prs, func(x, y github.PR) int { return strings.Compare(x.Repo, y.Repo) })
	ui.Scroll(c).Grow(1).Children(func() {
		for len(prs) > 0 {
			repo := prs[0].Repo
			n := 1
			for n < len(prs) && prs[n].Repo == repo {
				n++
			}
			band(c, repo, n)
			group(c, repo, func() {
				for _, pr := range prs[:n] {
					// A pull request and its checks are one row to the lines.
					ui.Column(c.Key(pr.Key())).Children(func() { a.prRow(c, pr, true) })
				}
			})
			prs = prs[n:]
		}
	})
}

// day names the day a run started on, as today sees it.
func (a *app) day(at time.Time) string {
	now := a.now()
	at = at.In(now.Location())
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch {
	case !at.Before(midnight):
		return "Today"
	case !at.Before(midnight.AddDate(0, 0, -1)):
		return "Yesterday"
	}
	return at.Format("Monday 2 January")
}

func (a *app) runsTab(c *ui.Context) {
	runs := a.snap.Runs
	if len(runs) == 0 {
		empty(c, "No workflow runs of yours in the last day.")
		return
	}
	runs = runs[:min(len(runs), maxRunRows)]
	ui.Scroll(c).Grow(1).Children(func() {
		for len(runs) > 0 {
			day := a.day(runs[0].CreatedAt)
			n := 1
			for n < len(runs) && a.day(runs[n].CreatedAt) == day {
				n++
			}
			band(c, day, n)
			group(c, day, func() {
				for _, r := range runs[:n] {
					a.runRow(c, r, true)
				}
			})
			runs = runs[n:]
		}
	})
}

func (a *app) settingsTab(c *ui.Context) {
	t := c.Theme() // the user's, set by dashboardView
	// A field's label names its switch, and toggles it when clicked.
	toggle := func(label string, on *bool, changed func()) {
		ui.Field(c, label, func() { ui.Switch(c, on).OnChange(changed) })
	}
	// card is a titled group of settings in a bordered box.
	card := func(title, hint string, body func()) {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(600)
			if hint != "" {
				ui.Text(c, hint).FontSize(11).TextColor(t.TextMuted)
			}
			ui.Column(c).Gap(10).Padding(14, 16).Radius(10).Border(1, hairline(t)).
				Background(t.Text.Alpha(0.02)).Children(body)
		})
	}
	// color is a well for one of the theme's colors, and a way back to the
	// theme's own.
	color := func(label string, well *ui.Color, saved *string, theme ui.Color) {
		ui.Field(c, label, func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				v, chosen := parseColor(*saved)
				if !chosen {
					v = theme
				}
				*well = v
				ui.ColorWell(c, well).OnChange(func() {
					*saved = hex(*well)
					a.saveSettings()
				})
				if !chosen {
					ui.Text(c, "Default").FontSize(11).TextColor(t.TextMuted)
				} else {
					ui.Button(c, "Reset").Label("Reset " + label).OnClick(func() {
						*saved = ""
						a.saveSettings()
					})
				}
			})
		})
	}
	a.mode = max(slices.Index(modes, a.settings.Appearance), 0)
	ui.Scroll(c).Grow(1).Padding(20).Gap(22).Children(func() {
		card("Appearance", "The colors apply to this window and the menu bar panel.", func() {
			ui.Form(c, func() {
				ui.Field(c, "Mode", func() {
					ui.Segmented(c, &a.mode, "System", "Light", "Dark").OnChange(func() {
						a.settings.Appearance = modes[a.mode]
						a.setAppearance(a.settings.Appearance)
						a.saveSettings()
					})
				})
				color("Accent", &a.accent, &a.settings.AccentColor, t.Accent)
				color("Background", &a.background, &a.settings.BackgroundColor, t.Background)
				color("Title bar", &a.titleBar, &a.settings.TitleBarColor, t.Background)
			})
		})

		card("Account", "", func() {
			switch {
			case a.auth.State != auth.Ready:
				ui.Text(c, "Not signed in. See the Pull requests tab for how to fix it.").TextColor(tintOf(t, toneAmber).text)
			case a.snap.Login != "":
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Avatar(c, a.snap.Login, nil)
					ui.Text(c, "Signed in as "+a.snap.Login+", through the GitHub CLI.").Grow(1).Shrink(1)
					pill(c, "Connected", toneGreen)
				})
			default:
				ui.Text(c, "Signing in through the GitHub CLI…").TextColor(t.TextMuted)
			}
		})

		card("Notifications", "Failed checks, failed runs, merge conflicts and merges always notify.", func() {
			ui.Form(c, func() {
				toggle("Checks passed", &a.settings.NotifyChecksPassed, a.saveSettings)
				toggle("Runs succeeded", &a.settings.NotifyRunSucceeded, a.saveSettings)
				toggle("Reviews", &a.settings.NotifyReviews, a.saveSettings)
			})
			if a.notifyDenied {
				ui.Text(c, "Notifications are turned off for this app in System Settings.").FontSize(11).TextColor(tintOf(t, toneAmber).text)
			}
		})

		card("General", "", func() {
			ui.Form(c, func() {
				toggle("Launch at login", &a.openAtLogin, func() {
					a.loginErr = ""
					if err := a.setOpenAtLogin(a.openAtLogin); err != nil {
						a.openAtLogin, a.loginErr = !a.openAtLogin, err.Error()
					}
				})
			})
			if a.loginErr != "" {
				ui.Text(c, a.loginErr).FontSize(11).TextColor(tintOf(t, toneRed).text)
			}
		})

		card("Extra repositories", "Runs are found in the repositories of your recent pull requests. Add others to watch here.", func() {
			for _, repo := range a.settings.ExtraRepos {
				ui.Row(c.Key("repo:" + repo)).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, repo).Grow(1)
					ui.Button(c, "Remove").Label("Remove " + repo).OnClick(func() { a.removeRepo(repo) })
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &a.newRepo).Placeholder("owner/name").Label("Repository").Grow(1).OnSubmit(a.addRepo)
				ui.Button(c, "Add").Label("Add repository").OnClick(a.addRepo)
			})
			if a.repoErr != "" {
				ui.Text(c, a.repoErr).FontSize(11).TextColor(tintOf(t, toneRed).text)
			}
		})
	})
}
