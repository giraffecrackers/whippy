package main

import (
	"fmt"
	"regexp"

	"github.com/egoist/mygo/ui"
)

// modes are the values of Settings.Appearance, in the order the settings
// show them.
var modes = []string{"", "light", "dark"}

var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// parseColor reads a color of the settings; an empty or damaged one is
// none.
func parseColor(s string) (ui.Color, bool) {
	if !colorPattern.MatchString(s) {
		return ui.Color{}, false
	}
	return ui.Hex(s), true
}

func hex(c ui.Color) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// light reports whether dark text reads better on c than white.
func light(c ui.Color) bool {
	return 0.299*float32(c.R)+0.587*float32(c.G)+0.114*float32(c.B) > 150
}

// inkOn is the text color for a fill of the user's choosing.
func inkOn(c ui.Color) ui.Color {
	if light(c) {
		return ui.Hex("#18181b")
	}
	return ui.Hex("#ffffff")
}

// theme gives the frame the user's colors over the system's theme, and
// returns it. Views call it before they build anything.
func (a *app) theme(c *ui.Context) *ui.Theme {
	accent, hasAccent := parseColor(a.settings.AccentColor)
	bg, hasBG := parseColor(a.settings.BackgroundColor)
	if !hasAccent && !hasBG {
		return c.Theme()
	}
	t := *c.Theme()
	if hasAccent {
		// Hovered and pressed move toward the far end from the theme's
		// background, as the theme's own do.
		toward := ui.Hex("#000000")
		if t.Dark {
			toward = ui.Hex("#ffffff")
		}
		t.Accent, t.AccentText = accent, inkOn(accent)
		t.AccentHover, t.AccentPressed = accent.Mix(toward, 0.12), accent.Mix(toward, 0.24)
		t.Focus, t.Selection = accent.Alpha(0.55), accent.Alpha(0.25)
	}
	if hasBG {
		// The faces and borders of controls keep their distance from the
		// background.
		t.Background = bg
		t.Surface = t.Text.Alpha(0.05).Over(bg)
		t.SurfaceHover = t.Text.Alpha(0.09).Over(bg)
		t.SurfacePressed = t.Text.Alpha(0.13).Over(bg)
		t.Border = t.Text.Alpha(0.16).Over(bg)
	}
	c.SetTheme(&t)
	return &t
}
