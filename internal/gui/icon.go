package gui

import "fyne.io/fyne/v2"

// appIconSVG is a tiny self-contained SVG used as the window/taskbar
// icon. A simple orange ring on a dark square — mirrors the ED orange
// accent and is recognisable in a taskbar even at 16px.
const appIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <rect width="64" height="64" fill="#14161a"/>
  <circle cx="32" cy="32" r="22" fill="none" stroke="#ff8000" stroke-width="5"/>
  <circle cx="32" cy="32" r="6" fill="#ff8000"/>
</svg>`

// appIcon returns the icon resource for the Fyne window.
func appIcon() fyne.Resource {
	return fyne.NewStaticResource("edcolreport.svg", []byte(appIconSVG))
}
