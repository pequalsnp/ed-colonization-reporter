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
	"fmt"
	"sync"
	"time"

	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// App is the Fyne window owner. It is built around a *web.Server which
// owns all backend state — Session, destinations, Frontier OAuth, the
// status hub, etc.
type App struct {
	srv *web.Server

	app    fyne.App
	window fyne.Window

	cmdrLabel, systemLabel, dockLabel *widget.Label
	versionLabel                      *widget.Label

	projects        *projectsPanel
	activity        *activityPanel
	settings        *settingsPanel
	frontierPanel   *frontierPanel
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
	a.window = a.app.NewWindow("ED Colonization Reporter")
	a.window.Resize(fyne.NewSize(960, 680))
	return a
}

func (a *App) show(ctx context.Context) {
	a.cmdrLabel = widget.NewLabel("Cmdr: —")
	a.systemLabel = widget.NewLabel("System: —")
	a.dockLabel = widget.NewLabel("Dock: —")
	a.versionLabel = widget.NewLabel("v" + a.srv.GetVersion())

	statusBar := container.NewHBox(
		a.cmdrLabel, widget.NewSeparator(),
		a.systemLabel, widget.NewSeparator(),
		a.dockLabel,
		widget.NewLabel(""), // spacer pushes version to the right when window is wide
		a.versionLabel,
	)

	a.projects = newProjectsPanel(a.srv)
	a.activity = newActivityPanel()
	a.settings = newSettingsPanel(a.srv)
	a.frontierPanel = newFrontierPanel(a.srv)

	tabs := container.NewAppTabs(
		container.NewTabItem("Projects", a.projects.content()),
		container.NewTabItem("Activity", a.activity.content()),
		container.NewTabItem("Settings", a.settings.content(a.frontierPanel)),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	root := container.NewBorder(statusBar, nil, nil, nil, tabs)
	a.window.SetContent(root)

	// Background goroutines: status bar polling, activity stream, periodic
	// project list refresh, Frontier status polling. All redirect their UI
	// mutations through fyne.Do (Fyne's main-thread dispatch since v2.6).
	subCtx, cancel := context.WithCancel(ctx)
	a.window.SetOnClosed(func() {
		cancel()
		a.app.Quit()
	})

	go a.runStatusBarLoop(subCtx)
	go a.runActivityLoop(subCtx)
	go a.projects.runAutoRefresh(subCtx)
	go a.frontierPanel.runStatusLoop(subCtx)

	a.window.ShowAndRun()
}

func (a *App) runStatusBarLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	update := func() {
		sess := a.srv.Session()
		snap := sess.Snapshot()
		fyne.Do(func() {
			a.cmdrLabel.SetText("Cmdr: " + dashIfEmpty(snap.Commander))
			a.systemLabel.SetText("System: " + dashIfEmpty(snap.StarSystem))
			if snap.Docked && snap.StationName != "" {
				a.dockLabel.SetText("Dock: " + snap.StationName)
			} else {
				a.dockLabel.SetText("Dock: undocked")
			}
		})
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

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// ---- Projects panel --------------------------------------------------------

type projectsPanel struct {
	srv *web.Server

	mu       sync.Mutex
	rows     []ravencolonial.Project
	commander string
	err       string

	summary  *widget.Label
	list     *widget.List
	refresh  *widget.Button
}

func newProjectsPanel(srv *web.Server) *projectsPanel {
	p := &projectsPanel{srv: srv}
	p.summary = widget.NewLabel("Loading…")
	p.list = widget.NewList(
		func() int {
			p.mu.Lock()
			defer p.mu.Unlock()
			return len(p.rows)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("template — long enough to size correctly")
		},
		func(idx widget.ListItemID, obj fyne.CanvasObject) {
			p.mu.Lock()
			pr := p.rows[idx]
			p.mu.Unlock()
			obj.(*widget.Label).SetText(formatProject(pr))
		},
	)
	p.refresh = widget.NewButton("Refresh", func() { go p.refreshNow() })
	return p
}

func (p *projectsPanel) content() fyne.CanvasObject {
	top := container.NewBorder(nil, nil, nil, p.refresh, p.summary)
	return container.NewBorder(top, nil, nil, nil, p.list)
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
	fyne.Do(func() { p.summary.SetText("Loading…") })
	rows, cmdr, err := p.srv.ActiveProjects(context.Background())
	p.mu.Lock()
	p.rows = rows
	p.commander = cmdr
	if err != nil {
		p.err = err.Error()
	} else {
		p.err = ""
	}
	count := len(rows)
	p.mu.Unlock()

	fyne.Do(func() {
		switch {
		case p.err != "":
			p.summary.SetText("Refresh failed: " + p.err)
		case p.commander == "":
			p.summary.SetText("Commander unknown — start Elite Dangerous to populate.")
		default:
			p.summary.SetText(fmt.Sprintf("%d active project%s for Cmdr %s", count, plural(count), p.commander))
		}
		p.list.Refresh()
	})
}

func formatProject(p ravencolonial.Project) string {
	name := p.BuildName
	if name == "" {
		name = p.BuildID
	}
	sysName := p.SystemName
	if sysName == "" {
		sysName = "(unknown system)"
	}
	outstanding := 0
	for _, n := range p.Commodities {
		outstanding += n
	}
	status := "active"
	if p.Complete {
		status = "complete"
	}
	return fmt.Sprintf("%s — %s [%s] %d units outstanding", sysName, name, status, outstanding)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---- Activity panel --------------------------------------------------------

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
		func() fyne.CanvasObject {
			lbl := widget.NewLabel("template line")
			lbl.Wrapping = fyne.TextWrapWord
			return lbl
		},
		func(idx widget.ListItemID, obj fyne.CanvasObject) {
			p.mu.Lock()
			s := p.entries[idx]
			p.mu.Unlock()
			obj.(*widget.Label).SetText(formatStatus(s))
		},
	)
	return p
}

func (p *activityPanel) content() fyne.CanvasObject {
	return p.list
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

func formatStatus(s reporter.Status) string {
	return fmt.Sprintf("[%s] %s  %s", s.Time.Format("15:04:05"), s.Level, s.Message)
}

// ---- Settings panel --------------------------------------------------------

type settingsPanel struct {
	srv *web.Server

	journalDir, apiBase, apiKey, cmdrOverride            *widget.Entry
	edsmKey, inaraKey                                    *widget.Entry
	replaySession, eddnEnabled, edsmEnabled, inaraEnabled *widget.Check
	frontierCAPIEnabled                                  *widget.Check
	notice                                               *widget.Label
}

func newSettingsPanel(srv *web.Server) *settingsPanel {
	p := &settingsPanel{srv: srv}
	cfg := srv.Config()

	p.journalDir = widget.NewEntry()
	p.journalDir.SetText(cfg.JournalDir)
	p.journalDir.SetPlaceHolder("auto-detected if blank")

	p.apiBase = widget.NewEntry()
	p.apiBase.SetText(cfg.APIBaseURL)
	p.apiBase.SetPlaceHolder(ravencolonial.DefaultBaseURL)

	p.apiKey = widget.NewPasswordEntry()
	p.apiKey.SetText(cfg.APIKey)
	p.apiKey.SetPlaceHolder("optional; get from ravencolonial.com/user")

	p.cmdrOverride = widget.NewEntry()
	p.cmdrOverride.SetText(cfg.CommanderOverride)
	p.cmdrOverride.SetPlaceHolder("leave blank to use the commander in the journal")

	p.replaySession = widget.NewCheck("Replay current journal session on startup (backfill)", nil)
	p.replaySession.SetChecked(cfg.ReplaySession)

	p.eddnEnabled = widget.NewCheck("Upload to EDDN (anonymous community data — no key)", nil)
	p.eddnEnabled.SetChecked(cfg.EDDNEnabled)

	p.edsmEnabled = widget.NewCheck("Upload to EDSM (requires API key)", nil)
	p.edsmEnabled.SetChecked(cfg.EDSMEnabled)
	p.edsmKey = widget.NewPasswordEntry()
	p.edsmKey.SetText(cfg.EDSMAPIKey)
	p.edsmKey.SetPlaceHolder("from edsm.net/en/settings/api")

	p.inaraEnabled = widget.NewCheck("Upload to Inara (requires API key)", nil)
	p.inaraEnabled.SetChecked(cfg.InaraEnabled)
	p.inaraKey = widget.NewPasswordEntry()
	p.inaraKey.SetText(cfg.InaraAPIKey)
	p.inaraKey.SetPlaceHolder("from inara.cz/settings-api/")

	p.frontierCAPIEnabled = widget.NewCheck("Use cAPI for FC inventory ground-truth (sign in below)", nil)
	p.frontierCAPIEnabled.SetChecked(cfg.FrontierCAPIEnabled)

	p.notice = widget.NewLabel("")
	p.notice.Wrapping = fyne.TextWrapWord

	return p
}

func (p *settingsPanel) content(frontier *frontierPanel) fyne.CanvasObject {
	form := widget.NewForm(
		widget.NewFormItem("Journal directory", p.journalDir),
		widget.NewFormItem("API base URL", p.apiBase),
		widget.NewFormItem("rcc-key (ravencolonial)", p.apiKey),
		widget.NewFormItem("Commander override", p.cmdrOverride),
		widget.NewFormItem("Backfill", p.replaySession),
		widget.NewFormItem("EDDN", p.eddnEnabled),
		widget.NewFormItem("EDSM", p.edsmEnabled),
		widget.NewFormItem("EDSM API key", p.edsmKey),
		widget.NewFormItem("Inara", p.inaraEnabled),
		widget.NewFormItem("Inara API key", p.inaraKey),
		widget.NewFormItem("Frontier cAPI", p.frontierCAPIEnabled),
	)
	form.SubmitText = "Save"
	form.OnSubmit = p.save

	return container.NewVScroll(container.NewVBox(
		form,
		p.notice,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Frontier sign-in", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		frontier.content(),
	))
}

func (p *settingsPanel) save() {
	newCfg := config.Config{
		JournalDir:          p.journalDir.Text,
		APIBaseURL:          p.apiBase.Text,
		APIKey:              p.apiKey.Text,
		CommanderOverride:   p.cmdrOverride.Text,
		ReplaySession:       p.replaySession.Checked,
		EDDNEnabled:         p.eddnEnabled.Checked,
		EDSMEnabled:         p.edsmEnabled.Checked,
		EDSMAPIKey:          p.edsmKey.Text,
		InaraEnabled:        p.inaraEnabled.Checked,
		InaraAPIKey:         p.inaraKey.Text,
		FrontierCAPIEnabled: p.frontierCAPIEnabled.Checked,
	}
	if err := p.srv.ApplyConfig(newCfg); err != nil {
		fyne.Do(func() { p.notice.SetText("Save failed: " + err.Error()) })
		return
	}
	fyne.Do(func() { p.notice.SetText("Saved. Some changes (journal dir) take effect on next startup.") })
}

// ---- Frontier sign-in panel ------------------------------------------------

type frontierPanel struct {
	srv *web.Server

	status   *widget.Label
	signin   *widget.Button
	signout  *widget.Button
}

func newFrontierPanel(srv *web.Server) *frontierPanel {
	p := &frontierPanel{srv: srv}
	p.status = widget.NewLabel("…")
	p.status.Wrapping = fyne.TextWrapWord
	p.signin = widget.NewButton("Sign in with Frontier", func() { go p.doSignin() })
	p.signout = widget.NewButton("Sign out", func() { go p.doSignout() })
	p.signout.Hide()
	return p
}

func (p *frontierPanel) content() fyne.CanvasObject {
	return container.NewVBox(p.status, container.NewHBox(p.signin, p.signout))
}

func (p *frontierPanel) runStatusLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	p.refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.refresh()
		}
	}
}

