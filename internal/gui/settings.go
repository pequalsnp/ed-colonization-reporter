package gui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/config"
	"github.com/pequalsnp/ed-colonization-reporter/internal/ravencolonial"
	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// settingsPanel groups the configuration into titled cards so the form
// doesn't read as one giant flat list.
type settingsPanel struct {
	srv *web.Server

	journalDir, apiBase, apiKey, cmdrOverride               *widget.Entry
	edsmKey, inaraKey                                       *widget.Entry
	replaySession, eddnEnabled, edsmEnabled, inaraEnabled   *widget.Check
	frontierCAPIEnabled                                     *widget.Check
	save                                                    *widget.Button
	notice                                                  *canvas.Text
}

func newSettingsPanel(srv *web.Server) *settingsPanel {
	p := &settingsPanel{srv: srv}
	cfg := srv.Config()

	p.journalDir = entry(cfg.JournalDir, "auto-detected if blank")
	p.apiBase = entry(cfg.APIBaseURL, ravencolonial.DefaultBaseURL)
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
	p.frontierCAPIEnabled = widget.NewCheck("Use cAPI for FC inventory ground-truth", nil)
	p.frontierCAPIEnabled.SetChecked(cfg.FrontierCAPIEnabled)

	p.save = widget.NewButtonWithIcon("Save settings", theme.ConfirmIcon(), p.doSave)
	p.save.Importance = widget.HighImportance

	p.notice = canvas.NewText("", edFgMuted)
	p.notice.TextSize = 12

	return p
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
		formItem("Commander override", p.cmdrOverride),
		checkboxRow(p.replaySession),
	)

	rcCard := section("ravencolonial.com",
		"Colonization project tracking. Anonymous; the rcc-key is only needed for Fleet Carrier writes.",
		formItem("API base URL", p.apiBase),
		formItem("rcc-key", p.apiKey),
	)

	eddnRow := checkboxRow(p.eddnEnabled)
	edsmRow := container.NewVBox(checkboxRow(p.edsmEnabled), formItem("API key", p.edsmKey))
	inaraRow := container.NewVBox(checkboxRow(p.inaraEnabled), formItem("API key", p.inaraKey))

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
