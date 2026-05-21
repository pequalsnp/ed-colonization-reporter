// Package ui builds the Fyne GUI.
package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// App is the GUI application. Build with New and call Run.
type App struct {
	cfg     config.Config
	session *state.Session
	client  *ravencolonial.Client
	rep     *reporter.Reporter

	fyneApp fyne.App
	window  fyne.Window

	// View state for the projects tab.
	projectsMu  sync.Mutex
	projects    []ravencolonial.Project
	projectsLbl *widget.Label
	projectsLst *widget.List

	// Activity log.
	activityMu  sync.Mutex
	activity    []reporter.Status
	activityLst *widget.List

	// Status bar widgets — bound to session updates.
	cmdrBinding   *widget.Label
	systemBinding *widget.Label
	dockBinding   *widget.Label

	// Lifecycle.
	cancel context.CancelFunc
}

// New constructs a UI App. It does not start any background goroutines or
// open windows; call Run for that.
func New(cfg config.Config) *App {
	sess := state.New()
	if cfg.CommanderOverride != "" {
		sess.SetCommander(cfg.CommanderOverride, "")
	}
	client := ravencolonial.New(
		ravencolonial.WithBaseURL(cfg.APIBaseURL),
		ravencolonial.WithAPIKey(cfg.APIKey),
	)
	rep := reporter.New(client, sess)
	return &App{
		cfg:     cfg,
		session: sess,
		client:  client,
		rep:     rep,
	}
}

// Run shows the main window and blocks until the user closes it.
func (a *App) Run() {
	a.fyneApp = fyneapp.NewWithID("ca.thegalloways.edcolonization")
	a.window = a.fyneApp.NewWindow("ED Colonization Reporter")
	a.window.Resize(fyne.NewSize(820, 560))

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.window.SetOnClosed(func() {
		cancel()
	})

	a.rep.OnStatus(a.appendActivity)

	a.window.SetContent(a.buildRoot())

	// Background workers.
	go a.runTailer(ctx)
	go a.refreshProjectsOnInterval(ctx, 60*time.Second)

	a.appendActivity(reporter.Status{
		Time: time.Now(), Level: reporter.LevelInfo,
		Message: "Starting up; tailing " + a.resolveJournalDir(),
	})

	a.window.ShowAndRun()
}

func (a *App) resolveJournalDir() string {
	if a.cfg.JournalDir != "" {
		return a.cfg.JournalDir
	}
	if dir, err := journal.FindJournalDir(); err == nil {
		return dir
	}
	return "(not detected — set one in Settings)"
}

