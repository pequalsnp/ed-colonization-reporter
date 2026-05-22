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

// projectsPanel renders one card per active project with a progress bar.
type projectsPanel struct {
	srv *web.Server

	mu        sync.Mutex
	rows      []ravencolonial.Project
	commander string
	err       string

	summary *canvas.Text
	refresh *widget.Button
	cards   *fyne.Container
	scroll  *container.Scroll
	empty   *fyne.Container
}

func newProjectsPanel(srv *web.Server) *projectsPanel {
	p := &projectsPanel{srv: srv}
	p.summary = canvas.NewText("Loading…", edFgMuted)
	p.summary.TextSize = 12
	p.refresh = widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() { go p.refreshNow() })
	p.refresh.Importance = widget.MediumImportance
	p.cards = container.NewVBox()
	p.scroll = container.NewVScroll(p.cards)

	emptyText := canvas.NewText("No active projects yet.", edFgMuted)
	emptyText.TextSize = 14
	emptyText.Alignment = fyne.TextAlignCenter
	hint := canvas.NewText("Dock at a construction depot in-game and it'll appear here.", edFgDim)
	hint.TextSize = 12
	hint.Alignment = fyne.TextAlignCenter
	p.empty = container.NewCenter(container.NewVBox(emptyText, hint))
	p.empty.Hide()

	return p
}

func (p *projectsPanel) content() fyne.CanvasObject {
	top := container.NewBorder(nil, nil, p.summary, p.refresh, layout.NewSpacer())
	stack := container.NewStack(p.scroll, p.empty)
	return container.NewBorder(
		container.NewPadded(top),
		nil, nil, nil,
		stack,
	)
}

func (p *projectsPanel) runAutoRefresh(ctx context.Context) {
	p.refreshNow()
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refreshNow()
		}
	}
}

