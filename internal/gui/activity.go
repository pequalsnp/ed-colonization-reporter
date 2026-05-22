package gui

import (
	"fmt"
	"image/color"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
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

	list      *widget.List
	chips     map[reporter.Level]*widget.Button
	pauseBtn  *widget.Button
	clearBtn  *widget.Button
	exportBtn *widget.Button
	countLbl  *canvas.Text

	// window is set by the App after construction so the Export dialog
	// has a parent to anchor on.
	window fyne.Window
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
	p.exportBtn = widget.NewButtonWithIcon("Export", theme.DocumentSaveIcon(), p.export)
	p.exportBtn.Importance = widget.LowImportance

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
	rightRow := container.NewHBox(p.countLbl, p.pauseBtn, p.exportBtn, p.clearBtn)
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

// export opens a save-as dialog and writes the currently-loaded
// activity entries to a plain text file. Useful for bug reports.
func (p *activityPanel) export() {
	if p.window == nil {
		return
	}
	p.mu.Lock()
	snapshot := append([]reporter.Status(nil), p.entries...)
	p.mu.Unlock()

	dlg := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			return
		}
		defer w.Close()
		var b strings.Builder
		for _, s := range snapshot {
			b.WriteString(formatStatus(s))
			b.WriteString("\n")
		}
		_, _ = w.Write([]byte(b.String()))
	}, p.window)
	dlg.SetFileName(fmt.Sprintf("edcolreport-activity-%s.log", today()))
	dlg.SetFilter(storage.NewExtensionFileFilter([]string{".log", ".txt"}))
	dlg.Show()
}

// today returns YYYYMMDD-HHMMSS for the default filename.
func today() string {
	return time.Now().Format("20060102-150405")
}

// formatStatus renders a reporter.Status as one human-readable line.
// Used by both the export button and (originally) the activity list
// row renderer; kept package-private so both call sites stay aligned.
func formatStatus(s reporter.Status) string {
	return fmt.Sprintf("[%s] %-5s %s", s.Time.Format("15:04:05"), s.Level.String(), s.Message)
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