func (p *frontierPanel) refresh() {
	signed, expiresAt, expired := p.srv.FrontierStatus()
	fyne.Do(func() {
		if signed {
			msg := "Signed in"
			if !expiresAt.IsZero() {
				msg += " — token expires " + expiresAt.Local().Format("15:04 02 Jan")
			}
			if expired {
				msg += " (will refresh on next use)"
			}
			p.status.SetText(msg)
			p.signin.Hide()
			p.signout.Show()
		} else {
			p.status.SetText("Not signed in. Click Sign in to authorise with Frontier; a browser tab will open.")
			p.signin.Show()
			p.signout.Hide()
		}
	})
}

func (p *frontierPanel) doSignin() {
	url, err := p.srv.FrontierStartSignin()
	if err != nil {
		fyne.Do(func() { p.status.SetText("Sign-in failed: " + err.Error()) })
		return
	}
	// Open the auth URL in the user's default browser.
	if err := web.OpenBrowser(url); err != nil {
		fyne.Do(func() {
			p.status.SetText("Could not open browser; copy this URL into your browser: " + url)
		})
		return
	}
	fyne.Do(func() {
		p.status.SetText("Opened Frontier auth in your browser — finish there, then return here.")
	})
}

func (p *frontierPanel) doSignout() {
	if err := p.srv.FrontierSignout(); err != nil {
		fyne.Do(func() { p.status.SetText("Sign-out failed: " + err.Error()) })
		return
	}
	p.refresh()
}
