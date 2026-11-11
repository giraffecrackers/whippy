package main

import (
	"github.com/egoist/mygo/ui"

	"whippy/internal/auth"
)

// setupView takes the place of the lists while gh is missing or signed
// out: the app has no other way to reach GitHub, so it says how to fix gh.
func (a *app) setupView(c *ui.Context) {
	t := c.Theme()
	title, body, command := "GitHub CLI not found",
		"Whippy signs in through the GitHub CLI (gh). Install it, then sign in with gh auth login.",
		"brew install gh"
	if a.auth.State == auth.GhNotLoggedIn {
		title, body, command = "GitHub CLI is not signed in",
			"Whippy uses the account the GitHub CLI (gh) is signed in to. Run this in a terminal:",
			"gh auth login"
	}
	ui.Column(c).Grow(1).Center().Gap(12).Padding(24).Children(func() {
		mark(c, "!", "Needs fixing", toneAmber)
		ui.Text(c, title).FontSize(18).FontWeight(600).TextAlign(ui.Center)
		ui.Text(c, body).TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(360)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, command).Font("monospace").Selectable().
				Padding(6, 10).Radius(t.Radius).Background(t.Surface).Border(1, t.Border)
			services := c.Services()
			ui.Button(c, "Copy").Label("Copy command").OnClick(func() { services.WriteClipboard(command) })
		})
		if a.auth.State == auth.GhNotInstalled {
			ui.Link(c, "Other ways to install", "https://cli.github.com")
		}
		if a.auth.Detail != "" {
			ui.Text(c, a.auth.Detail).FontSize(11).TextColor(t.TextMuted).TextAlign(ui.Center).MaxWidth(360)
		}
		ui.PrimaryButton(c, "Check again").OnClick(a.refresh)
		ui.Text(c, "Checked again every few seconds.").FontSize(11).TextColor(t.TextMuted)
	})
}