func (a *App) buildRoot() fyne.CanvasObject {
	tabs := container.NewAppTabs(
		container.NewTabItem("Projects", a.buildProjectsTab()),
		container.NewTabItem("Activity", a.buildActivityTab()),
		container.NewTabItem("Settings", a.buildSettingsTab()),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	return container.NewBorder(nil, a.buildStatusBar(), nil, nil, tabs)
}

// --- Projects tab ----------------------------------------------------------

func (a *App) buildProjectsTab() fyne.CanvasObject {
	a.projectsLbl = widget.NewLabel("No projects loaded yet.")

	refreshBtn := widget.NewButton("Refresh", func() {
		go a.refreshProjects(context.Background())
	})

	a.projectsLst = widget.NewList(
		func() int {
			a.projectsMu.Lock()
			defer a.projectsMu.Unlock()
			return len(a.projects)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("template — long enough to be reused")
		},
		func(idx widget.ListItemID, obj fyne.CanvasObject) {
			a.projectsMu.Lock()
			p := a.projects[idx]
			a.projectsMu.Unlock()
			lbl := obj.(*widget.Label)
			lbl.SetText(formatProjectLine(p))
		},
	)

	top := container.NewBorder(nil, nil, nil, refreshBtn, a.projectsLbl)
	return container.NewBorder(top, nil, nil, nil, a.projectsLst)
}

func formatProjectLine(p ravencolonial.Project) string {
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

func (a *App) refreshProjects(ctx context.Context) {
	cmdr := a.session.Commander()
	if cmdr == "" && a.cfg.CommanderOverride != "" {
		cmdr = a.cfg.CommanderOverride
	}
	if cmdr == "" {
		fyne.Do(func() {
			a.projectsLbl.SetText("Commander unknown — start Elite Dangerous to populate.")
		})
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ps, err := a.client.ActiveProjects(rctx, cmdr)
	if err != nil {
		a.appendActivity(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: fmt.Sprintf("Refresh projects failed: %v", err),
		})
		return
	}
	a.projectsMu.Lock()
	a.projects = ps
	a.projectsMu.Unlock()
	fyne.Do(func() {
		a.projectsLbl.SetText(fmt.Sprintf("%d active project(s) for Cmdr %s", len(ps), cmdr))
		a.projectsLst.Refresh()
	})
}

func (a *App) refreshProjectsOnInterval(ctx context.Context, interval time.Duration) {
	a.refreshProjects(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.refreshProjects(ctx)
		}
	}
}

// --- Activity tab ----------------------------------------------------------

func (a *App) buildActivityTab() fyne.CanvasObject {
	a.activityLst = widget.NewList(
		func() int {
			a.activityMu.Lock()
			defer a.activityMu.Unlock()
			return len(a.activity)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(idx widget.ListItemID, obj fyne.CanvasObject) {
			a.activityMu.Lock()
			s := a.activity[idx]
			a.activityMu.Unlock()
			obj.(*widget.Label).SetText(formatStatus(s))
		},
	)
	return a.activityLst
}

func formatStatus(s reporter.Status) string {
	return fmt.Sprintf("[%s] %s  %s", s.Time.Format("15:04:05"), s.Level, s.Message)
}

func (a *App) appendActivity(s reporter.Status) {
	a.activityMu.Lock()
	a.activity = append(a.activity, s)
	// Cap at 500 lines so memory doesn't grow unboundedly during long sessions.
	if len(a.activity) > 500 {
		a.activity = a.activity[len(a.activity)-500:]
	}
	count := len(a.activity)
	a.activityMu.Unlock()

	if a.activityLst != nil {
		fyne.Do(func() {
			a.activityLst.Refresh()
			a.activityLst.ScrollTo(count - 1)
		})
	}
	a.refreshStatusBar()
}

// --- Settings tab ----------------------------------------------------------

func (a *App) buildSettingsTab() fyne.CanvasObject {
	journalEntry := widget.NewEntry()
	journalEntry.SetText(a.cfg.JournalDir)
	journalEntry.SetPlaceHolder(a.resolveJournalDir())

	apiBaseEntry := widget.NewEntry()
	apiBaseEntry.SetText(a.cfg.APIBaseURL)
	apiBaseEntry.SetPlaceHolder(ravencolonial.DefaultBaseURL)

	apiKeyEntry := widget.NewPasswordEntry()
	apiKeyEntry.SetText(a.cfg.APIKey)
	apiKeyEntry.SetPlaceHolder("Optional — get from ravencolonial.com/user for Fleet Carrier writes")

	cmdrEntry := widget.NewEntry()
	cmdrEntry.SetText(a.cfg.CommanderOverride)
	cmdrEntry.SetPlaceHolder("Leave blank to use the commander parsed from the journal")

	notice := widget.NewLabel("")
	notice.Wrapping = fyne.TextWrapWord

	form := widget.NewForm(
		widget.NewFormItem("Journal directory", journalEntry),
		widget.NewFormItem("API base URL", apiBaseEntry),
		widget.NewFormItem("API key (rcc-key)", apiKeyEntry),
		widget.NewFormItem("Commander override", cmdrEntry),
	)
	form.SubmitText = "Save"
	form.OnSubmit = func() {
		newCfg := config.Config{
			JournalDir:        journalEntry.Text,
			APIBaseURL:        apiBaseEntry.Text,
			APIKey:            apiKeyEntry.Text,
			CommanderOverride: cmdrEntry.Text,
		}
		if newCfg.APIBaseURL == "" {
			newCfg.APIBaseURL = ravencolonial.DefaultBaseURL
		}
		if err := config.Save(newCfg); err != nil {
			notice.SetText("Save failed: " + err.Error())
			return
		}
		a.cfg = newCfg
		// Rebuild the client and reporter with the new config; the existing
		// tailer goroutine keeps running and will hot-swap on next event.
		a.client = ravencolonial.New(
			ravencolonial.WithBaseURL(newCfg.APIBaseURL),
			ravencolonial.WithAPIKey(newCfg.APIKey),
		)
		a.rep = reporter.New(a.client, a.session)
		a.rep.OnStatus(a.appendActivity)
		if newCfg.CommanderOverride != "" {
			a.session.SetCommander(newCfg.CommanderOverride, "")
		}
		notice.SetText("Saved. Restart the app to pick up a new journal directory.")
	}

	return container.NewVBox(form, notice)
}

// --- Status bar ------------------------------------------------------------

func (a *App) buildStatusBar() fyne.CanvasObject {
	a.cmdrBinding = widget.NewLabel("Cmdr: —")
	a.systemBinding = widget.NewLabel("System: —")
	a.dockBinding = widget.NewLabel("Dock: undocked")
	a.refreshStatusBar()
	return container.NewHBox(a.cmdrBinding, widget.NewSeparator(), a.systemBinding, widget.NewSeparator(), a.dockBinding)
}

func (a *App) refreshStatusBar() {
	if a.cmdrBinding == nil {
		return
	}
	snap := a.session.Snapshot()
	cmdr := snap.Commander
	if cmdr == "" {
		cmdr = "—"
	}
	sys := snap.StarSystem
	if sys == "" {
		sys = "—"
	}
	dock := "undocked"
	if snap.Docked {
		dock = snap.StationName
	}
	fyne.Do(func() {
		a.cmdrBinding.SetText("Cmdr: " + cmdr)
		a.systemBinding.SetText("System: " + sys)
		a.dockBinding.SetText("Dock: " + dock)
	})
}

// --- Tailer goroutine ------------------------------------------------------

func (a *App) runTailer(ctx context.Context) {
	dir := a.cfg.JournalDir
	if dir == "" {
		if d, err := journal.FindJournalDir(); err == nil {
			dir = d
		}
	}
	if dir == "" {
		a.appendActivity(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "No journal directory configured or detected. Set one in Settings.",
		})
		return
	}
	if err := journal.IsJournalDirReadable(dir); err != nil {
		a.appendActivity(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: fmt.Sprintf("Journal directory %s unreadable: %v", dir, err),
		})
		return
	}

	tl := &journal.Tailer{Dir: dir, StartAt: journal.StartAtEnd}
	events := make(chan journal.Raw, 32)

	tailErr := make(chan error, 1)
	go func() { tailErr <- tl.Run(ctx, events) }()

	for raw := range events {
		if err := a.rep.HandleEvent(ctx, raw); err != nil {
			// Reporter already emits a status; nothing extra to do here.
			_ = err
		}
	}
	if err := <-tailErr; err != nil && err != context.Canceled && err != context.DeadlineExceeded {
		a.appendActivity(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "Tailer exited: " + err.Error(),
		})
	}
}
