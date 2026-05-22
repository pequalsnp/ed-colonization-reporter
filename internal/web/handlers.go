package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", s.staticHandler())
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/frontier/signin", s.handleFrontierSignin)
	mux.HandleFunc("/api/frontier/signout", s.handleFrontierSignout)
	mux.HandleFunc("/api/frontier/status", s.handleFrontierStatus)
	// Note: /frontier/callback (no /api/ prefix) is the browser-redirect
	// landing page. It returns HTML, not JSON.
	mux.HandleFunc("/frontier/callback", s.handleFrontierCallback)
	return mux
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snap := s.session.Snapshot()
	resp := map[string]any{
		"commander":     snap.Commander,
		"fid":           snap.FID,
		"starSystem":    snap.StarSystem,
		"systemAddress": snap.SystemAddress,
		"docked":        snap.Docked,
		"stationName":   snap.StationName,
		"marketID":      snap.MarketID,
		"version":       s.Version,
	}
	writeJSON(w, resp)
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cmdr := s.session.Commander()
	if cmdr == "" && s.cfg.CommanderOverride != "" {
		cmdr = s.cfg.CommanderOverride
	}
	if cmdr == "" {
		writeJSON(w, map[string]any{"projects": []any{}, "commander": ""})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	projects, err := s.client.ActiveProjects(ctx, cmdr)
	if err != nil {
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: fmt.Sprintf("ActiveProjects failed: %v", err),
		})
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"projects": projects, "commander": cmdr})
}

