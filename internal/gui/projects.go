package gui

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"sort"
	"strings"
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

// stableSort is a thin wrapper so call sites read like a domain operation.
func stableSort(rows []ravencolonial.Project, less func(i, j int) bool) {
	sort.SliceStable(rows, less)
}

// projectsPanel renders one card per active project with a progress bar.
type projectsPanel struct {
	srv *web.Server

	mu        sync.Mutex
	rows      []ravencolonial.Project
	commander string
	err       string
	// expanded tracks which build IDs are currently expanded to show
	// the full commodity breakdown. Survives rerenders so refreshing
	// the list doesn't collapse the user's drilldown.
	expanded map[string]bool

	search *widget.Entry
	filter string

	sortSel *widget.Select
	sortKey string

	summary *canvas.Text
	refresh *widget.Button
	cards   *fyne.Container
	scroll  *container.Scroll
	empty   *fyne.Container

	// prefs is set by content() once the panel is wired into the App so
	// sort/filter preferences survive launches.
	prefs fyne.Preferences
}

func newProjectsPanel(srv *web.Server) *projectsPanel {
	p := &projectsPanel{srv: srv, expanded: map[string]bool{}, sortKey: sortOutstandingDesc}

	// Render targets first — both the search OnChanged and the sort
	// Select fire their callbacks synchronously on construction, which
	// re-enters rerender(). rerender touches p.cards / p.empty so those
	// MUST exist before any control-creation that wires a handler.
	p.cards = container.NewVBox()
	p.scroll = container.NewVScroll(p.cards)
	p.empty = container.NewCenter(container.NewVBox())
	p.empty.Hide()

	p.summary = canvas.NewText("Loading…", edFgMuted)
	p.summary.TextSize = 12
	p.refresh = widget.NewButtonWithIcon("Refresh", theme.ViewRefreshIcon(), func() { go p.refreshNow() })
	p.refresh.Importance = widget.MediumImportance
	p.search = widget.NewEntry()
	p.search.SetPlaceHolder("Filter by system or build name…")
	p.search.OnChanged = func(text string) {
		p.mu.Lock()
		p.filter = strings.TrimSpace(strings.ToLower(text))
		p.mu.Unlock()
		if p.prefs != nil {
			p.prefs.SetString("projects.filter", text)
		}
		p.rerender()
	}

	p.sortSel = widget.NewSelect(sortOptions(), func(label string) {
		p.mu.Lock()
		p.sortKey = sortKeyForLabel(label)
		key := p.sortKey
		p.mu.Unlock()
		if p.prefs != nil {
			p.prefs.SetString("projects.sort", key)
		}
		p.rerender()
	})
	p.sortSel.SetSelected(sortLabelForKey(sortOutstandingDesc))

	return p
}

func (p *projectsPanel) content() fyne.CanvasObject {
	summaryRow := container.NewBorder(nil, nil, p.summary, p.refresh, layout.NewSpacer())
	// Search icon prefix on the entry so it reads as a filter, not a label.
	searchPrefix := widget.NewIcon(theme.SearchIcon())
	sortLabel := canvas.NewText("Sort:", edFgMuted)
	sortLabel.TextSize = 12
	searchRow := container.NewBorder(nil, nil,
		container.NewPadded(searchPrefix),
		container.NewHBox(container.NewPadded(sortLabel), p.sortSel),
		p.search,
	)
	top := container.NewVBox(summaryRow, searchRow)
	stack := container.NewStack(p.scroll, p.empty)
	return container.NewBorder(
		container.NewPadded(top),
		nil, nil, nil,
		stack,
	)
}

// AttachPrefs is called by the App once it has access to fyne.Preferences,
// so the panel can restore its persisted sort + filter state.
func (p *projectsPanel) AttachPrefs(prefs fyne.Preferences) {
	p.prefs = prefs
	if saved := prefs.StringWithFallback("projects.sort", sortOutstandingDesc); saved != "" {
		p.mu.Lock()
		p.sortKey = saved
		p.mu.Unlock()
		p.sortSel.SetSelected(sortLabelForKey(saved))
	}
	if savedFilter := prefs.StringWithFallback("projects.filter", ""); savedFilter != "" {
		p.mu.Lock()
		p.filter = strings.TrimSpace(strings.ToLower(savedFilter))
		p.mu.Unlock()
		fyne.Do(func() { p.search.SetText(savedFilter) })
	}
}

