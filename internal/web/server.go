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
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
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

	hub      *statusHub
	listener net.Listener
	srv      *http.Server

	// tailer lifecycle
	tailerCancel context.CancelFunc
}

// New creates a Server with the initial config.
func New(cfg config.Config) *Server {
	return &Server{cfg: cfg, hub: newStatusHub()}
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
	return nil
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
		if err := s.rep.HandleEvent(ctx, raw); err != nil {
			// Reporter already emits a status for failures we care about.
			_ = err
		}
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
