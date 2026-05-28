package gui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// combinedPanel shows a single aggregated view of every active project's
// outstanding commodities — useful when planning a haul that'll service
// multiple builds in one trip. Complete projects are excluded from the
// sum so they don't pull totals toward zero.
type combinedPanel struct {
	srv *web.Server

	mu        sync.Mutex
	rows      []ravencolonial.Project
	commander string
	err       string

	summary *canvas.Text
	refresh *widget.Button
	body    *fyne.Container
	scroll  *container.Scroll
}

func newCombinedPanel(srv *web.Server) *combinedPanel {
	p := &combinedPanel{srv: srv}
	p.body = container.NewVBox()
	p.scroll = container.NewVScroll(p.body)
	p.summary = canvas.NewText("Loading…", edFgMuted)
	p.summary.TextSize = 12
	p.refresh = widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() { go p.refreshNow() })
	p.refresh.Importance = widget.MediumImportance
	return p
}

func (p *combinedPanel) content() fyne.CanvasObject {
	summaryRow := container.NewBorder(nil, nil, p.summary, p.refresh, layout.NewSpacer())
	top := container.NewVBox(summaryRow)
	return container.NewBorder(
		container.NewPadded(top),
		nil, nil, nil,
		p.scroll,
	)
}

func (p *combinedPanel) runAutoRefresh(ctx context.Context) {
	p.refreshNow()
	interval := projectsPollInterval(p.srv.Config().ProjectsPollSeconds)
	t := time.NewTicker(interval)
	defer t.Stop()
	reconfig := time.NewTicker(30 * time.Second)
	defer reconfig.Stop()
	// Local-only redraw on a faster cadence so FC/ship cargo changes
	// reflect within a couple seconds without re-pulling the project
	// list from ravencolonial.
	redraw := time.NewTicker(2 * time.Second)
	defer redraw.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refreshNow()
		case <-redraw.C:
			p.rerender()
		case <-reconfig.C:
			want := projectsPollInterval(p.srv.Config().ProjectsPollSeconds)
			if want != interval {
				t.Reset(want)
				interval = want
			}
		}
	}
}

func (p *combinedPanel) refreshNow() {
	fyne.Do(func() {
		p.summary.Text = "Loading…"
		p.summary.Color = edFgMuted
		p.summary.Refresh()
	})
	rows, cmdr, err := p.srv.ActiveProjects(context.Background())
	p.mu.Lock()
	p.rows = rows
	p.commander = cmdr
	if err != nil {
		p.err = err.Error()
	} else {
		p.err = ""
	}
	p.mu.Unlock()
	p.rerender()
}

func (p *combinedPanel) rerender() {
	p.mu.Lock()
	rows := append([]ravencolonial.Project(nil), p.rows...)
	cmdr := p.commander
	errMsg := p.err
	p.mu.Unlock()

	// Aggregate across active (non-complete) projects only.
	totals := map[string]int{}
	activeCount := 0
	for _, r := range rows {
		if r.Complete {
			continue
		}
		activeCount++
		for sym, qty := range r.Commodities {
			if qty <= 0 {
				continue
			}
			totals[sym] += qty
		}
	}
	grandTotal := 0
	for _, n := range totals {
		grandTotal += n
	}

	fcName, fcInv, _ := p.srv.LastFCInventory()
	shipInv, _ := p.srv.LastShipCargo()
	station, marketStock, _ := p.srv.CurrentMarket()

	fyne.Do(func() {
		switch {
		case errMsg != "":
			p.summary.Text = "Refresh failed: " + errMsg
			p.summary.Color = edStatusError
		case cmdr == "":
			p.summary.Text = "Commander unknown — start Elite Dangerous to populate."
			p.summary.Color = edFgMuted
		case activeCount == 0:
			p.summary.Text = "No active projects."
			p.summary.Color = edFgMuted
		default:
			p.summary.Text = fmt.Sprintf("%d active project%s · %s units outstanding total — Cmdr %s",
				activeCount, plural(activeCount), humanCount(grandTotal), cmdr)
			p.summary.Color = edFgMuted
		}
		p.summary.Refresh()

		p.body.RemoveAll()
		if activeCount == 0 {
			p.body.Refresh()
			return
		}

		heading := "Combined needs across all active projects:"
		switch {
		case fcName != "" && station != "":
			heading = fmt.Sprintf("FC %s · ship cargo · buyable at %s (highlighted):", fcName, station)
		case fcName != "":
			heading = fmt.Sprintf("FC %s · ship cargo:", fcName)
		case station != "":
			heading = fmt.Sprintf("Buyable at %s (highlighted):", station)
		}
		h := canvas.NewText(heading, edFgMuted)
		h.TextSize = 11
		p.body.Add(container.NewPadded(h))
		p.body.Add(allCommoditiesGrid(totals, fcInv, shipInv, marketStock))
		p.body.Refresh()
	})
}
