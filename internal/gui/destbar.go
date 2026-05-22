package gui

import (
	"context"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"

	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// destBar renders a footer row of small "destination chips" showing
// each upload target's enabled state at a glance. Lives at the bottom
// of the main window; polls config every few seconds so saves in the
// Settings tab reflect immediately.
type destBar struct {
	srv *web.Server

	chips map[string]*destChip
	url   *canvas.Text
	root  fyne.CanvasObject
}

type destChip struct {
	background *canvas.Rectangle
	border     *canvas.Rectangle
	text       *canvas.Text
	container  fyne.CanvasObject
}

func newDestBar(srv *web.Server) *destBar {
	b := &destBar{srv: srv, chips: map[string]*destChip{}}

	row := container.NewHBox()
	for _, name := range []string{"RC", "FC sync", "EDDN", "EDSM", "Inara", "cAPI"} {
		c := newDestChip(name)
		b.chips[name] = c
		row.Add(c.container)
	}

	b.url = canvas.NewText("", edFgDim)
	b.url.TextSize = 11

	left := container.NewHBox(
		labelMutedSmall("Destinations:"),
		row,
	)
	b.root = container.NewPadded(container.NewBorder(nil, nil, left, b.url, layout.NewSpacer()))
	b.update()
	return b
}

func newDestChip(name string) *destChip {
	c := &destChip{}
	c.background = canvas.NewRectangle(edBgInput)
	c.background.CornerRadius = 3
	c.border = canvas.NewRectangle(color.NRGBA{0, 0, 0, 0}) // transparent overlay for visual style; unused for now
	c.border.StrokeWidth = 0
	c.text = canvas.NewText(name, edFgDim)
	c.text.TextSize = 10
	c.text.TextStyle = fyne.TextStyle{Bold: true}
	c.container = container.NewStack(c.background, container.NewPadded(container.NewCenter(c.text)))
	return c
}

func (c *destChip) setEnabled(on bool, color color.Color) {
	if on {
		c.background.FillColor = withAlphaColor(color, 0x33)
		c.text.Color = color
	} else {
		c.background.FillColor = edBgInput
		c.text.Color = edFgDim
	}
	c.background.Refresh()
	c.text.Refresh()
}

// withAlphaColor is the public helper version of the same trick in
// projects.go; we keep one here in image/color terms.
func withAlphaColor(c color.Color, a uint8) color.NRGBA {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: a}
}

func labelMutedSmall(text string) fyne.CanvasObject {
	t := canvas.NewText(text, edFgMuted)
	t.TextSize = 11
	return container.NewPadded(t)
}

func (b *destBar) runLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	b.update()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.update()
		}
	}
}

func (b *destBar) update() {
	cfg := b.srv.Config()
	signedIn, _, _ := b.srv.FrontierStatus()

	// RC is always "on" — we always post depot+contribution updates if
	// the cmdr is present; rcc-key is only for FC writes.
	fyne.Do(func() {
		b.chips["RC"].setEnabled(true, edStatusOK)
		// "FC sync" is on if we have an rcc-key (writes are possible).
		fcWritable := cfg.APIKey != ""
		fcColor := edStatusOK
		if !fcWritable {
			fcColor = edFgDim
		}
		b.chips["FC sync"].setEnabled(fcWritable, fcColor)
		b.chips["EDDN"].setEnabled(cfg.EDDNEnabled, edStatusInfo)
		b.chips["EDSM"].setEnabled(cfg.EDSMEnabled && cfg.EDSMAPIKey != "", edStatusInfo)
		b.chips["Inara"].setEnabled(cfg.InaraEnabled && cfg.InaraAPIKey != "", edStatusInfo)
		b.chips["cAPI"].setEnabled(cfg.FrontierCAPIEnabled && signedIn, edOrange)

		if u := b.srv.URL(); u != "" {
			b.url.Text = u
			b.url.Refresh()
		}
	})
}

func (b *destBar) content() fyne.CanvasObject { return b.root }
