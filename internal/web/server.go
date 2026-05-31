// Package web serves a local browser-based UI for the colonization reporter.
//
// On startup, a Server listens on a chosen loopback address and opens the
// user's default browser to it. The page polls JSON endpoints for status
// and active projects and subscribes via Server-Sent Events for the live
// activity log.
package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/eddn"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/edsm"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/inara"
	"github.com/pequalsnp/ed-colonization-reporter/internal/edsy"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// Server is the long-running local HTTP server + background tailer that
// together comprise the running app.
type Server struct {
	Version string
	// Bind, if set, overrides the default loopback bind ("127.0.0.1:0").
	// "0" port = OS-assigned.
	Bind string
	// OpenBrowser, if non-nil, is called with the listening URL after Start
	// returns. Production uses openBrowser; tests pass a no-op.
	OpenBrowser func(url string)

	mu      sync.Mutex
	cfg     config.Config
	cfgPath string

	session *state.Session
	client  *ravencolonial.Client
	rep     *reporter.Reporter
	eddn    *eddn.Uploader
	edsm    *edsm.Uploader
	inara   *inara.Uploader
	mux     *destinations.Multiplex

	// lastEventAt is the wall-clock time (unix nanos) of the most
	// recent journal event the tailer handed to the multiplex.
	// Atomic for lock-free reads from the GUI's liveness indicator.
	lastEventAt atomic.Int64

	// startedAt is when New() ran; used for the About dialog uptime.
	startedAt time.Time
	// firstRun is set by main.go when config.Load reported no existing
	// file. The GUI uses it to decide whether to show a welcome dialog.
	firstRun bool

	// activityLog persists every statusHub entry to disk for offline
	// reading. Optional; nil if open failed.
	activityLog *activityFileLogger

	hub      *statusHub
	listener net.Listener
	srv      *http.Server

	// tailer lifecycle
	tailerCancel context.CancelFunc
}

// New creates a Server with the initial config. firstRun indicates
// whether the user has never run the app before (no config file existed
// on disk), so the GUI can decide whether to show a welcome dialog.
func New(cfg config.Config) *Server {
	return &Server{
		cfg:       cfg,
		hub:       newStatusHub(),
		startedAt: time.Now(),
	}
}

// SetFirstRun records that this launch found no pre-existing config.
func (s *Server) SetFirstRun(b bool) { s.firstRun = b }

// FirstRun reports whether the app started without a pre-existing config.
func (s *Server) FirstRun() bool { return s.firstRun }

// URL returns the http URL the server is listening on. Empty until Start.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return "http://" + s.listener.Addr().String()
}

// SessionStartedAt returns when the backend Server was constructed.
// Used by the GUI's About dialog to show session uptime.
func (s *Server) SessionStartedAt() time.Time { return s.startedAt }

// Session exposes the live state.Session so in-process consumers (the
// Fyne GUI) can read commander, system, dock, etc. directly without
// going through JSON.
func (s *Server) Session() *state.Session {
	return s.session
}

// Subscribe returns a channel of reporter.Status events. The returned
// cancel function MUST be called to release the subscription. Used by
// the GUI's Activity panel.
func (s *Server) Subscribe() (<-chan reporter.Status, func()) {
	return s.hub.Subscribe()
}

// Version returns the build version that was passed in.
func (s *Server) GetVersion() string { return s.Version }

