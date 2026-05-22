package gui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// settingsPanel groups the configuration into titled cards so the form
// doesn't read as one giant flat list.
type settingsPanel struct {
	srv *web.Server

	journalDir, apiBase, apiKey, cmdrOverride             *widget.Entry
	edsmKey, inaraKey                                     *widget.Entry
	replaySession, eddnEnabled, edsmEnabled, inaraEnabled *widget.Check
	frontierCAPIEnabled                                   *widget.Check
	save                                                  *widget.Button
	notice                                                *canvas.Text

	// journalStatus shows whether the configured (or auto-detected)
	// journal directory contains any Journal.*.log files.
	journalStatus *canvas.Text

	// rcTestStatus + rcTestBtn implement the "Test connection" probe
	// for the ravencolonial API.
	rcTestStatus *canvas.Text
	rcTestBtn    *widget.Button

	edsmTestStatus  *canvas.Text
	edsmTestBtn     *widget.Button
	inaraTestStatus *canvas.Text
	inaraTestBtn    *widget.Button
}

func newSettingsPanel(srv *web.Server) *settingsPanel {
	p := &settingsPanel{srv: srv}
	cfg := srv.Config()

	p.journalDir = entry(cfg.JournalDir, "auto-detected if blank")
	p.journalStatus = canvas.NewText("", edFgMuted)
	p.journalStatus.TextSize = 11
	p.journalDir.OnChanged = func(string) { p.updateJournalStatus() }

	p.apiBase = entry(cfg.APIBaseURL, ravencolonial.DefaultBaseURL)
	p.rcTestStatus = canvas.NewText("", edFgMuted)
	p.rcTestStatus.TextSize = 11
	p.rcTestBtn = widget.NewButtonWithIcon("Test connection", theme.ConfirmIcon(), func() { go p.testRavencolonial() })
	p.rcTestBtn.Importance = widget.LowImportance
	p.apiKey = passwordEntry(cfg.APIKey, "optional — from ravencolonial.com/user")
	p.cmdrOverride = entry(cfg.CommanderOverride, "leave blank to use the journal commander")

	p.replaySession = widget.NewCheck("Replay current journal session on startup", nil)
	p.replaySession.SetChecked(cfg.ReplaySession)

	p.eddnEnabled = widget.NewCheck("Enable", nil)
	p.eddnEnabled.SetChecked(cfg.EDDNEnabled)
	p.edsmEnabled = widget.NewCheck("Enable", nil)
	p.edsmEnabled.SetChecked(cfg.EDSMEnabled)
	p.edsmKey = passwordEntry(cfg.EDSMAPIKey, "from edsm.net/en/settings/api")
	p.inaraEnabled = widget.NewCheck("Enable", nil)
	p.inaraEnabled.SetChecked(cfg.InaraEnabled)
	p.inaraKey = passwordEntry(cfg.InaraAPIKey, "from inara.cz/settings-api/")

	p.edsmTestStatus = canvas.NewText("", edFgMuted)
	p.edsmTestStatus.TextSize = 11
	p.edsmTestBtn = widget.NewButtonWithIcon("Test", theme.ConfirmIcon(), func() { go p.testEDSM() })
	p.edsmTestBtn.Importance = widget.LowImportance

	p.inaraTestStatus = canvas.NewText("", edFgMuted)
	p.inaraTestStatus.TextSize = 11
	p.inaraTestBtn = widget.NewButtonWithIcon("Test", theme.ConfirmIcon(), func() { go p.testInara() })
	p.inaraTestBtn.Importance = widget.LowImportance
	p.frontierCAPIEnabled = widget.NewCheck("Use cAPI for FC inventory ground-truth", nil)
	p.frontierCAPIEnabled.SetChecked(cfg.FrontierCAPIEnabled)

	p.save = widget.NewButtonWithIcon("Save settings", theme.ConfirmIcon(), p.doSave)
	p.save.Importance = widget.HighImportance

	p.notice = canvas.NewText("", edFgMuted)
	p.notice.TextSize = 12

	// Seed the journal-status indicator with the initial state.
	p.updateJournalStatus()

	return p
}