func (p *projectsPanel) runAutoRefresh(ctx context.Context) {
	p.refreshNow()
	interval := projectsPollInterval(p.srv.Config().ProjectsPollSeconds)
	t := time.NewTicker(interval)
	defer t.Stop()
	// Reconfigure ticker periodically in case the user changed the
	// setting at runtime via Save.
	reconfig := time.NewTicker(30 * time.Second)
	defer reconfig.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refreshNow()
		case <-reconfig.C:
			want := projectsPollInterval(p.srv.Config().ProjectsPollSeconds)
			if want != interval {
				t.Reset(want)
				interval = want
			}
		}
	}
}

// projectsPollInterval clamps the configured value into a sensible range
// and applies the default when 0.
func projectsPollInterval(seconds int) time.Duration {
	if seconds <= 0 {
		seconds = 60
	}
	if seconds < 15 {
		seconds = 15
	}
	if seconds > 600 {
		seconds = 600
	}
	return time.Duration(seconds) * time.Second
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
	filter := p.filter
	sortKey := p.sortKey
	p.mu.Unlock()

	filtered := filterProjects(rows, filter)
	sortProjects(filtered, sortKey)

	fyne.Do(func() {
		switch {
		case errMsg != "":
			p.summary.Text = "Refresh failed: " + errMsg
			p.summary.Color = edStatusError
		case cmdr == "":
			p.summary.Text = "Commander unknown — start Elite Dangerous to populate."
			p.summary.Color = edFgMuted
		case filter != "":
			p.summary.Text = fmt.Sprintf("%d of %d project%s match — Cmdr %s",
				len(filtered), len(rows), plural(len(rows)), cmdr)
			p.summary.Color = edFgMuted
		default:
			p.summary.Text = fmt.Sprintf("%d active project%s — Cmdr %s", len(rows), plural(len(rows)), cmdr)
			p.summary.Color = edFgMuted
		}
		p.summary.Refresh()

		p.cards.RemoveAll()
		if len(filtered) == 0 && errMsg == "" {
			p.empty.Objects = []fyne.CanvasObject{buildEmptyState(cmdr, filter != "")}
			p.empty.Refresh()
			p.empty.Show()
		} else {
			p.empty.Hide()
		}
		for _, r := range filtered {
			r := r
			isExpanded := p.expanded[r.BuildID]
			p.cards.Add(p.buildProjectCard(r, isExpanded))
		}
		p.cards.Refresh()
	})
}

// Sort keys.
const (
	sortOutstandingDesc = "outstanding-desc"
	sortProgressAsc     = "progress-asc"
	sortSystemAsc       = "system-asc"
	sortBuildAsc        = "build-asc"
)

func sortOptions() []string {
	return []string{
		sortLabelForKey(sortOutstandingDesc),
		sortLabelForKey(sortProgressAsc),
		sortLabelForKey(sortSystemAsc),
		sortLabelForKey(sortBuildAsc),
	}
}

func sortLabelForKey(k string) string {
	switch k {
	case sortOutstandingDesc:
		return "Outstanding (most first)"
	case sortProgressAsc:
		return "Progress (least complete first)"
	case sortSystemAsc:
		return "System name (A→Z)"
	case sortBuildAsc:
		return "Build name (A→Z)"
	}
	return "Outstanding (most first)"
}

func sortKeyForLabel(label string) string {
	for _, k := range []string{sortOutstandingDesc, sortProgressAsc, sortSystemAsc, sortBuildAsc} {
		if sortLabelForKey(k) == label {
			return k
		}
	}
	return sortOutstandingDesc
}

