package menubar

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"fyne.io/systray"
)

// IconState picks which procedural variant renderIcon emits. The
// controller drives transitions via menuView.applyIconState; the
// real implementation (menuItems.applyIconState) hands the bytes
// to systray.SetTemplateIcon for an immediate redraw.
type IconState int

const (
	IconStateLoggedOut IconState = iota // outlined "E" — muted
	IconStateLoggedIn                   // filled "E" — default
	IconStateAlert                      // filled "E" + corner dot
)

// renderIcon emits a 44x44 black "E" template PNG. macOS treats it
// as a template image and tints automatically for light / dark mode;
// the alpha channel is what matters. All three variants share the
// same bounding box so the menu-bar slot doesn't shift width.
//
// Placeholder artwork until a designer-made glyph lands (tracked in
// GOAL.md "Still out of scope").
func renderIcon(state IconState) []byte {
	const (
		size  = 44
		pad   = 8 // edge inset
		thick = 6 // bar thickness for filled variants
	)
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{0, 0, 0, 0}), image.Point{}, draw.Src)
	black := image.NewUniform(color.RGBA{0, 0, 0, 255})
	drawBar := func(x0, y0, x1, y1 int) {
		draw.Draw(img, image.Rect(x0, y0, x1, y1), black, image.Point{}, draw.Src)
	}

	if state == IconStateLoggedOut {
		const stroke = 2
		drawBar(pad, pad, pad+stroke, size-pad)             // left spine
		drawBar(pad, pad, size-pad, pad+stroke)             // top
		drawBar(pad, size-pad-stroke, size-pad, size-pad)   // bottom
		midY := size / 2
		drawBar(pad, midY-stroke/2, size-pad-4, midY+stroke/2) // mid
	} else {
		drawBar(pad, pad, pad+thick, size-pad)              // left spine
		drawBar(pad, pad, size-pad, pad+thick)              // top
		midY := size / 2
		drawBar(pad, midY-thick/2, size-pad-4, midY+thick/2) // mid
		drawBar(pad, size-pad-thick, size-pad, size-pad)    // bottom
	}

	if state == IconStateAlert {
		const (
			dot    = 7
			margin = 4
		)
		drawBar(size-margin-dot, margin, size-margin, margin+dot)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		// stdlib png.Encode on a 44x44 RGBA cannot realistically
		// fail; panic surfaces a programmer error immediately
		// rather than rendering a blank tray.
		panic(err)
	}
	return buf.Bytes()
}

// setSystrayIcon is a package var so tests can stub the actual
// systray write — calling systray.SetTemplateIcon without a live
// systray.Run results in undefined behaviour.
var setSystrayIcon = func(template []byte) {
	systray.SetTemplateIcon(template, template)
}

// applyIconState renders the variant and pushes it to systray.
// menuView interface method.
func (m *menuItems) applyIconState(state IconState) {
	setSystrayIcon(renderIcon(state))
}
