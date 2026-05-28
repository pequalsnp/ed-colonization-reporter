// Command inaracheck is a one-shot probe that confirms an Inara API
// whitelist by POSTing a single setCommanderTravelLocation event using
// the production inara package's header shape.
//
// Reads the user's config for inara_api_key + inara_app_name; falls
// back to "edcolreport" as the appName. Pulls commander name + FID
// from the most recent journal file (or $CMDR and $FID overrides).
//
// isDeveloped=true on the header so the request is flagged as a test
// and not counted toward the commander's live profile.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/destinations/inara"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	journalDir := flag.String("journal", "", "override journal directory (default: auto)")
	system := flag.String("system", "", "override star-system name (default: from latest journal)")
	flag.Parse()

	cfg, _, _, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg.InaraAPIKey == "" {
		return errors.New("config has no inara_api_key")
	}
	appName := cfg.InaraAppName
	if appName == "" {
		appName = "edcolreport"
	}

	cmdr, fid, starSystem, starPos, err := lastTravelContext(*journalDir)
	if err != nil {
		return fmt.Errorf("read journal: %w", err)
	}
	if v := os.Getenv("CMDR"); v != "" {
		cmdr = v
	}
	if v := os.Getenv("FID"); v != "" {
		fid = v
	}
	if *system != "" {
		starSystem = *system
	}
	if cmdr == "" {
		return errors.New("could not determine commander name (set $CMDR)")
	}
	if starSystem == "" {
		return errors.New("could not determine star system (set -system)")
	}

	req := inara.Request{
		Header: inara.Header{
			AppName:             appName,
			AppVersion:          "inaracheck",
			IsDeveloped:         true, // mark as test so it doesn't count toward profile
			APIKey:              cfg.InaraAPIKey,
			CommanderName:       cmdr,
			CommanderFrontierID: fid,
		},
		Events: []inara.Event{{
			Name:      inara.EventSetCommanderTravelLocation,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
			Data: map[string]any{
				"starsystemName":   starSystem,
				"starsystemCoords": starPos,
			},
		}},
	}

	// Print the request for visibility (redact the API key).
	redacted := req
	redacted.Header.APIKey = "<redacted>"
	pretty, _ := json.MarshalIndent(redacted, "", "  ")
	fmt.Println("POST", inara.Endpoint)
	fmt.Println(string(pretty))
	fmt.Println()

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, inara.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("POST: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	fmt.Println("HTTP", resp.Status)
	fmt.Println()

	var reply inara.Reply
	if err := json.Unmarshal(raw, &reply); err != nil {
		fmt.Println(string(raw))
		return fmt.Errorf("decode reply: %w", err)
	}
	pretty, _ = json.MarshalIndent(reply, "", "  ")
	fmt.Println(string(pretty))
	fmt.Println()

	headerOK := reply.Header.EventStatus/100 == 2
	if headerOK {
		fmt.Printf("HEADER OK   (%d) %s\n", reply.Header.EventStatus, reply.Header.EventStatusText)
	} else {
		fmt.Printf("HEADER FAIL (%d) %s\n", reply.Header.EventStatus, reply.Header.EventStatusText)
	}
	for i, ev := range reply.Events {
		tag := "OK  "
		if ev.EventStatus/100 != 2 {
			tag = "FAIL"
		}
		fmt.Printf("EVENT %d %s (%d) %s\n", i, tag, ev.EventStatus, ev.EventStatusText)
	}
	if !headerOK {
		os.Exit(2)
	}
	return nil
}

// lastTravelContext reads the most-recent journal file and returns the
// last-seen commander name, FID, star system, and star pos. Best-effort
// — empty values returned for fields not found.
func lastTravelContext(dirOverride string) (cmdr, fid, system string, pos []float64, err error) {
	dir := dirOverride
	if dir == "" {
		dir = autoJournalDir()
	}
	if dir == "" {
		return "", "", "", nil, errors.New("journal directory not found; pass -journal")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", "", "", nil, err
	}
	var logs []os.DirEntry
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "Journal.") && strings.HasSuffix(name, ".log") {
			logs = append(logs, e)
		}
	}
	if len(logs) == 0 {
		return "", "", "", nil, fmt.Errorf("no journal files in %s", dir)
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].Name() > logs[j].Name() })
	latest := filepath.Join(dir, logs[0].Name())
	f, err := os.Open(latest)
	if err != nil {
		return "", "", "", nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", "", "", nil, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var hdr struct {
			Event      string    `json:"event"`
			Name       string    `json:"Name"`
			Commander  string    `json:"Commander"`
			FID        string    `json:"FID"`
			StarSystem string    `json:"StarSystem"`
			StarPos    []float64 `json:"StarPos"`
		}
		if err := json.Unmarshal([]byte(line), &hdr); err != nil {
			continue
		}
		switch hdr.Event {
		case "Commander":
			cmdr = hdr.Name
			if hdr.FID != "" {
				fid = hdr.FID
			}
		case "LoadGame":
			if hdr.Commander != "" {
				cmdr = hdr.Commander
			}
			if hdr.FID != "" {
				fid = hdr.FID
			}
		case "Location", "FSDJump", "CarrierJump":
			if hdr.StarSystem != "" {
				system = hdr.StarSystem
			}
			if len(hdr.StarPos) == 3 {
				pos = hdr.StarPos
			}
		}
	}
	return cmdr, fid, system, pos, nil
}

// autoJournalDir tries the common Linux+Proton path. We don't need to
// be smart — the config already supports overrides and this is a probe.
func autoJournalDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(home, ".steam/steam/steamapps/compatdata/359320/pfx/drive_c/users/steamuser/Saved Games/Frontier Developments/Elite Dangerous"),
		filepath.Join(home, "Saved Games/Frontier Developments/Elite Dangerous"),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return ""
}