// sortProjects sorts in place according to the sort key.
func sortProjects(rows []ravencolonial.Project, key string) {
	switch key {
	case sortProgressAsc:
		sortStable(rows, func(i, j int) bool {
			pi := computeProgress(rows[i], totalOutstanding(rows[i].Commodities))
			pj := computeProgress(rows[j], totalOutstanding(rows[j].Commodities))
			if pi != pj {
				return pi < pj
			}
			return rows[i].BuildName < rows[j].BuildName
		})
	case sortSystemAsc:
		sortStable(rows, func(i, j int) bool {
			if rows[i].SystemName != rows[j].SystemName {
				return rows[i].SystemName < rows[j].SystemName
			}
			return rows[i].BuildName < rows[j].BuildName
		})
	case sortBuildAsc:
		sortStable(rows, func(i, j int) bool { return rows[i].BuildName < rows[j].BuildName })
	default: // sortOutstandingDesc
		sortStable(rows, func(i, j int) bool {
			oi := totalOutstanding(rows[i].Commodities)
			oj := totalOutstanding(rows[j].Commodities)
			if oi != oj {
				return oi > oj
			}
			return rows[i].BuildName < rows[j].BuildName
		})
	}
}

func totalOutstanding(m map[string]int) int {
	t := 0
	for _, v := range m {
		t += v
	}
	return t
}

// sortStable is sort.SliceStable; wrapped to keep the call site tidy.
func sortStable(rows []ravencolonial.Project, less func(i, j int) bool) {
	stableSort(rows, less)
}

// buildEmptyState chooses the right onboarding message based on
// whether the user has been seen by the app yet, or just has a busy
// filter that matches nothing.
func buildEmptyState(commander string, filterActive bool) fyne.CanvasObject {
	if filterActive {
		title := canvas.NewText("No projects match your filter.", edFgMuted)
		title.TextSize = 14
		title.Alignment = fyne.TextAlignCenter
		hint := canvas.NewText("Clear the search field to see all projects.", edFgDim)
		hint.TextSize = 12
		hint.Alignment = fyne.TextAlignCenter
		return container.NewVBox(title, hint)
	}
	if commander == "" {
		title := canvas.NewText("Waiting for Elite Dangerous…", edFgMuted)
		title.TextSize = 16
		title.Alignment = fyne.TextAlignCenter
		l1 := canvas.NewText("1. Launch Elite Dangerous.", edFgDim)
		l1.TextSize = 12
		l2 := canvas.NewText("2. Open the cockpit so the game writes a Journal entry.", edFgDim)
		l2.TextSize = 12
		l3 := canvas.NewText("3. The status bar above will show your commander.", edFgDim)
		l3.TextSize = 12
		l4 := canvas.NewText("4. Dock at a construction depot to start tracking a build.", edFgDim)
		l4.TextSize = 12
		hint := canvas.NewText("If nothing happens after a few minutes, check Settings → Journal Directory.", edFgDim)
		hint.TextSize = 11
		hint.Alignment = fyne.TextAlignCenter
		return container.NewVBox(title, container.NewPadded(container.NewVBox(l1, l2, l3, l4)), hint)
	}
	title := canvas.NewText("No active projects yet.", edFgMuted)
	title.TextSize = 14
	title.Alignment = fyne.TextAlignCenter
	hint := canvas.NewText(
		"Dock at a construction depot in-game and it'll appear here. The app "+
			"creates a ravencolonial project automatically on first sight.",
		edFgDim)
	hint.TextSize = 12
	hint.Alignment = fyne.TextAlignCenter
	hint.TextStyle = fyne.TextStyle{Italic: true}
	return container.NewVBox(title, container.NewPadded(hint))
}

// filterProjects applies the case-insensitive search filter to the list,
// matching against system name and build name. An empty filter returns
// the input unchanged. Filter normalisation (lowercase + trim) is done
// here so callers don't have to remember.
func filterProjects(rows []ravencolonial.Project, filter string) []ravencolonial.Project {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return rows
	}
	out := make([]ravencolonial.Project, 0, len(rows))
	for _, r := range rows {
		hay := strings.ToLower(r.SystemName + " " + r.BuildName)
		if strings.Contains(hay, filter) {
			out = append(out, r)
		}
	}
	return out
}