func (p *projectsPanel) refreshNow() {
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

func (p *projectsPanel) rerender() {
	p.mu.Lock()
	rows := append([]ravencolonial.Project(nil), p.rows...)
	cmdr := p.commander
	errMsg := p.err
	p.mu.Unlock()

	fyne.Do(func() {
		switch {
		case errMsg != "":
			p.summary.Text = "Refresh failed: " + errMsg
			p.summary.Color = edStatusError
		case cmdr == "":
			p.summary.Text = "Commander unknown — start Elite Dangerous to populate."
			p.summary.Color = edFgMuted
		default:
			p.summary.Text = fmt.Sprintf("%d active project%s — Cmdr %s", len(rows), plural(len(rows)), cmdr)
			p.summary.Color = edFgMuted
		}
		p.summary.Refresh()

		p.cards.RemoveAll()
		if len(rows) == 0 && errMsg == "" {
			p.empty.Show()
		} else {
			p.empty.Hide()
		}
		for _, r := range rows {
			p.cards.Add(buildProjectCard(r))
		}
		p.cards.Refresh()
	})
}

// buildProjectCard renders one project as a self-contained card with
// system, build name, status badge, progress bar, outstanding count,
// and the top outstanding commodities.
func buildProjectCard(p ravencolonial.Project) fyne.CanvasObject {
	systemName := p.SystemName
	if systemName == "" {
		systemName = "(unknown system)"
	}
	buildName := p.BuildName
	if buildName == "" {
		buildName = p.BuildID
	}
	if buildName == "" {
		buildName = "(unnamed build)"
	}

	systemLbl := canvas.NewText(systemName, edFg)
	systemLbl.TextStyle = fyne.TextStyle{Bold: true}
	systemLbl.TextSize = 15

	buildLbl := canvas.NewText(buildName, edFgMuted)
	buildLbl.TextSize = 13

	statusBadge := makeBadge(statusBadgeText(p), statusBadgeColor(p))

	header := container.NewBorder(nil, nil, container.NewVBox(systemLbl, buildLbl), statusBadge, layout.NewSpacer())

	outstanding := 0
	for _, n := range p.Commodities {
		outstanding += n
	}
	progress := computeProgress(p, outstanding)

	bar := widget.NewProgressBar()
	bar.SetValue(progress)
	bar.TextFormatter = func() string {
		return fmt.Sprintf("%.0f%%", progress*100)
	}

	summary := canvas.NewText(fmt.Sprintf("%s units outstanding", humanCount(outstanding)), edFgMuted)
	summary.TextSize = 12

	body := container.NewVBox(bar, summary)
	if outstanding > 0 {
		body.Add(commoditiesLine(p.Commodities))
	}

	card := container.NewVBox(
		container.NewPadded(header),
		container.NewPadded(body),
	)

	// Card-style frame: subtle rounded background.
	bg := canvas.NewRectangle(edBgRaised)
	bg.CornerRadius = 6
	bg.StrokeColor = edBorder
	bg.StrokeWidth = 1
	return container.NewPadded(container.NewStack(bg, card))
}

// commoditiesLine renders the top outstanding commodities as a compact
// dot-separated row, with a "+N more" trailer when there are more than
// the visible cap. Quantity is shown in bold + foreground colour;
// commodity name in muted.
func commoditiesLine(commodities map[string]int) fyne.CanvasObject {
	const cap = 5
	top := topCommodities(commodities, cap)
	if len(top) == 0 {
		return container.NewWithoutLayout()
	}

	row := container.NewHBox()
	for i, c := range top {
		if i > 0 {
			sep := canvas.NewText("·", edFgDim)
			sep.TextSize = 12
			row.Add(container.NewPadded(sep))
		}
		qty := canvas.NewText(humanCount(c.Count), edFg)
		qty.TextSize = 12
		qty.TextStyle = fyne.TextStyle{Bold: true}
		name := canvas.NewText(PrettifyCommodity(c.Symbol), edFgMuted)
		name.TextSize = 12
		row.Add(qty)
		row.Add(name)
	}

	more := 0
	for _, n := range commodities {
		if n > 0 {
			more++
		}
	}
	more -= len(top)
	if more > 0 {
		tail := canvas.NewText(fmt.Sprintf("· +%d more", more), edFgDim)
		tail.TextSize = 12
		row.Add(container.NewPadded(tail))
	}
	return container.NewHScroll(row)
}

func statusBadgeText(p ravencolonial.Project) string {
	if p.Complete {
		return "COMPLETE"
	}
	return "ACTIVE"
}

func statusBadgeColor(p ravencolonial.Project) fyne.ThemeColorName {
	if p.Complete {
		return theme.ColorNameSuccess
	}
	return theme.ColorNamePrimary
}

// computeProgress returns how far along the build is, [0,1]. If MaxNeed
// is unknown we approximate from the outstanding count alone — better
// than a zero progress bar.
func computeProgress(p ravencolonial.Project, outstanding int) float64 {
	if p.Complete {
		return 1.0
	}
	if p.MaxNeed > 0 {
		delivered := p.MaxNeed - outstanding
		if delivered < 0 {
			delivered = 0
		}
		return float64(delivered) / float64(p.MaxNeed)
	}
	return 0
}

func makeBadge(text string, colorName fyne.ThemeColorName) fyne.CanvasObject {
	col := theme.Color(colorName)
	lbl := canvas.NewText(text, col)
	lbl.TextStyle = fyne.TextStyle{Bold: true}
	lbl.TextSize = 10
	bg := canvas.NewRectangle(withAlpha(col, 0x22))
	bg.CornerRadius = 3
	bg.StrokeColor = withAlpha(col, 0x55)
	bg.StrokeWidth = 1
	return container.NewPadded(container.NewStack(bg, container.NewCenter(lbl)))
}

func withAlpha(c fyneColor, a uint8) fyneColor {
	r, g, b, _ := c.RGBA()
	return colorNRGBA(uint8(r>>8), uint8(g>>8), uint8(b>>8), a)
}

// Local aliases so the rest of the file doesn't import image/color.
type fyneColor = interface {
	RGBA() (r, g, b, a uint32)
}

func colorNRGBA(r, g, b, a uint8) fyneColor {
	return nrgba{r, g, b, a}
}

type nrgba struct{ r, g, b, a uint8 }

func (n nrgba) RGBA() (uint32, uint32, uint32, uint32) {
	return uint32(n.r) * 0x101, uint32(n.g) * 0x101, uint32(n.b) * 0x101, uint32(n.a) * 0x101
}

func humanCount(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 10_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
