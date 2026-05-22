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
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/eddn"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/edsm"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/inara"
	"github.com/pequalsnp/ed-colonization-reporter/internal/frontier"
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

	frontierFlow    *frontier.FlowManager
	frontierCAPI    *frontier.CAPI
	frontierStore   frontier.TokenStore
	frontierTrigger chan struct{} // buffered(1): kick the cAPI poller

	// lastEventAt is the wall-clock time (unix nanos) of the most
	// recent journal event the tailer handed to the multiplex.
	// Atomic for lock-free reads from the GUI's liveness indicator.
	lastEventAt atomic.Int64

	// startedAt is when New() ran; used for the About dialog uptime.
	startedAt time.Time
	// firstRun is set by main.go when config.Load reported no existing
	// file. The GUI uses it to decide whether to show a welcome dialog.
	firstRun bool

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
		cfg:             cfg,
		hub:             newStatusHub(),
		frontierTrigger: make(chan struct{}, 1),
		startedAt:       time.Now(),
	}
}

// SetFirstRun records that this launch found no pre-existing config.
func (s *Server) SetFirstRun(b bool) { s.firstRun = b }

// FirstRun reports whether the app started without a pre-existing config.
func (s *Server) FirstRun() bool { return s.firstRun }

// kickFrontierSync requests an immediate cAPI poll on the next tick of
// the sync goroutine. Non-blocking: if the trigger is already pending,
// the second kick is dropped.
func (s *Server) kickFrontierSync() {
	select {
	case s.frontierTrigger <- struct{}{}:
	default:
	}
}

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

// FrontierTokenPath returns the on-disk location of the persisted
// Frontier OAuth tokens. Useful for support / backup / "where do I
// reset this?" questions.
func (s *Server) FrontierTokenPath() string { return resolveFrontierTokenPath() }

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
	prevCAPI := s.cfg.FrontierCAPIEnabled
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
	shouldKick := newCfg.FrontierCAPIEnabled && !prevCAPI
	s.mu.Unlock()
	if shouldKick {
		s.kickFrontierSync()
	}
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

// FrontierStartSignin generates a new auth URL for the user's browser.
func (s *Server) FrontierStartSignin() (string, error) {
	s.mu.Lock()
	ln := s.listener
	s.mu.Unlock()
	if ln == nil {
		return "", errors.New("server not bound")
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "", errors.New("server not bound to TCP")
	}
	return s.frontierFlow.Start(addr.Port)
}

// FrontierStatus reports whether tokens are present + their expiry. The
// (false, time.Time{}, nil) tuple means "not signed in."
func (s *Server) FrontierStatus() (signedIn bool, expiresAt time.Time, expired bool) {
	tok, err := s.frontierStore.Load()
	if err != nil || tok == nil || tok.AccessToken == "" {
		return false, time.Time{}, false
	}
	return true, tok.ExpiresAt, tok.Expired()
}

// FrontierClientID returns the OAuth client_id currently in use.
func (s *Server) FrontierClientID() string {
	if s.frontierFlow == nil {
		return ""
	}
	return s.frontierFlow.ClientID
}

// LastEventAt returns the wall-clock time of the most recent journal
// event the tailer processed. Returns the zero time if nothing has
// arrived yet (game not running, journal dir misconfigured, etc.).
func (s *Server) LastEventAt() time.Time {
	if v := s.lastEventAt.Load(); v != 0 {
		return time.Unix(0, v)
	}
	return time.Time{}
}

// ForceFCSync kicks the cAPI /fleetcarrier poller. Honors the 15-min
// server-side cooldown — if we already polled recently, the next call
// will silently no-op (cAPI returns ErrFleetCarrierRateLimited which
// the poller swallows). Exposed for the GUI's "Refresh FC now" button.
func (s *Server) ForceFCSync() { s.kickFrontierSync() }

// FrontierSignout discards stored tokens.
func (s *Server) FrontierSignout() error {
	if err := s.frontierStore.Clear(); err != nil {
		return err
	}
	s.frontierCAPI.SetTokens(nil)
	s.hub.Publish(reporter.Status{
		Time: time.Now(), Level: reporter.LevelInfo,
		Message: "Signed out of Frontier",
	})
	return nil
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

	// Fetch the EDSM discard list on startup (and refresh periodically).
	// Safe to start even when EDSM is disabled — it's a tiny HTTP call.
	s.edsm.StartBackground(tailerCtx)
	// Spin up the Inara batch flusher. Runs even when Inara is disabled;
	// Flush() is a no-op without an API key.
	s.inara.StartBackground(tailerCtx, 0)
	// Background poller for the Frontier cAPI /fleetcarrier endpoint.
	go s.runFrontierCAPISync(tailerCtx)

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

	s.inara = inara.New(inara.SoftwareID{Name: "edcolreport", Version: s.Version}, s.session)
	s.inara.OnStatus = statusBridge
	s.inara.SetAPIKey(s.cfg.InaraAPIKey)
	s.inara.SetEnabled(s.cfg.InaraEnabled)

	// Frontier OAuth + cAPI. Token file lives next to the regular config so
	// it inherits user-only directory permissions.
	tokenPath := resolveFrontierTokenPath()
	s.frontierStore = frontier.NewFileTokenStore(tokenPath)
	oauth := frontier.NewClient()
	clientID := s.cfg.FrontierClientID
	if clientID == "" {
		clientID = frontier.DefaultClientID
	}
	s.frontierCAPI = frontier.NewCAPI(oauth, clientID, s.frontierStore)
	s.frontierFlow = frontier.NewFlowManager(oauth, s.frontierStore)
	s.frontierFlow.ClientID = clientID
	s.frontierFlow.OnTokens = func(t *frontier.Tokens) {
		s.frontierCAPI.SetTokens(t)
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelOK,
			Message: "Signed in with Frontier (cAPI tokens cached)",
		})
		// Trigger an immediate FC sync — the user just signed in and is
		// presumably eager to see ravencolonial reflect their FC state.
		s.kickFrontierSync()
	}

	s.mux = destinations.NewMultiplex(s.rep, s.eddn, s.edsm, s.inara)
	s.mux.OnError = func(name string, err error) {
		// Don't surface per-event errors here — destinations emit their own
		// user-visible status messages. This callback exists for diagnostics
		// only.
		_ = err
	}
	return nil
}

// resolveFrontierTokenPath returns the file path for the Frontier OAuth
// token store. Sits in the same XDG/AppData directory as config.toml so
// the parent dir's perms cover both files.
func resolveFrontierTokenPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "edcolreport-frontier-tokens.json")
	}
	return filepath.Join(dir, "ed-colonization-reporter", "frontier_tokens.json")
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

	tl := &journal.Tailer{Dir: dir, StartAt: startAt}
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
