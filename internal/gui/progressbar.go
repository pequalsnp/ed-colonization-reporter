package gui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

// gradientProgressBar is a Fyne widget that draws a horizontal progress
// bar coloured red→orange→green by the value. Fyne's stock
// widget.ProgressBar is themed via theme.ColorNamePrimary, so we can't
// tint individual instances; this widget renders the bar from scratch
// to gain per-instance colour and avoid the slightly-cartoonish default
// blue-on-grey look.
type gradientProgressBar struct {
	widget.BaseWidget
	value float64 // [0,1]
}

func newGradientProgressBar(value float64) *gradientProgressBar {
	if value < 0 {
		value = 0
	}
	if value > 1 {
		value = 1
	}
	p := &gradientProgressBar{value: value}
	p.ExtendBaseWidget(p)
	return p
}

// SetValue updates the bar; callers must invoke Refresh or rely on
// container.Refresh.
func (p *gradientProgressBar) SetValue(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	p.value = v
	p.Refresh()
}

// CreateRenderer implements fyne.Widget.
func (p *gradientProgressBar) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(edBgInput)
	bg.CornerRadius = 3
	bg.StrokeColor = edBorder
	bg.StrokeWidth = 1

	fg := canvas.NewRectangle(progressColor(p.value))
	fg.CornerRadius = 3

	label := canvas.NewText(fmt.Sprintf("%.0f%%", p.value*100), edFg)
	label.TextSize = 11
	label.TextStyle = fyne.TextStyle{Bold: true}
	label.Alignment = fyne.TextAlignCenter

	return &gradientProgressRenderer{bar: p, bg: bg, fg: fg, label: label}
}

type gradientProgressRenderer struct {
	bar   *gradientProgressBar
	bg    *canvas.Rectangle
	fg    *canvas.Rectangle
	label *canvas.Text
}

func (r *gradientProgressRenderer) MinSize() fyne.Size {
	return fyne.NewSize(120, 20)
}

func (r *gradientProgressRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	fillWidth := size.Width * float32(r.bar.value)
	r.fg.Move(fyne.NewPos(0, 0))
	r.fg.Resize(fyne.NewSize(fillWidth, size.Height))
	// Center the label vertically by computing approximate text height.
	textH := float32(14)
	r.label.Resize(fyne.NewSize(size.Width, textH))
	r.label.Move(fyne.NewPos(0, (size.Height-textH)/2))
}

func (r *gradientProgressRenderer) Refresh() {
	r.fg.FillColor = progressColor(r.bar.value)
	bgSize := r.bg.Size()
	fillWidth := bgSize.Width * float32(r.bar.value)
	r.fg.Resize(fyne.NewSize(fillWidth, bgSize.Height))
	r.label.Text = fmt.Sprintf("%.0f%%", r.bar.value*100)
	r.bg.Refresh()
	r.fg.Refresh()
	r.label.Refresh()
}

func (r *gradientProgressRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.bg, r.fg, r.label}
}

func (r *gradientProgressRenderer) Destroy() {}

// progressColor returns the bar's fill colour for a given value in [0,1].
// Smooth interpolation across red→orange→yellow-green→green so the bar
// gets visibly "healthier" as builds approach completion.
func progressColor(v float64) color.NRGBA {
	if v <= 0 {
		return color.NRGBA{0xc0, 0x40, 0x40, 0xff}
	}
	if v >= 1 {
		return color.NRGBA{0x4a, 0xc0, 0x6a, 0xff}
	}
	// Two linear segments: red(0)→orange(0.5)→green(1).
	// Picked deliberately muted so the chunky-bright primary colours of
	// the underlying canvas don't compete with the orange-accented chrome.
	type rgb struct{ R, G, B uint8 }
	red := rgb{0xc0, 0x40, 0x40}
	mid := rgb{0xf0, 0xa0, 0x40} // amber
	grn := rgb{0x4a, 0xc0, 0x6a}
	lerp := func(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }
	if v < 0.5 {
		t := v / 0.5
		return color.NRGBA{lerp(red.R, mid.R, t), lerp(red.G, mid.G, t), lerp(red.B, mid.B, t), 0xff}
	}
	t := (v - 0.5) / 0.5
	return color.NRGBA{lerp(mid.R, grn.R, t), lerp(mid.G, grn.G, t), lerp(mid.B, grn.B, t), 0xff}
}
