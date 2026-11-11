// Command icons draws the app's icons from resources/whip.svg. Run it from
// the root of the module after changing the drawing:
//
//	go run ./cmd/icons
package main

import (
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"

	"github.com/egoist/mygo/ui"
)

func main() {
	data, err := os.ReadFile("resources/whip.svg")
	if err != nil {
		log.Fatal(err)
	}
	whip, err := ui.ParseSVG(data)
	if err != nil {
		log.Fatal(err)
	}

	// The app icon: a white whip on a rounded square, with the margin
	// macOS icons leave around theirs.
	const size, margin, radius = 1024, 100, 185
	face := ui.Render(func(c *ui.Context) {
		ui.Column(c).Fill().Center().Gradient(ui.Hex("#374151"), ui.Hex("#111827"), 135).Children(func() {
			ui.Icon(c, whip).FontSize(560).TextColor(ui.Hex("#ffffff"))
		})
	}, size, size, 1)
	icon := image.NewNRGBA(face.Bounds())
	for y := range size {
		for x := range size {
			p := face.RGBAAt(x, y)
			a := roundedSquare(float64(x)+0.5, float64(y)+0.5, size, margin, radius)
			icon.SetNRGBA(x, y, color.NRGBA{R: p.R, G: p.G, B: p.B, A: uint8(255*a + 0.5)})
		}
	}
	write("resources/icon.png", icon)

	// The tray icons: the whip alone, black for macOS to tint and blue
	// elsewhere. Drawn white on black, its brightness is its coverage.
	const tray = 44
	mask := ui.Render(func(c *ui.Context) {
		ui.Column(c).Fill().Center().Background(ui.Hex("#000000")).Children(func() {
			ui.Icon(c, whip).FontSize(tray - 4).TextColor(ui.Hex("#ffffff"))
		})
	}, tray, tray, 1)
	for name, ink := range map[string]color.NRGBA{
		"resources/tray.png":       {},
		"resources/tray-color.png": {R: 55, G: 65, B: 81},
	} {
		img := image.NewNRGBA(mask.Bounds())
		for y := range tray {
			for x := range tray {
				ink.A = mask.RGBAAt(x, y).R
				img.SetNRGBA(x, y, ink)
			}
		}
		write(name, img)
	}
}

// roundedSquare is how much of the pixel at (x, y) a rounded square covers,
// from 0 to 1.
func roundedSquare(x, y, size, margin, radius float64) float64 {
	half := size/2 - margin
	dx := math.Max(math.Abs(x-size/2)-(half-radius), 0)
	dy := math.Max(math.Abs(y-size/2)-(half-radius), 0)
	return math.Max(0, math.Min(1, radius-math.Hypot(dx, dy)+0.5))
}

func write(name string, img image.Image) {
	f, err := os.Create(name)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