// updateJournalStatus inspects the configured (or auto-detected) journal
// directory and rewrites the status indicator. Called on every keystroke
// in the journal_dir entry plus once at construction.
func (p *settingsPanel) updateJournalStatus() {
	dir := strings.TrimSpace(p.journalDir.Text)
	autoDetected := false
	if dir == "" {
		if d, err := journal.FindJournalDir(); err == nil {
			dir = d
			autoDetected = true
		}
	}
	var (
		txt   string
		color = edFgMuted
	)
	switch {
	case dir == "":
		txt = "✗ no directory configured or auto-detected"
		color = edStatusError
	default:
		info, err := os.Stat(dir)
		switch {
		case os.IsNotExist(err):
			txt = "✗ path does not exist"
			color = edStatusError
		case err != nil:
			txt = "✗ cannot read: " + err.Error()
			color = edStatusError
		case !info.IsDir():
			txt = "✗ path is not a directory"
			color = edStatusError
		default:
			n := countJournalFiles(dir)
			if n == 0 {
				txt = fmt.Sprintf("✗ directory exists but contains no Journal.*.log files (%s)", dir)
				color = edStatusWarn
			} else {
				prefix := "✓"
				if autoDetected {
					prefix = "✓ auto-detected"
				}
				txt = fmt.Sprintf("%s — %d journal file%s found", prefix, n, plural(n))
				color = edStatusOK
			}
		}
	}
	fyne.Do(func() {
		p.journalStatus.Text = txt
		p.journalStatus.Color = color
		p.journalStatus.Refresh()
	})
}

func countJournalFiles(dir string) int {
	files, err := filepath.Glob(filepath.Join(dir, "Journal.*.log"))
	if err != nil {
		return 0
	}
	return len(files)
}

// testEDSM probes the EDSM journal endpoint. With an API key we POST a
// minimal events array so EDSM validates auth and returns msgnum;
// without one we GET the public discard list as a reachability check.
func (p *settingsPanel) testEDSM() {
	cmdr := p.srv.Session().Commander()
	if cmdr == "" {
		cmdr = "test"
	}
	apiKey := strings.TrimSpace(p.edsmKey.Text)
	fyne.Do(func() {
		p.edsmTestStatus.Text = "Testing…"
		p.edsmTestStatus.Color = edFgMuted
		p.edsmTestStatus.Refresh()
		p.edsmTestBtn.Disable()
	})
	defer fyne.Do(func() { p.edsmTestBtn.Enable() })

	client := &http.Client{Timeout: 10 * time.Second}
	if apiKey == "" {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
			"https://www.edsm.net/api-journal-v1/discard", nil)
		resp, err := client.Do(req)
		p.reportTest(p.edsmTestStatus, resp, err, "EDSM reachable (no API key set; key not validated)")
		return
	}
	form := url.Values{
		"commanderName":       {cmdr},
		"apiKey":              {apiKey},
		"fromSoftware":        {"edcolreport"},
		"fromSoftwareVersion": {p.srv.GetVersion()},
		"message":             {"[]"},
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://www.edsm.net/api-journal-v1", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		fyne.Do(func() {
			p.edsmTestStatus.Text = "✗ " + err.Error()
			p.edsmTestStatus.Color = edStatusError
			p.edsmTestStatus.Refresh()
		})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var reply struct {
		MsgNum int    `json:"msgnum"`
		Msg    string `json:"msg"`
	}
	_ = json.Unmarshal(body, &reply)
	fyne.Do(func() {
		switch {
		case reply.MsgNum/100 == 1:
			p.edsmTestStatus.Text = fmt.Sprintf("✓ EDSM accepted the key (msg %d: %s)", reply.MsgNum, reply.Msg)
			p.edsmTestStatus.Color = edStatusOK
		case reply.MsgNum >= 200:
			p.edsmTestStatus.Text = fmt.Sprintf("✗ EDSM rejected the key (msg %d: %s)", reply.MsgNum, reply.Msg)
			p.edsmTestStatus.Color = edStatusError
		default:
			p.edsmTestStatus.Text = fmt.Sprintf("? HTTP %d, body: %s", resp.StatusCode, truncate(string(body), 120))
			p.edsmTestStatus.Color = edStatusWarn
		}
		p.edsmTestStatus.Refresh()
	})
}