// Config returns a copy of the current config. Safe to read concurrently.
func (s *Server) Config() config.Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// ApplyConfig persists a new config and hot-updates every destination
// that supports runtime reconfiguration (EDDN, EDSM, Inara, Frontier
// cAPI, ravencolonial client). The GUI calls this from its settings
// panel.
func (s *Server) ApplyConfig(newCfg config.Config) error {
	if newCfg.APIBaseURL == "" {
		newCfg.APIBaseURL = ravencolonial.DefaultBaseURL
	}
	if err := config.Save(newCfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	s.mu.Lock()
	s.cfg = newCfg
	s.client = ravencolonial.New(
		ravencolonial.WithBaseURL(newCfg.APIBaseURL),
		ravencolonial.WithAPIKey(newCfg.APIKey),
	)
	s.rep = reporter.New(s.client, s.session)
	s.rep.JournalDir = resolveJournalDir(newCfg.JournalDir)
	s.rep.OnStatus(s.hub.Publish)
	if newCfg.CommanderOverride != "" {
		s.session.SetCommander(newCfg.CommanderOverride, "")
	}
	if s.eddn != nil {
		s.eddn.SetEnabled(newCfg.EDDNEnabled)
		s.eddn.JournalDir = resolveJournalDir(newCfg.JournalDir)
	}
	if s.edsm != nil {
		s.edsm.SetAPIKey(newCfg.EDSMAPIKey)
		s.edsm.SetEnabled(newCfg.EDSMEnabled)
	}
	if s.inara != nil {
		s.inara.SetAPIKey(newCfg.InaraAPIKey)
		s.inara.SetEnabled(newCfg.InaraEnabled)
	}
	if s.mux != nil {
		s.mux.Replace(s.rep, s.eddn, s.edsm, s.inara)
	}
	s.mu.Unlock()
	s.hub.Publish(reporter.Status{
		Time: time.Now(), Level: reporter.LevelOK,
		Message: "Settings saved.",
	})
	return nil
}

// ActiveProjects fetches the commander's active builds from ravencolonial.
// Used by the GUI's Projects panel.
func (s *Server) ActiveProjects(ctx context.Context) ([]ravencolonial.Project, string, error) {
	cmdr := s.session.Commander()
	if cmdr == "" && s.cfg.CommanderOverride != "" {
		cmdr = s.cfg.CommanderOverride
	}
	if cmdr == "" {
		return nil, "", nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ps, err := s.client.ActiveProjects(cctx, cmdr)
	return ps, cmdr, err
}

// runActivityFileLog drains the statusHub to the file-backed logger
// until the context is cancelled. One goroutine per server lifetime.
func (s *Server) runActivityFileLog(ctx context.Context) {
	ch, cancel := s.hub.Subscribe()
	defer cancel()
	defer s.activityLog.close()
	for {
		select {
		case <-ctx.Done():
			return
		case status, ok := <-ch:
			if !ok {
				return
			}
			s.activityLog.write(status)
		}
	}
}

// ActivityLogPath returns the on-disk location of the persistent activity
// log. The GUI exposes this in the Help menu so users can find or share it.
func (s *Server) ActivityLogPath() string { return resolveActivityLogPath() }

// LastEventAt returns the wall-clock time of the most recent journal
// event the tailer processed. Returns the zero time if nothing has
// arrived yet (game not running, journal dir misconfigured, etc.).
func (s *Server) LastEventAt() time.Time {
	if v := s.lastEventAt.Load(); v != 0 {
		return time.Unix(0, v)
	}
	return time.Time{}
}

// LastShipCargo returns the current ship cargo manifest plus the
// timestamp it was last refreshed. Reads from state.Session, populated
// by every journal Cargo event.
func (s *Server) LastShipCargo() (cargo map[string]int, at time.Time) {
	return s.session.ShipCargo()
}

// CurrentMarket returns the {commodity: Stock} map and station name of
// the market the player most recently interacted with (commodities-
// market UI opened), or ("", nil, zero) when none has been seen this
// session / the player has undocked since.
func (s *Server) CurrentMarket() (station string, stock map[string]int, at time.Time) {
	return s.session.CurrentMarket()
}

// CurrentShip returns a short display label for the player's current ship
// (name if set, else type) and whether a Loadout has been seen this session.
// Cheap — safe to poll from the UI refresh loop.
func (s *Server) CurrentShip() (label string, ok bool) {
	if _, has := s.session.ShipLoadout(); !has {
		return "", false
	}
	shipType, shipName, _ := s.session.Ship()
	switch {
	case shipName != "":
		return shipName, true
	case shipType != "":
		return shipType, true
	default:
		return "current ship", true
	}
}

// EDSYShipURL builds an edsy.org import link for the player's current ship
// from the last Loadout event. Returns ("", false) before any Loadout has
// been seen this session. Builds the (gzip+base64) link on demand, so call
// it on user action rather than in a poll loop.
func (s *Server) EDSYShipURL() (string, bool) {
	loadout, has := s.session.ShipLoadout()
	if !has {
		return "", false
	}
	u, err := edsy.URL(loadout)
	if err != nil {
		return "", false
	}
	return u, true
}

// LastFCInventory returns the aggregated cargo across all owned FCs
// (in practice: the single one) plus the most-recent carrier name and
// update time. Reads from state.Session, populated by RC GET at boot
// and live deltas.
func (s *Server) LastFCInventory() (name string, cargo ravencolonial.Cargo, at time.Time) {
	n, c, t := s.session.FCCargoAggregate()
	if c == nil {
		return "", nil, time.Time{}
	}
	out := make(ravencolonial.Cargo, len(c))
	for k, v := range c {
		out[k] = v
	}
	return n, out, t
}

// Start binds the listener, wires the reporter/tailer, and serves until ctx
// is cancelled. Returns nil on clean shutdown, an error otherwise.
func (s *Server) Start(ctx context.Context) error {
	bind := s.Bind
	if bind == "" {
		bind = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", bind)
	if err != nil {
		return fmt.Errorf("listen %s: %w", bind, err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	if err := s.initSessionAndReporter(); err != nil {
		ln.Close()
		return err
	}

	mux := s.routes()
	s.srv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	tailerCtx, cancel := context.WithCancel(ctx)
	s.tailerCancel = cancel
	go s.runTailer(tailerCtx)

	// Disk-persist every status entry so the user (and bug-reporters)
	// can read history after the app closes. Best-effort; log failures
	// don't break the running session.
	s.activityLog = newActivityFileLogger(resolveActivityLogPath())
	if err := s.activityLog.start(); err == nil {
		go s.runActivityFileLog(tailerCtx)
	} else {
		s.activityLog = nil
	}

	// Fetch the EDSM discard list on startup (and refresh periodically).
	// Safe to start even when EDSM is disabled — it's a tiny HTTP call.
	s.edsm.StartBackground(tailerCtx)
	// Spin up the Inara batch flusher. Runs even when Inara is disabled;
	// Flush() is a no-op without an API key.
	s.inara.StartBackground(tailerCtx, 0)

	if s.OpenBrowser != nil {
		s.OpenBrowser(s.URL())
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, c2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer c2()
		_ = s.srv.Shutdown(shutdownCtx)
		s.tailerCancel()
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		s.tailerCancel()
		return err
	}
}

func (s *Server) initSessionAndReporter() error {
	if s.session == nil {
		s.session = state.New()
	}
	if s.cfg.CommanderOverride != "" {
		s.session.SetCommander(s.cfg.CommanderOverride, "")
	}
	s.client = ravencolonial.New(
		ravencolonial.WithBaseURL(s.cfg.APIBaseURL),
		ravencolonial.WithAPIKey(s.cfg.APIKey),
	)
	s.rep = reporter.New(s.client, s.session)
	s.rep.JournalDir = resolveJournalDir(s.cfg.JournalDir)
	s.rep.OnStatus(s.hub.Publish)

	statusBridge := func(level, msg string) {
		s.hub.Publish(reporter.Status{Time: time.Now(), Level: parseLevel(level), Message: msg})
	}

	s.eddn = eddn.New(eddn.SoftwareID{Name: "edcolreport", Version: s.Version}, s.session)
	s.eddn.JournalDir = resolveJournalDir(s.cfg.JournalDir)
	s.eddn.OnStatus = statusBridge
	s.eddn.SetEnabled(s.cfg.EDDNEnabled)
	if s.cfg.EDDNTestMode {
		s.eddn.TestMode = true
		s.eddn.Endpoint = eddn.BetaEndpoint
	}

	s.edsm = edsm.New(edsm.SoftwareID{Name: "edcolreport", Version: s.Version}, s.session)
	s.edsm.OnStatus = statusBridge
	s.edsm.SetAPIKey(s.cfg.EDSMAPIKey)
	s.edsm.SetEnabled(s.cfg.EDSMEnabled)

	inaraName := s.cfg.InaraAppName
	if inaraName == "" {
		inaraName = "edcolreport"
	}
	s.inara = inara.New(inara.SoftwareID{Name: inaraName, Version: s.Version}, s.session)
	s.inara.OnStatus = statusBridge
	s.inara.SetAPIKey(s.cfg.InaraAPIKey)
	s.inara.SetEnabled(s.cfg.InaraEnabled)

	s.mux = destinations.NewMultiplex(s.rep, s.eddn, s.edsm, s.inara)
	s.mux.OnError = func(name string, err error) {
		// Don't surface per-event errors here — destinations emit their own
		// user-visible status messages. This callback exists for diagnostics
		// only.
		_ = err
	}
	return nil
}

// parseLevel maps a string log level to the reporter.Level enum.
func parseLevel(s string) reporter.Level {
	switch s {
	case "OK":
		return reporter.LevelOK
	case "WARN":
		return reporter.LevelWarn
	case "ERROR":
		return reporter.LevelError
	default:
		return reporter.LevelInfo
	}
}

func resolveJournalDir(configured string) string {
	if configured != "" {
		return configured
	}
	if dir, err := journal.FindJournalDir(); err == nil {
		return dir
	}
	return ""
}

func (s *Server) runTailer(ctx context.Context) {
	dir := resolveJournalDir(s.cfg.JournalDir)
	if dir == "" {
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "No journal directory configured or detected. Set one in Settings.",
		})
		return
	}
	if err := journal.IsJournalDirReadable(dir); err != nil {
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: fmt.Sprintf("Journal directory %s unreadable: %v", dir, err),
		})
		return
	}
	startAt := journal.StartAtEnd
	if s.cfg.ReplaySession {
		startAt = journal.StartAtBeginning
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelInfo,
			Message: "Backfill enabled: replaying current journal file from start.",
		})
	}
	s.hub.Publish(reporter.Status{
		Time: time.Now(), Level: reporter.LevelInfo,
		Message: "Tailing " + dir,
	})

	tl := &journal.Tailer{
		Dir:     dir,
		StartAt: startAt,
	}
	events := make(chan journal.Raw, 64)
	tailErr := make(chan error, 1)
	go func() { tailErr <- tl.Run(ctx, events) }()

	for raw := range events {
		// Multiplex dispatches to every configured destination (ravencolonial,
		// EDDN, and any future ones). Each destination logs its own errors.
		s.lastEventAt.Store(time.Now().UnixNano())
		_ = s.mux.HandleEvent(ctx, raw)
	}
	if err := <-tailErr; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "Tailer exited: " + err.Error(),
		})
	}
}

// staticHandler serves the embedded HTML at /.
func (s *Server) staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// embed.FS always has the subdir we declared; panic is a bug.
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
