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
	"net/url"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
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

	// Restore the last-used window size from preferences. Defaults to
	// a reasonable starting size on first launch.
	prefs := a.app.Preferences()
	width := float32(prefs.FloatWithFallback("window.width", 960))
	height := float32(prefs.FloatWithFallback("window.height", 720))
	if width < 480 {
		width = 960
	}
	if height < 360 {
		height = 720
	}
	a.window.Resize(fyne.NewSize(width, height))
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

	// Restore last-active tab + persist on every switch.
	prefs := a.app.Preferences()
	if idx := prefs.IntWithFallback("window.activeTab", 0); idx >= 0 && idx < len(tabs.Items) {
		tabs.SelectIndex(idx)
	}
	tabs.OnSelected = func(_ *container.TabItem) {
		prefs.SetInt("window.activeTab", tabs.SelectedIndex())
	}

	// Keyboard shortcuts — standard desktop conventions.
	a.registerShortcuts(tabs)

	// Menubar — gives the app a real Quit / About / Shortcuts entry point.
	a.window.SetMainMenu(a.buildMenu(tabs))

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
		// Persist the window size for next launch. Fyne doesn't expose
		// the window's screen position cross-platform (X11/Wayland/Win
		// behave differently and not all WMs allow setting it), so we
		// only round-trip the size.
		if c := a.window.Canvas(); c != nil {
			sz := c.Size()
			prefs.SetFloat("window.width", float64(sz.Width))
			prefs.SetFloat("window.height", float64(sz.Height))
		}
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
			a.maybeNotify(s)
		}
	}
}

// maybeNotify decides whether a reporter.Status warrants a system
// notification. We keep this list tight — every event already shows up
// in the Activity tab, so we only ping the OS when something deserves
// the user's attention even if the window is minimised.
func (a *App) maybeNotify(s reporter.Status) {
	switch {
	case s.Level == reporter.LevelOK && strings.HasPrefix(s.Message, "Marked build "):
		a.notify("Build complete", s.Message)
	case s.Level == reporter.LevelOK && s.Message == "Signed in with Frontier (cAPI tokens cached)":
		a.notify("Frontier sign-in", "Signed in successfully.")
	case s.Level == reporter.LevelError && strings.HasPrefix(s.Message, "Tailer exited:"):
		a.notify("Journal tail stopped", s.Message)
	}
}

func (a *App) notify(title, body string) {
	a.app.SendNotification(&fyne.Notification{Title: title, Content: body})
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

// buildMenu builds the main menu bar. File contains Refresh + Quit;
// Help has the keyboard-shortcut reference and the About dialog.
func (a *App) buildMenu(tabs *container.AppTabs) *fyne.MainMenu {
	refresh := fyne.NewMenuItem("Refresh projects", func() { a.projects.refreshNow() })
	refresh.Shortcut = &fyne.ShortcutPaste{} // placeholder; shortcuts wired via Canvas already
	quit := fyne.NewMenuItem("Quit", func() { a.window.Close() })

	fileMenu := fyne.NewMenu("File", refresh, fyne.NewMenuItemSeparator(), quit)

	repo := fyne.NewMenuItem("Open repository", func() {
		_ = web.OpenBrowser("https://github.com/pequalsnp/ed-colonization-reporter")
	})
	shortcuts := fyne.NewMenuItem("Keyboard shortcuts…", a.showShortcutsDialog)
	about := fyne.NewMenuItem("About", a.showAboutDialog)

	helpMenu := fyne.NewMenu("Help", shortcuts, repo, fyne.NewMenuItemSeparator(), about)

	return fyne.NewMainMenu(fileMenu, helpMenu)
}

func (a *App) showAboutDialog() {
	body := container.NewVBox(
		labelLarge("ED Colonization Reporter"),
		labelMuted("Version "+a.srv.GetVersion()),
		widget.NewLabel(""),
		labelWrapped(
			"Linux-first colonization tracking and journal relay for Elite Dangerous. "+
				"Reports to ravencolonial.com and optionally EDDN, EDSM, Inara, and Frontier's cAPI.",
		),
		widget.NewLabel(""),
		labelMuted("MIT License — © 2026 Kyle Galloway"),
		widget.NewHyperlink("github.com/pequalsnp/ed-colonization-reporter",
			mustURL("https://github.com/pequalsnp/ed-colonization-reporter")),
	)
	dlg := dialog.NewCustom("About", "Close", body, a.window)
	dlg.Resize(fyne.NewSize(440, 260))
	dlg.Show()
}

func (a *App) showShortcutsDialog() {
	rows := [][2]string{
		{"Ctrl+R / F5", "Refresh projects"},
		{"Ctrl+1", "Projects tab"},
		{"Ctrl+2", "Activity tab"},
		{"Ctrl+3", "Settings tab"},
		{"Ctrl+Q", "Quit"},
	}
	grid := container.New(layout.NewFormLayout())
	for _, r := range rows {
		k := canvas.NewText(r[0], edFg)
		k.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
		k.TextSize = 12
		v := canvas.NewText(r[1], edFgMuted)
		v.TextSize = 12
		grid.Add(k)
		grid.Add(v)
	}
	dlg := dialog.NewCustom("Keyboard shortcuts", "Close", grid, a.window)
	dlg.Resize(fyne.NewSize(360, 220))
	dlg.Show()
}

func labelLarge(s string) fyne.CanvasObject {
	t := canvas.NewText(s, edFg)
	t.TextStyle = fyne.TextStyle{Bold: true}
	t.TextSize = 16
	return t
}

func labelMuted(s string) fyne.CanvasObject {
	t := canvas.NewText(s, edFgMuted)
	t.TextSize = 12
	return t
}

func labelWrapped(s string) fyne.CanvasObject {
	l := widget.NewLabel(s)
	l.Wrapping = fyne.TextWrapWord
	return l
}

func mustURL(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}

// registerShortcuts wires standard desktop keyboard shortcuts. Fyne's
// Canvas.AddShortcut handles the cross-platform Ctrl/Cmd modifier
// mapping (Cmd on macOS, Ctrl elsewhere) via KeyModifierShortcutDefault.
func (a *App) registerShortcuts(tabs *container.AppTabs) {
	mod := fyne.KeyModifierShortcutDefault
	canvas := a.window.Canvas()

	bind := func(key fyne.KeyName, action func()) {
		canvas.AddShortcut(&desktop.CustomShortcut{KeyName: key, Modifier: mod}, func(fyne.Shortcut) {
			action()
		})
	}

	bind(fyne.KeyR, func() { a.projects.refreshNow() })
	bind(fyne.Key1, func() { tabs.SelectIndex(0) })
	bind(fyne.Key2, func() { tabs.SelectIndex(1) })
	bind(fyne.Key3, func() { tabs.SelectIndex(2) })
	bind(fyne.KeyQ, func() { a.window.Close() })

	// F5 (no modifier) as a refresh alias — matches browser muscle memory.
	canvas.AddShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyF5}, func(fyne.Shortcut) {
		a.projects.refreshNow()
	})
}