// testInara probes the Inara API. With an API key set, posts a minimal
// request and reads the header.eventStatus to validate auth.
func (p *settingsPanel) testInara() {
	cmdr := p.srv.Session().Commander()
	if cmdr == "" {
		cmdr = "test"
	}
	apiKey := strings.TrimSpace(p.inaraKey.Text)
	fyne.Do(func() {
		p.inaraTestStatus.Text = "Testing…"
		p.inaraTestStatus.Color = edFgMuted
		p.inaraTestStatus.Refresh()
		p.inaraTestBtn.Disable()
	})
	defer fyne.Do(func() { p.inaraTestBtn.Enable() })

	if apiKey == "" {
		fyne.Do(func() {
			p.inaraTestStatus.Text = "✗ No API key set."
			p.inaraTestStatus.Color = edStatusWarn
			p.inaraTestStatus.Refresh()
		})
		return
	}

	payload := map[string]any{
		"header": map[string]any{
			"appName":       "edcolreport",
			"appVersion":    p.srv.GetVersion(),
			"APIkey":        apiKey,
			"commanderName": cmdr,
		},
		"events": []any{},
	}
	body, _ := json.Marshal(payload)
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://inara.cz/inapi/v1/", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fyne.Do(func() {
			p.inaraTestStatus.Text = "✗ " + err.Error()
			p.inaraTestStatus.Color = edStatusError
			p.inaraTestStatus.Refresh()
		})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var reply struct {
		Header struct {
			EventStatus     int    `json:"eventStatus"`
			EventStatusText string `json:"eventStatusText"`
		} `json:"header"`
	}
	_ = json.Unmarshal(respBody, &reply)
	fyne.Do(func() {
		switch {
		case reply.Header.EventStatus == 200:
			p.inaraTestStatus.Text = "✓ Inara accepted the key."
			p.inaraTestStatus.Color = edStatusOK
		case reply.Header.EventStatus != 0:
			p.inaraTestStatus.Text = fmt.Sprintf("✗ Inara rejected the key (%d: %s)",
				reply.Header.EventStatus, reply.Header.EventStatusText)
			p.inaraTestStatus.Color = edStatusError
		default:
			p.inaraTestStatus.Text = fmt.Sprintf("? HTTP %d, body: %s", resp.StatusCode, truncate(string(respBody), 120))
			p.inaraTestStatus.Color = edStatusWarn
		}
		p.inaraTestStatus.Refresh()
	})
}

// reportTest is a small helper for the simple HTTP-reachability probes.
func (p *settingsPanel) reportTest(label *canvas.Text, resp *http.Response, err error, okMsg string) {
	fyne.Do(func() {
		if err != nil {
			label.Text = "✗ " + err.Error()
			label.Color = edStatusError
		} else {
			defer resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				label.Text = "✓ " + okMsg
				label.Color = edStatusOK
			} else {
				label.Text = fmt.Sprintf("✗ HTTP %d", resp.StatusCode)
				label.Color = edStatusError
			}
		}
		label.Refresh()
	})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// testRavencolonial probes the configured API base URL with a known
// no-auth endpoint and updates the inline status indicator. The probe
// uses the commander "test" because ravencolonial returns 200 with []
// for any unknown commander — confirms reachability + URL correctness
// without needing the user's real commander to be known yet.
func (p *settingsPanel) testRavencolonial() {
	base := strings.TrimRight(strings.TrimSpace(p.apiBase.Text), "/")
	if base == "" {
		base = ravencolonial.DefaultBaseURL
	}
	url := base + "/api/cmdr/test/active"

	fyne.Do(func() {
		p.rcTestStatus.Text = "Testing…"
		p.rcTestStatus.Color = edFgMuted
		p.rcTestStatus.Refresh()
		p.rcTestBtn.Disable()
	})
	defer fyne.Do(func() { p.rcTestBtn.Enable() })

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	resp, err := client.Do(req)

	fyne.Do(func() {
		if err != nil {
			p.rcTestStatus.Text = "✗ " + err.Error()
			p.rcTestStatus.Color = edStatusError
		} else {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				p.rcTestStatus.Text = fmt.Sprintf("✓ %s reachable (HTTP 200)", base)
				p.rcTestStatus.Color = edStatusOK
			} else {
				p.rcTestStatus.Text = fmt.Sprintf("✗ HTTP %d from %s", resp.StatusCode, base)
				p.rcTestStatus.Color = edStatusError
			}
		}
		p.rcTestStatus.Refresh()
	})
}

func entry(initial, placeholder string) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(initial)
	e.SetPlaceHolder(placeholder)
	return e
}

func passwordEntry(initial, placeholder string) *widget.Entry {
	e := widget.NewPasswordEntry()
	e.SetText(initial)
	e.SetPlaceHolder(placeholder)
	return e
}

