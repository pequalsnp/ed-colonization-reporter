package gui

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// destBar renders a footer row of small "destination chips" showing
// each upload target's enabled state at a glance, plus per-destination
// last-post health derived from the statusHub. Lives at the bottom of
// the main window; polls config every few seconds so saves in the
// Settings tab reflect immediately.
type destBar struct {
	srv *web.Server

	chips     map[string]*destChip
	url       *canvas.Text
	lastEvent *canvas.Text
	root      fyne.CanvasObject

	// destHealth tracks the most recent classified outcome per
	// destination name. Updated from the statusHub subscription;
	// read by update().
	healthMu sync.Mutex
	health   map[string]destHealth
}

type destHealth struct {
	LastOK  time.Time
	LastErr time.Time
}

type destChip struct {
	background *canvas.Rectangle
	border     *canvas.Rectangle
	text       *canvas.Text
	container  fyne.CanvasObject
}

func newDestBar(srv *web.Server) *destBar {
	b := &destBar{srv: srv, chips: map[string]*destChip{}, health: map[string]destHealth{}}

	row := container.NewHBox()
	for _, name := range []string{"RC", "FC sync", "EDDN", "EDSM", "Inara"} {
		c := newDestChip(name)
		b.chips[name] = c
		row.Add(c.container)
	}

	b.url = canvas.NewText("", edFgDim)
	b.url.TextSize = 11

	b.lastEvent = canvas.NewText("Last event: —", edFgDim)
	b.lastEvent.TextSize = 11

	left := container.NewHBox(
		labelMutedSmall("Destinations:"),
		row,
	)
	right := container.NewHBox(b.lastEvent, widget.NewSeparator(), b.url)
	b.root = container.NewPadded(container.NewBorder(nil, nil, left, right, layout.NewSpacer()))
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
	// Subscribe to the status hub so we can classify per-destination
	// outcomes from message prefixes.
	statusCh, cancelSub := b.srv.Subscribe()
	defer cancelSub()
	go b.classifyStatuses(ctx, statusCh)

	// Faster cadence for the liveness indicator — 2s feels live.
	t := time.NewTicker(2 * time.Second)
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

// classifyStatuses fans the status hub into per-destination health
// based on message-prefix matching. Heuristic but covers the messages
// the codebase actually emits today.
func (b *destBar) classifyStatuses(ctx context.Context, ch <-chan reporter.Status) {
	for {
		select {
		case <-ctx.Done():
			return
		case s, ok := <-ch:
			if !ok {
				return
			}
			b.classify(s)
		}
	}
}

func (b *destBar) classify(s reporter.Status) {
	dest := matchDestination(s.Message)
	if dest == "" {
		return
	}
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	h := b.health[dest]
	switch s.Level {
	case reporter.LevelOK:
		h.LastOK = s.Time
	case reporter.LevelError:
		h.LastErr = s.Time
	}
	b.health[dest] = h
}

// matchDestination returns the chip name the given status message
// belongs to, or "" if it's a generic message we don't attribute.
func matchDestination(msg string) string {
	switch {
	case strings.HasPrefix(msg, "EDDN"):
		return "EDDN"
	case strings.HasPrefix(msg, "EDSM"):
		return "EDSM"
	case strings.HasPrefix(msg, "Inara"):
		return "Inara"
	case strings.HasPrefix(msg, "Synced FC ") || strings.HasPrefix(msg, "Sync FC ") ||
		strings.HasPrefix(msg, "FC cargo "):
		return "FC sync"
	case strings.HasPrefix(msg, "Published Fleet Carrier") ||
		strings.HasPrefix(msg, "Created ravencolonial") ||
		strings.HasPrefix(msg, "Reported depot") ||
		strings.HasPrefix(msg, "Contributed to ") ||
		strings.HasPrefix(msg, "Marked build ") ||
		strings.HasPrefix(msg, "Set ") && strings.Contains(msg, "as architect"):
		return "RC"
	}
	return ""
}

// healthColor picks the chip color from per-destination health: red if
// the most recent outcome was an error within the last 5 minutes,
// otherwise the destination's default colour.
func healthColor(h destHealth, defaultColor color.Color) color.Color {
	if !h.LastErr.IsZero() && h.LastErr.After(h.LastOK) {
		if time.Since(h.LastErr) < 5*time.Minute {
			return edStatusError
		}
	}
	return defaultColor
}

func (b *destBar) update() {
	cfg := b.srv.Config()

	// Per-destination health from the statusHub classifier.
	b.healthMu.Lock()
	health := make(map[string]destHealth, len(b.health))
	for k, v := range b.health {
		health[k] = v
	}
	b.healthMu.Unlock()

	fyne.Do(func() {
		// RC is always "on" — we always post depot+contribution updates if
		// the cmdr is present; rcc-key is only for FC writes.
		b.chips["RC"].setEnabled(true, healthColor(health["RC"], edStatusOK))
		// "FC sync" is on if we have an rcc-key (writes are possible).
		fcWritable := cfg.APIKey != ""
		fcColor := edStatusOK
		if !fcWritable {
			fcColor = edFgDim
		}
		b.chips["FC sync"].setEnabled(fcWritable, healthColor(health["FC sync"], fcColor))
		b.chips["EDDN"].setEnabled(cfg.EDDNEnabled, healthColor(health["EDDN"], edStatusInfo))
		b.chips["EDSM"].setEnabled(cfg.EDSMEnabled && cfg.EDSMAPIKey != "", healthColor(health["EDSM"], edStatusInfo))
		b.chips["Inara"].setEnabled(cfg.InaraEnabled && cfg.InaraAPIKey != "", healthColor(health["Inara"], edStatusInfo))

		if u := b.srv.URL(); u != "" {
			b.url.Text = u
			b.url.Refresh()
		}

		// Liveness indicator: how long since the tailer processed an
		// event. Green if very recent (game is logging), amber if
		// stale (game probably closed), gray if never seen.
		t := b.srv.LastEventAt()
		if t.IsZero() {
			b.lastEvent.Text = "Last event: —"
			b.lastEvent.Color = edFgDim
		} else {
			age := time.Since(t)
			b.lastEvent.Text = "Last event: " + humanAge(age)
			switch {
			case age < 30*time.Second:
				b.lastEvent.Color = edStatusOK
			case age < 5*time.Minute:
				b.lastEvent.Color = edStatusInfo
			case age < 30*time.Minute:
				b.lastEvent.Color = edStatusWarn
			default:
				b.lastEvent.Color = edFgDim
			}
		}
		b.lastEvent.Refresh()
	})
}

func humanAge(d time.Duration) string {
	if d < time.Second {
		return "just now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%.1fh ago", d.Hours())
}

func (b *destBar) content() fyne.CanvasObject { return b.root }
