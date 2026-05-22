// Package gui implements the native Fyne desktop UI.
//
// It is a thin presentation layer on top of internal/web.Server: every
// panel reads state directly through the methods Server exposes
// (Session, Subscribe, Config, ApplyConfig, ActiveProjects, Frontier*)
// rather than going through HTTP. The browser UI still works at the
// server's loopback URL if anything goes wrong with the desktop window.
package gui

import (
	"context"
	"image/color"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// _ keeps the color import in scope until the embedded statusBar uses
// it (status indicators reference edFgDim et al. by value, but the
// linter still wants the import explicit).
var _ = color.Black

// App is the Fyne window owner. It is built around a *web.Server which
// owns all backend state — Session, destinations, Frontier OAuth, the
// status hub, etc.
type App struct {
	srv *web.Server

	app    fyne.App
	window fyne.Window

	statusBar     *statusBar
	projects      *projectsPanel
	activity      *activityPanel
	settings      *settingsPanel
	frontierPanel *frontierPanel
	destBar       *destBar
}

// Run starts the Fyne app and blocks until the window is closed.
// Must be called from the main goroutine — Fyne (like every native
// toolkit) requires its event loop to own main.
func Run(ctx context.Context, srv *web.Server) {
	a := newApp(srv)
	a.show(ctx)
}

func newApp(srv *web.Server) *App {
	a := &App{srv: srv}
	a.app = fyneapp.NewWithID("ca.thegalloways.edcolreport")
	a.app.Settings().SetTheme(&edTheme{})
	a.app.SetIcon(appIcon())
	a.window = a.app.NewWindow("ED Colonization Reporter")
	a.window.SetIcon(appIcon())
	a.window.Resize(fyne.NewSize(960, 720))
	return a
}

func (a *App) show(ctx context.Context) {
	a.statusBar = newStatusBar(a.srv.GetVersion())
	a.projects = newProjectsPanel(a.srv)
	a.activity = newActivityPanel()
	a.settings = newSettingsPanel(a.srv)
	a.frontierPanel = newFrontierPanel(a.srv)
	a.destBar = newDestBar(a.srv)

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("Projects", theme.GridIcon(), a.projects.content()),
		container.NewTabItemWithIcon("Activity", theme.HistoryIcon(), a.activity.content()),
		container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), a.settings.content(a.frontierPanel)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	// Thin orange divider line under the status bar — same trick the ED
	// in-game HUD uses to separate header from body.
	topDivider := canvas.NewRectangle(edOrange)
	topDivider.SetMinSize(fyne.NewSize(0, 1))
	bottomDivider := canvas.NewRectangle(edBorder)
	bottomDivider.SetMinSize(fyne.NewSize(0, 1))

	header := container.NewVBox(a.statusBar.content(), topDivider)
	footer := container.NewVBox(bottomDivider, a.destBar.content())
	root := container.NewBorder(header, footer, nil, nil, tabs)
	a.window.SetContent(root)

	subCtx, cancel := context.WithCancel(ctx)
	a.window.SetOnClosed(func() {
		cancel()
		a.app.Quit()
	})

	go a.runStatusBarLoop(subCtx)
	go a.runActivityLoop(subCtx)
	go a.projects.runAutoRefresh(subCtx)
	go a.frontierPanel.runStatusLoop(subCtx)
	go a.destBar.runLoop(subCtx)

	a.window.ShowAndRun()
}

func (a *App) runStatusBarLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	update := func() {
		snap := a.srv.Session().Snapshot()
		fyne.Do(func() { a.statusBar.update(snap.Commander, snap.StarSystem, snap.Docked, snap.StationName) })
	}
	update()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			update()
		}
	}
}

func (a *App) runActivityLoop(ctx context.Context) {
	ch, cancel := a.srv.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case s, ok := <-ch:
			if !ok {
				return
			}
			a.activity.append(s)
		}
	}
}

// ---------------------------------------------------------------------------
// Status bar
// ---------------------------------------------------------------------------

type statusBar struct {
	version string

	cmdrVal, systemVal, dockVal *canvas.Text
	indicator                   *canvas.Circle
	root                        fyne.CanvasObject
}

func newStatusBar(version string) *statusBar {
	mkLabel := func(text string) *canvas.Text {
		t := canvas.NewText(text, edFgMuted)
		t.TextSize = 12
		return t
	}
	mkValue := func() *canvas.Text {
		t := canvas.NewText("—", edFg)
		t.TextSize = 13
		t.TextStyle = fyne.TextStyle{Bold: true}
		return t
	}
	sb := &statusBar{
		version:   version,
		cmdrVal:   mkValue(),
		systemVal: mkValue(),
		dockVal:   mkValue(),
		indicator: canvas.NewCircle(edFgDim),
	}
	sb.indicator.Resize(fyne.NewSize(10, 10))
	sb.indicator.StrokeWidth = 0

	field := func(label string, value *canvas.Text) fyne.CanvasObject {
		return container.NewHBox(mkLabel(label), value)
	}

	indWrap := container.New(layout.NewCenterLayout(), container.NewWithoutLayout(sb.indicator))
	indWrap.Resize(fyne.NewSize(16, 16))

	verText := canvas.NewText("v"+version, edFgDim)
	verText.TextSize = 11

	left := container.NewHBox(
		container.NewPadded(indWrap),
		field("CMDR ", sb.cmdrVal),
		widget.NewSeparator(),
		field("SYSTEM ", sb.systemVal),
		widget.NewSeparator(),
		field("DOCK ", sb.dockVal),
	)
	right := container.NewHBox(verText)
	sb.root = container.NewPadded(container.NewBorder(nil, nil, left, right, layout.NewSpacer()))
	return sb
}

func (sb *statusBar) content() fyne.CanvasObject { return sb.root }

func (sb *statusBar) update(cmdr, system string, docked bool, station string) {
	sb.cmdrVal.Text = dashIfEmpty(cmdr)
	sb.systemVal.Text = dashIfEmpty(system)
	if docked && station != "" {
		sb.dockVal.Text = station
		sb.dockVal.Color = edStatusOK
		sb.indicator.FillColor = edStatusOK
	} else {
		sb.dockVal.Text = "undocked"
		sb.dockVal.Color = edFgMuted
		sb.indicator.FillColor = edFgDim
	}
	if cmdr == "" {
		sb.indicator.FillColor = edFgDim
	} else if sb.indicator.FillColor != edStatusOK {
		sb.indicator.FillColor = edStatusInfo
	}
	sb.cmdrVal.Refresh()
	sb.systemVal.Refresh()
	sb.dockVal.Refresh()
	sb.indicator.Refresh()
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