func (p *settingsPanel) content(frontier *frontierPanel) fyne.CanvasObject {
	localCard := section("Local",
		"Where your journal lives and how the app behaves at startup.",
		formItem("Journal directory", p.journalDir),
		container.NewPadded(p.journalStatus),
		formItem("Commander override", p.cmdrOverride),
		checkboxRow(p.replaySession),
	)

	rcCard := section("ravencolonial.com",
		"Colonization project tracking. Anonymous; the rcc-key is only needed for Fleet Carrier writes.",
		formItem("API base URL", p.apiBase),
		container.NewBorder(nil, nil, nil, p.rcTestBtn, p.rcTestStatus),
		formItem("rcc-key", p.apiKey),
	)

	eddnRow := checkboxRow(p.eddnEnabled)
	edsmTestRow := container.NewBorder(nil, nil, nil, p.edsmTestBtn, p.edsmTestStatus)
	edsmRow := container.NewVBox(checkboxRow(p.edsmEnabled), formItem("API key", p.edsmKey), edsmTestRow)
	inaraTestRow := container.NewBorder(nil, nil, nil, p.inaraTestBtn, p.inaraTestStatus)
	inaraRow := container.NewVBox(checkboxRow(p.inaraEnabled), formItem("API key", p.inaraKey), inaraTestRow)

	uploadsCard := section("Community uploads",
		"Send journal data to third-party trackers. EDDN is anonymous; EDSM and Inara need their own API keys.",
		subhead("EDDN"), eddnRow,
		subhead("EDSM"), edsmRow,
		subhead("Inara"), inaraRow,
	)

	frontierCard := section("Frontier cAPI",
		"Authoritative Fleet Carrier inventory via Frontier's Companion API. PKCE — no client secret leaves your machine.",
		checkboxRow(p.frontierCAPIEnabled),
		frontier.content(),
	)

	saveRow := container.NewBorder(nil, nil, nil, p.save, p.notice)

	body := container.NewVBox(
		localCard,
		rcCard,
		uploadsCard,
		frontierCard,
		container.NewPadded(saveRow),
	)
	return container.NewVScroll(container.NewPadded(body))
}

func (p *settingsPanel) doSave() {
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
	go func() {
		err := p.srv.ApplyConfig(newCfg)
		fyne.Do(func() {
			if err != nil {
				p.notice.Text = "Save failed: " + err.Error()
				p.notice.Color = edStatusError
			} else {
				p.notice.Text = "Saved. " + time.Now().Format("15:04:05")
				p.notice.Color = edStatusOK
			}
			p.notice.Refresh()
		})
	}()
}

// section builds a labelled card with a heading + body. Used for each
// configuration group on the Settings tab.
func section(title, subtitle string, body ...fyne.CanvasObject) fyne.CanvasObject {
	titleText := canvas.NewText(title, edFg)
	titleText.TextStyle = fyne.TextStyle{Bold: true}
	titleText.TextSize = 14

	subtitleText := canvas.NewText(subtitle, edFgMuted)
	subtitleText.TextSize = 12

	header := container.NewVBox(titleText, subtitleText)

	bg := canvas.NewRectangle(edBgRaised)
	bg.CornerRadius = 6
	bg.StrokeColor = edBorder
	bg.StrokeWidth = 1

	inner := container.NewVBox(append([]fyne.CanvasObject{header, widget.NewSeparator()}, body...)...)
	padded := container.NewPadded(inner)
	stack := container.NewStack(bg, padded)
	return container.NewPadded(stack)
}

func subhead(text string) fyne.CanvasObject {
	t := canvas.NewText(text, edFgMuted)
	t.TextStyle = fyne.TextStyle{Bold: true}
	t.TextSize = 11
	return container.NewPadded(t)
}

func formItem(label string, w fyne.CanvasObject) fyne.CanvasObject {
	l := canvas.NewText(label, edFgMuted)
	l.TextSize = 12
	return container.NewBorder(nil, nil,
		container.NewGridWrap(fyne.NewSize(160, 30), l),
		nil,
		w,
	)
}

func checkboxRow(c *widget.Check) fyne.CanvasObject {
	return container.NewPadded(c)
}

// silence unused-context-import warning while context is plausibly used by
// future per-section async actions; remove once a section needs it.
var _ = context.Background
