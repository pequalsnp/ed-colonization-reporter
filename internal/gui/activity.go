package gui

import (
	"fmt"
	"image/color"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

// activityPanel renders the reporter status stream with per-level colours.
type activityPanel struct {
	mu      sync.Mutex
	entries []reporter.Status

	list *widget.List
}

func newActivityPanel() *activityPanel {
	p := &activityPanel{}
	p.list = widget.NewList(
		func() int {
			p.mu.Lock()
			defer p.mu.Unlock()
			return len(p.entries)
		},
		// Three canvas.Text columns: time, level badge, message. Reuse
		// per row to avoid per-update allocation.
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
			s := p.entries[idx]
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
	return p
}

func (p *activityPanel) content() fyne.CanvasObject {
	header := canvas.NewText("Live activity", edFgMuted)
	header.TextStyle = fyne.TextStyle{Bold: true}
	header.TextSize = 12
	return container.NewBorder(
		container.NewPadded(header),
		nil, nil, nil,
		p.list,
	)
}

func (p *activityPanel) append(s reporter.Status) {
	p.mu.Lock()
	p.entries = append(p.entries, s)
	if len(p.entries) > 500 {
		p.entries = p.entries[len(p.entries)-500:]
	}
	count := len(p.entries)
	p.mu.Unlock()
	fyne.Do(func() {
		p.list.Refresh()
		p.list.ScrollTo(count - 1)
	})
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
