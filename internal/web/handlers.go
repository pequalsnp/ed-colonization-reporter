package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", s.staticHandler())
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/projects", s.handleProjects)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/events", s.handleEvents)
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
	JournalDir        string `json:"journal_dir"`
	APIBaseURL        string `json:"api_base_url"`
	APIKey            string `json:"api_key"`
	CommanderOverride string `json:"commander_override"`
	ReplaySession     bool   `json:"replay_session"`
	EDDNEnabled       bool   `json:"eddn_enabled"`
	EDSMEnabled       bool   `json:"edsm_enabled"`
	EDSMAPIKey        string `json:"edsm_api_key"`
	InaraEnabled      bool   `json:"inara_enabled"`
	InaraAPIKey       string `json:"inara_api_key"`
	EdcolonizeEnabled bool   `json:"edcolonize_enabled"`
	EdcolonizeURL     string `json:"edcolonize_url"`
	EdcolonizeToken   string `json:"edcolonize_token"`
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
			EDDNEnabled:       c.EDDNEnabled,
			EDSMEnabled:       c.EDSMEnabled,
			EDSMAPIKey:        c.EDSMAPIKey,
			InaraEnabled:      c.InaraEnabled,
			InaraAPIKey:       c.InaraAPIKey,
			EdcolonizeEnabled: c.EdcolonizeEnabled,
			EdcolonizeURL:     c.EdcolonizeURL,
			EdcolonizeToken:   c.EdcolonizeToken,
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
			EDDNEnabled:       in.EDDNEnabled,
			EDSMEnabled:       in.EDSMEnabled,
			EDSMAPIKey:        in.EDSMAPIKey,
			InaraEnabled:      in.InaraEnabled,
			InaraAPIKey:       in.InaraAPIKey,
			EdcolonizeEnabled: in.EdcolonizeEnabled,
			EdcolonizeURL:     in.EdcolonizeURL,
			EdcolonizeToken:   in.EdcolonizeToken,
		}
		// Delegate to the single apply path shared with the Fyne settings
		// panel. These were near-identical copies, which is how the
		// edcolonize destination ended up hot-updated in one and silently
		// dropped from the multiplex in the other.
		if err := s.ApplyConfig(newCfg); err != nil {
			http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
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
