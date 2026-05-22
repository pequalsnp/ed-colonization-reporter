package gui

import (
	"fmt"
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

// activityPanel renders the reporter status stream with per-level
// colours, filter chips, and a pause-autoscroll toggle.
type activityPanel struct {
	mu      sync.Mutex
	entries []reporter.Status
	visible []int // indices into entries[] after filtering, recomputed on filter/append

	// Filter state — true means "show this level".
	levelEnabled map[reporter.Level]bool
	paused       bool

	list         *widget.List
	chips        map[reporter.Level]*widget.Button
	pauseBtn     *widget.Button
	clearBtn     *widget.Button
	countLbl     *canvas.Text
}

func newActivityPanel() *activityPanel {
	p := &activityPanel{
		levelEnabled: map[reporter.Level]bool{
			reporter.LevelError: true,
			reporter.LevelWarn:  true,
			reporter.LevelOK:    true,
			reporter.LevelInfo:  true,
		},
	}
	p.list = widget.NewList(
		func() int {
			p.mu.Lock()
			defer p.mu.Unlock()
			return len(p.visible)
		},
		func() fyne.CanvasObject {
			t := canvas.NewText("00:00:00", edFgDim)
			t.TextSize = 12
			t.TextStyle = fyne.TextStyle{Monospace: true}

			level := canvas.NewText("INFO ", edStatusInfo)
			level.TextSize = 12
			level.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}

			msg := canvas.NewText("template message", edFg)
			msg.TextSize = 12
			msg.TextStyle = fyne.TextStyle{Monospace: true}

			return container.NewHBox(t, level, msg)
		},
		func(idx widget.ListItemID, obj fyne.CanvasObject) {
			p.mu.Lock()
			if idx < 0 || idx >= len(p.visible) {
				p.mu.Unlock()
				return
			}
			s := p.entries[p.visible[idx]]
			p.mu.Unlock()

			box := obj.(*fyne.Container)
			t := box.Objects[0].(*canvas.Text)
			level := box.Objects[1].(*canvas.Text)
			msg := box.Objects[2].(*canvas.Text)

			t.Text = s.Time.Format("15:04:05")
			t.Refresh()
			level.Text = fmt.Sprintf("%-5s", s.Level.String())
			level.Color = levelColor(s.Level)
			level.Refresh()
			msg.Text = s.Message
			msg.Color = edFg
			msg.Refresh()
		},
	)

	p.chips = map[reporter.Level]*widget.Button{}
	for _, lvl := range []reporter.Level{reporter.LevelError, reporter.LevelWarn, reporter.LevelOK, reporter.LevelInfo} {
		lvl := lvl
		btn := widget.NewButton(lvl.String(), func() { p.toggleLevel(lvl) })
		p.chips[lvl] = btn
	}
	p.applyChipStyles()

	p.pauseBtn = widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), p.togglePause)
	p.clearBtn = widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), p.clear)
	p.clearBtn.Importance = widget.LowImportance

	p.countLbl = canvas.NewText("0 entries", edFgMuted)
	p.countLbl.TextSize = 12

	return p
}

func (p *activityPanel) content() fyne.CanvasObject {
	// Toolbar: [level chips] | spacer | count | pause | clear
	chipsRow := container.NewHBox()
	for _, lvl := range []reporter.Level{reporter.LevelError, reporter.LevelWarn, reporter.LevelOK, reporter.LevelInfo} {
		chipsRow.Add(p.chips[lvl])
	}
	rightRow := container.NewHBox(p.countLbl, p.pauseBtn, p.clearBtn)
	toolbar := container.NewBorder(nil, nil, chipsRow, rightRow, widget.NewLabel(""))
	return container.NewBorder(container.NewPadded(toolbar), nil, nil, nil, p.list)
}

func (p *activityPanel) append(s reporter.Status) {
	p.mu.Lock()
	p.entries = append(p.entries, s)
	if len(p.entries) > 1000 {
		// Trim the underlying buffer; recompute visible indices wholesale
		// since indices into entries[] just shifted.
		p.entries = p.entries[len(p.entries)-1000:]
		p.recomputeVisibleLocked()
	} else if p.levelEnabled[s.Level] {
		// Fast path: just append the new entry's index to the visible list.
		p.visible = append(p.visible, len(p.entries)-1)
	}
	wantScroll := !p.paused
	count := len(p.entries)
	visCount := len(p.visible)
	p.mu.Unlock()

	fyne.Do(func() {
		p.countLbl.Text = fmt.Sprintf("%d / %d entries", visCount, count)
		p.countLbl.Refresh()
		p.list.Refresh()
		if wantScroll && visCount > 0 {
			p.list.ScrollTo(visCount - 1)
		}
	})
}

func (p *activityPanel) toggleLevel(lvl reporter.Level) {
	p.mu.Lock()
	p.levelEnabled[lvl] = !p.levelEnabled[lvl]
	p.recomputeVisibleLocked()
	visCount := len(p.visible)
	totalCount := len(p.entries)
	p.mu.Unlock()
	fyne.Do(func() {
		p.applyChipStyles()
		p.countLbl.Text = fmt.Sprintf("%d / %d entries", visCount, totalCount)
		p.countLbl.Refresh()
		p.list.Refresh()
		if !p.isPaused() && visCount > 0 {
			p.list.ScrollTo(visCount - 1)
		}
	})
}

func (p *activityPanel) togglePause() {
	p.mu.Lock()
	p.paused = !p.paused
	paused := p.paused
	p.mu.Unlock()
	fyne.Do(func() {
		if paused {
			p.pauseBtn.SetText("Resume")
			p.pauseBtn.SetIcon(theme.MediaPlayIcon())
		} else {
			p.pauseBtn.SetText("Pause")
			p.pauseBtn.SetIcon(theme.MediaPauseIcon())
		}
	})
}

func (p *activityPanel) clear() {
	p.mu.Lock()
	p.entries = nil
	p.visible = nil
	p.mu.Unlock()
	fyne.Do(func() {
		p.countLbl.Text = "0 entries"
		p.countLbl.Refresh()
		p.list.Refresh()
	})
}

func (p *activityPanel) isPaused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

// recomputeVisibleLocked rebuilds visible[] from entries[] + filter state.
// Caller holds p.mu.
func (p *activityPanel) recomputeVisibleLocked() {
	p.visible = p.visible[:0]
	for i, e := range p.entries {
		if p.levelEnabled[e.Level] {
			p.visible = append(p.visible, i)
		}
	}
}

// applyChipStyles paints each filter chip with the level's color when on,
// or a muted background when off. Called on toggle.
func (p *activityPanel) applyChipStyles() {
	p.mu.Lock()
	state := make(map[reporter.Level]bool, len(p.levelEnabled))
	for k, v := range p.levelEnabled {
		state[k] = v
	}
	p.mu.Unlock()
	for lvl, btn := range p.chips {
		if state[lvl] {
			// Active — high importance, will render with primary color.
			btn.Importance = widget.HighImportance
		} else {
			btn.Importance = widget.LowImportance
		}
		btn.Refresh()
	}
}

func levelColor(l reporter.Level) color.Color {
	switch l {
	case reporter.LevelOK:
		return edStatusOK
	case reporter.LevelWarn:
		return edStatusWarn
	case reporter.LevelError:
		return edStatusError
	default:
		return edStatusInfo
	}
}