// toggleExpansion flips the expand/collapse state of a project card
// and triggers a re-render.
func (p *projectsPanel) toggleExpansion(buildID string) {
	if buildID == "" {
		return
	}
	p.mu.Lock()
	p.expanded[buildID] = !p.expanded[buildID]
	p.mu.Unlock()
	p.rerender()
}

// buildProjectCard renders one project as a self-contained card with
// system, build name, status badge, progress bar, outstanding count,
// and the top outstanding commodities. When expanded, the full
// commodity list replaces the top-5 line.
func (panel *projectsPanel) buildProjectCard(p ravencolonial.Project, expanded bool) fyne.CanvasObject {
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

	// Right side of the header: open-in-browser link plus status badge.
	headerRight := container.NewHBox(openOnWebsiteButton(p), statusBadge)
	header := container.NewBorder(nil, nil, container.NewVBox(systemLbl, buildLbl), headerRight, layout.NewSpacer())

	outstanding := 0
	for _, n := range p.Commodities {
		outstanding += n
	}
	progress := computeProgress(p, outstanding)

	bar := newGradientProgressBar(progress)

	summary := canvas.NewText(fmt.Sprintf("%s units outstanding", humanCount(outstanding)), edFgMuted)
	summary.TextSize = 12

	body := container.NewVBox(bar, summary)
	if outstanding > 0 {
		if expanded {
			fcName, fcInv, _ := panel.srv.LastFCInventory()
			shipInv, _ := panel.srv.LastShipCargo()
			if fcName != "" {
				heading := canvas.NewText(fmt.Sprintf("Against your Fleet Carrier (%s) + ship cargo:", fcName), edFgMuted)
				heading.TextSize = 11
				body.Add(container.NewPadded(heading))
			}
			body.Add(allCommoditiesGrid(p.Commodities, fcInv, shipInv))
		} else {
			body.Add(commoditiesLine(p.Commodities))
		}
	}

	// Expand/collapse toggle. Only shown when there's something worth
	// expanding (more commodities than fit in the top-N line).
	var toggle fyne.CanvasObject
	if countOutstanding(p.Commodities) > 5 {
		icon := theme.MenuDropDownIcon()
		label := "Show all commodities"
		if expanded {
			icon = theme.MenuDropUpIcon()
			label = "Show fewer"
		}
		btn := widget.NewButtonWithIcon(label, icon, func() {
			panel.toggleExpansion(p.BuildID)
		})
		btn.Importance = widget.LowImportance
		toggle = btn
	}

	cardChildren := []fyne.CanvasObject{
		container.NewPadded(header),
		container.NewPadded(body),
	}
	if toggle != nil {
		cardChildren = append(cardChildren, container.NewPadded(toggle))
	}
	card := container.NewVBox(cardChildren...)

	// Card-style frame: subtle rounded background.
	bg := canvas.NewRectangle(edBgRaised)
	bg.CornerRadius = 6
	bg.StrokeColor = edBorder
	bg.StrokeWidth = 1
	return container.NewPadded(container.NewStack(bg, card))
}

// allCommoditiesGrid renders every outstanding commodity sorted by
// need-quantity desc, with columns: Need · FC · Ship · Diff · Name.
// The FC/Ship/Diff columns are omitted when fcInventory is nil (cAPI
// not signed in or never fetched) — in that case we don't have FC
// data and a ship-only diff would be misleading. Two grid columns
// wide so 20+ commodities don't make the card absurdly tall.
//
// shipInventory may be nil; when present, the Diff column shows
// (FC + Ship) - Need so the user can see whether the combined hold
// is enough to satisfy the outstanding need.
func allCommoditiesGrid(commodities map[string]int, fcInventory, shipInventory map[string]int) fyne.CanvasObject {
	entries := topCommodities(commodities, 1000)
	if len(entries) == 0 {
		return container.NewWithoutLayout()
	}

	rows := container.New(layout.NewGridLayout(2))
	header := commoditiesHeader(fcInventory != nil)
	rows.Add(header)
	// Add an empty cell so the header doesn't push the first commodity
	// into the right column.
	rows.Add(commoditiesHeader(fcInventory != nil))

	for _, c := range entries {
		rows.Add(commodityRow(c, fcInventory, shipInventory))
	}
	return rows
}