// configDTO is the JSON shape exchanged with the browser. It uses snake_case
// to match the TOML field names so the form is self-documenting.
type configDTO struct {
	JournalDir          string `json:"journal_dir"`
	APIBaseURL          string `json:"api_base_url"`
	APIKey              string `json:"api_key"`
	CommanderOverride   string `json:"commander_override"`
	ReplaySession       bool   `json:"replay_session"`
	EDDNEnabled         bool   `json:"eddn_enabled"`
	EDSMEnabled         bool   `json:"edsm_enabled"`
	EDSMAPIKey          string `json:"edsm_api_key"`
	InaraEnabled        bool   `json:"inara_enabled"`
	InaraAPIKey         string `json:"inara_api_key"`
	FrontierCAPIEnabled bool   `json:"frontier_capi_enabled"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		c := s.cfg
		s.mu.Unlock()
		writeJSON(w, configDTO{
			JournalDir:        c.JournalDir,
			APIBaseURL:        c.APIBaseURL,
			APIKey:            c.APIKey,
			CommanderOverride: c.CommanderOverride,
			ReplaySession:     c.ReplaySession,
			EDDNEnabled:         c.EDDNEnabled,
			EDSMEnabled:         c.EDSMEnabled,
			EDSMAPIKey:          c.EDSMAPIKey,
			InaraEnabled:        c.InaraEnabled,
			InaraAPIKey:         c.InaraAPIKey,
			FrontierCAPIEnabled: c.FrontierCAPIEnabled,
		})
	case http.MethodPost:
		var in configDTO
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		newCfg := config.Config{
			JournalDir:        in.JournalDir,
			APIBaseURL:        in.APIBaseURL,
			APIKey:            in.APIKey,
			CommanderOverride: in.CommanderOverride,
			ReplaySession:     in.ReplaySession,
			EDDNEnabled:         in.EDDNEnabled,
			EDSMEnabled:         in.EDSMEnabled,
			EDSMAPIKey:          in.EDSMAPIKey,
			InaraEnabled:        in.InaraEnabled,
			InaraAPIKey:         in.InaraAPIKey,
			FrontierCAPIEnabled: in.FrontierCAPIEnabled,
		}
		if newCfg.APIBaseURL == "" {
			newCfg.APIBaseURL = ravencolonial.DefaultBaseURL
		}
		if err := config.Save(newCfg); err != nil {
			http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.cfg = newCfg
		// Rebuild the client and reporter with the new settings. The tailer
		// goroutine keeps running; it just picks up the new reporter pointer
		// on the next event.
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
		// Hot-update the EDDN destination too — enable/disable flag and
		// journal dir can change without restart.
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
		// Rebuild the destination set so the new ravencolonial reporter is in it.
		if s.mux != nil {
			s.mux.Replace(s.rep, s.eddn, s.edsm, s.inara)
		}
		s.mu.Unlock()
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelOK,
			Message: "Settings saved.",
		})
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := s.hub.Subscribe()
	defer cancel()

	// Heartbeat so proxies don't reap idle connections (also keeps the
	// browser's onerror handler from firing during long quiet periods).
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case s, ok := <-ch:
			if !ok {
				return
			}
			payload, _ := json.Marshal(map[string]any{
				"time":    s.Time.Format(time.RFC3339),
				"level":   s.Level.String(),
				"message": s.Message,
			})
			if _, err := fmt.Fprintf(w, "event: status\ndata: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// handleFrontierSignin starts the OAuth flow and returns the URL the
// browser should open. We don't open the browser server-side because the
// user is already in a browser tab — they'd rather open a new tab.
func (s *Server) handleFrontierSignin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	addr, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		http.Error(w, "server not bound", http.StatusInternalServerError)
		return
	}
	url, err := s.frontierFlow.Start(addr.Port)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"auth_url": url})
}

// handleFrontierCallback is what the GitHub Pages redirector points the
// user's browser at, with ?code= and ?state= from Frontier. It exchanges
// the code for tokens and renders a "you can close this tab" page.
func (s *Server) handleFrontierCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "Frontier sign-in failed: " + errMsg,
		})
		renderCallbackPage(w, http.StatusBadRequest, "Frontier returned error: "+errMsg, false)
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		renderCallbackPage(w, http.StatusBadRequest, "Missing code or state from Frontier.", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if _, err := s.frontierFlow.Complete(ctx, code, state); err != nil {
		s.hub.Publish(reporter.Status{
			Time: time.Now(), Level: reporter.LevelError,
			Message: "Frontier code exchange failed: " + err.Error(),
		})
		renderCallbackPage(w, http.StatusBadGateway, "Token exchange failed: "+err.Error(), false)
		return
	}
	renderCallbackPage(w, http.StatusOK, "Signed in. You can close this tab and return to the app.", true)
}

// handleFrontierStatus returns whether we currently have valid tokens.
func (s *Server) handleFrontierStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := map[string]any{
		"signed_in":   false,
		"client_id":   s.frontierFlow.ClientID,
		"capi_enabled": s.cfg.FrontierCAPIEnabled,
	}
	if tok, err := s.frontierStore.Load(); err == nil && tok != nil && tok.AccessToken != "" {
		resp["signed_in"] = true
		resp["expires_at"] = tok.ExpiresAt.Format(time.RFC3339)
		resp["expired"] = tok.Expired()
	}
	writeJSON(w, resp)
}

// handleFrontierSignout discards tokens.
func (s *Server) handleFrontierSignout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.frontierStore.Clear(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.frontierCAPI.SetTokens(nil)
	s.hub.Publish(reporter.Status{
		Time: time.Now(), Level: reporter.LevelInfo,
		Message: "Signed out of Frontier",
	})
	w.WriteHeader(http.StatusNoContent)
}

// renderCallbackPage writes a small HTML page for the OAuth callback.
// Kept self-contained (no embed) so it survives even if the app reloads
// its static assets.
func renderCallbackPage(w http.ResponseWriter, status int, msg string, ok bool) {
	color := "#ff6b6b"
	if ok {
		color = "#6bd587"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	body := `<!DOCTYPE html><html><head><meta charset="utf-8"><title>edcolreport — Frontier sign-in</title>` +
		`<style>body{font-family:system-ui,sans-serif;background:#16161a;color:#e0e0e0;padding:48px 24px;max-width:520px;margin:0 auto}` +
		`.box{padding:14px 18px;border-radius:6px;border:1px solid #2a2a30;background:#1a1a20;color:` + color + `;font-size:14px;line-height:1.5}</style>` +
		`</head><body><div class="box">` + htmlEscape(msg) + `</div></body></html>`
	_, _ = w.Write([]byte(body))
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
