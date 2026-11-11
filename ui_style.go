package main

import (
	_ "embed"

	"github.com/egoist/mygo/ui"

	"whippy/internal/github"
)

// tone is what a color says: the app colors by meaning, not by widget.
type tone int

const (
	toneNeutral tone = iota
	toneGreen
	toneRed
	toneAmber
	toneBlue
)

// tint is a tone's colors: a faint fill with a hairline ring around it,
// text that reads on the fill, and a solid for dots.
type tint struct {
	fill, ring, text, solid ui.Color
}

// shades of a tone, light to dark: 50, 400, 500, 600 and 700.
var shades = map[tone][5]string{
	toneGreen: {"#f0fdf4", "#4ade80", "#22c55e", "#16a34a", "#15803d"},
	toneRed:   {"#fef2f2", "#f87171", "#ef4444", "#dc2626", "#b91c1c"},
	toneAmber: {"#fffbeb", "#fbbf24", "#f59e0b", "#d97706", "#b45309"},
	toneBlue:  {"#eff6ff", "#60a5fa", "#3b82f6", "#2563eb", "#1d4ed8"},
}

func tintOf(t *ui.Theme, tn tone) tint {
	s, ok := shades[tn]
	switch {
	case !ok:
		return tint{fill: t.Text.Alpha(0.05), ring: t.Text.Alpha(0.1), text: t.TextMuted, solid: t.TextMuted.Alpha(0.7)}
	case t.Dark:
		return tint{fill: ui.Hex(s[2]).Alpha(0.12), ring: ui.Hex(s[2]).Alpha(0.2), text: ui.Hex(s[1]), solid: ui.Hex(s[1])}
	default:
		return tint{fill: ui.Hex(s[0]), ring: ui.Hex(s[3]).Alpha(0.2), text: ui.Hex(s[4]), solid: ui.Hex(s[2])}
	}
}

// hairline separates rows and regions, fainter than a control's border.
func hairline(t *ui.Theme) ui.Color { return t.Text.Alpha(0.08) }

// pill is a short word about a row, in the color of what it says.
func pill(c *ui.Context, text string, tn tone) {
	k := tintOf(c.Theme(), tn)
	ui.Box(c).Padding(2, 7).Radius(6).Background(k.fill).Border(1, k.ring).Shrink(0).Children(func() {
		ui.Text(c, text).FontSize(11).FontWeight(500).TextColor(k.text).SingleLine()
	})
}

// dot is a small solid mark of a tone.
func dot(c *ui.Context, tn tone) {
	ui.Box(c).Size(6, 6).Radius(3).Background(tintOf(c.Theme(), tn).solid).Shrink(0)
}

// mark is a glyph in a round tinted spot, at the head of a row. The glyph
// "●" is drawn as a dot, which no font misplaces.
func mark(c *ui.Context, glyph, label string, tn tone) {
	k := tintOf(c.Theme(), tn)
	ui.Box(c).Size(22, 22).Radius(11).Background(k.fill).Border(1, k.ring).Shrink(0).Center().Label(label).Children(func() {
		if glyph == "●" {
			ui.Box(c).Size(8, 8).Radius(4).Background(k.solid)
			return
		}
		ui.Text(c, glyph).FontSize(11).Bold().TextColor(k.text)
	})
}

// statusLook is how a status shows: its glyph, its name and its tone.
func statusLook(s github.Status) (glyph, label string, tn tone) {
	switch s {
	case github.StatusSuccess:
		return "✓", "Passed", toneGreen
	case github.StatusFailure:
		return "✕", "Failed", toneRed
	case github.StatusPending:
		return "●", "Running", toneAmber
	case github.StatusSkipped:
		return "–", "Skipped", toneNeutral
	}
	return "○", "No checks", toneNeutral
}

func statusMark(c *ui.Context, s github.Status) {
	glyph, label, tn := statusLook(s)
	mark(c, glyph, label, tn)
}

//go:embed resources/whip.svg
var whipSVG []byte

var logo = ui.MustParseSVG(whipSVG)