func commoditiesHeader(showFC bool) fyne.CanvasObject {
	header := func(text string) *canvas.Text {
		t := canvas.NewText(text, edFgDim)
		t.TextSize = 10
		t.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
		return t
	}
	need := container.NewGridWrap(fyne.NewSize(56, 14), header("NEED"))
	if !showFC {
		return container.NewHBox(need, header("COMMODITY"))
	}
	fc := container.NewGridWrap(fyne.NewSize(56, 14), header("FC"))
	ship := container.NewGridWrap(fyne.NewSize(56, 14), header("SHIP"))
	diff := container.NewGridWrap(fyne.NewSize(56, 14), header("DIFF"))
	return container.NewHBox(need, fc, ship, diff, header("COMMODITY"))
}

func commodityRow(c commodityEntry, fcInventory, shipInventory map[string]int) fyne.CanvasObject {
	cellNum := func(value string, fg color.Color) fyne.CanvasObject {
		t := canvas.NewText(value, fg)
		t.TextSize = 12
		t.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
		t.Alignment = fyne.TextAlignLeading
		return container.NewGridWrap(fyne.NewSize(56, 18), t)
	}

	need := cellNum(humanCount(c.Count), edFg)
	name := canvas.NewText(PrettifyCommodity(c.Symbol), edFgMuted)
	name.TextSize = 12

	if fcInventory == nil {
		return container.NewHBox(need, name)
	}
	fcQty := fcInventory[c.Symbol]
	shipQty := 0
	if shipInventory != nil {
		shipQty = shipInventory[c.Symbol]
	}
	available := fcQty + shipQty

	fcCell := cellNum("—", edFgDim)
	if fcQty > 0 {
		fcCell = cellNum(humanCount(fcQty), edFg)
	}
	shipCell := cellNum("—", edFgDim)
	if shipQty > 0 {
		shipCell = cellNum(humanCount(shipQty), edFg)
	}
	diffCell := cellNum("—", edFgDim)
	if available > 0 || fcInventory[c.Symbol] >= 0 {
		// Diff = (FC + Ship) - Need. Negative = deficit (red),
		// positive = surplus (green). We only show a diff when we
		// actually have some FC data or stock on hand; an entry the
		// FC has never carried just shows "—".
		_, hasFCEntry := fcInventory[c.Symbol]
		if hasFCEntry || shipQty > 0 {
			delta := available - c.Count
			if delta < 0 {
				diffCell = cellNum("-"+humanCount(-delta), edStatusError)
			} else {
				diffCell = cellNum("+"+humanCount(delta), edStatusOK)
			}
		}
	}
	return container.NewHBox(need, fcCell, shipCell, diffCell, name)
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// countOutstanding returns how many commodities have outstanding > 0.
func countOutstanding(m map[string]int) int {
	n := 0
	for _, v := range m {
		if v > 0 {
			n++
		}
	}
	return n
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

// openOnWebsiteButton returns a small icon button that opens the
// project's ravencolonial.com page in the user's default browser.
// Returns an empty container if there's no BuildID to link to.
func openOnWebsiteButton(p ravencolonial.Project) fyne.CanvasObject {
	if p.BuildID == "" {
		return container.NewWithoutLayout()
	}
	target := "https://ravencolonial.com/#build=" + url.PathEscape(p.BuildID)
	btn := widget.NewButtonWithIcon("", theme.ComputerIcon(), func() {
		_ = web.OpenBrowser(target)
	})
	btn.Importance = widget.LowImportance
	return btn
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
