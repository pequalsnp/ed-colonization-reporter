package gui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// frontierPanel handles the Sign in / Sign out controls and status line.
type frontierPanel struct {
	srv *web.Server
	win fyne.Window

	status    *canvas.Text
	signin    *widget.Button
	signout   *widget.Button
	refreshFC *widget.Button
	healRC    *widget.Button
}

func newFrontierPanel(srv *web.Server) *frontierPanel {
	p := &frontierPanel{srv: srv}
	p.status = canvas.NewText("…", edFgMuted)
	p.status.TextSize = 12

	p.signin = widget.NewButtonWithIcon("Sign in with Frontier", theme.LoginIcon(), func() { go p.doSignin() })
	p.signin.Importance = widget.HighImportance

	p.signout = widget.NewButtonWithIcon("Sign out", theme.LogoutIcon(), func() { go p.doSignout() })
	p.signout.Importance = widget.LowImportance
	p.signout.Hide()

	p.refreshFC = widget.NewButtonWithIcon("Refresh FC now", theme.ViewRefreshIcon(), func() {
		p.srv.ForceFCSync()
	})
	p.refreshFC.Importance = widget.MediumImportance
	p.refreshFC.Hide()

	p.healRC = widget.NewButtonWithIcon("Heal RC from cAPI", theme.WarningIcon(), func() { go p.doHealRC() })
	p.healRC.Importance = widget.WarningImportance
	p.healRC.Hide()

	return p
}

func (p *frontierPanel) SetWindow(w fyne.Window) { p.win = w }

func (p *frontierPanel) content() fyne.CanvasObject {
	return container.NewVBox(
		container.NewPadded(p.status),
		container.NewHBox(p.signin, p.signout, p.refreshFC, p.healRC),
	)
}

// doHealRC asks for confirmation, then fetches the cAPI /fleetcarrier
// snapshot and POSTs it as an OverwriteCarrierCargo to ravencolonial,
// replacing whatever cargo state RC currently holds for the FC.
//
// Use case: RC's per-FC cargo has drifted from reality (e.g. previous
// versions of this app sent `cargo: {}` PUTs that wiped state). The
// cAPI snapshot is treated as the source of truth for that moment.
func (p *frontierPanel) doHealRC() {
	confirm := func(ok bool) {
		if !ok {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cargo, units, err := p.srv.HealRCFromCAPI(ctx)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(fmt.Errorf("heal failed: %w", err), p.win)
					return
				}
				dialog.ShowInformation(
					"RC healed",
					fmt.Sprintf("Overwrote ravencolonial with cAPI's snapshot: %d commodities, %d units.", len(cargo), units),
					p.win,
				)
			})
		}()
	}
	if p.win == nil {
		// No window means no dialog; just run without confirm.
		confirm(true)
		return
	}
	dialog.ShowConfirm(
		"Heal RC from cAPI?",
		"This will POST cAPI's current Fleet Carrier snapshot to ravencolonial, REPLACING whatever cargo state RC holds for the FC.\n\nUse this only when RC has drifted from reality. The cAPI data is itself slightly stale (~minutes), so any transfers made in the last few minutes may need a manual catch-up.",
		confirm,
		p.win,
	)
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
			p.status.Text = msg
			p.status.Color = edStatusOK
			p.signin.Hide()
			p.signout.Show()
			p.refreshFC.Show()
			p.healRC.Show()
		} else {
			p.status.Text = "Not signed in. Click below — a browser tab will open for the Frontier consent screen."
			p.status.Color = edFgMuted
			p.signin.Show()
			p.signout.Hide()
			p.refreshFC.Hide()
			p.healRC.Hide()
		}
		p.status.Refresh()
	})
}

func (p *frontierPanel) doSignin() {
	url, err := p.srv.FrontierStartSignin()
	if err != nil {
		fyne.Do(func() {
			p.status.Text = "Sign-in failed: " + err.Error()
			p.status.Color = edStatusError
			p.status.Refresh()
		})
		return
	}
	if err := web.OpenBrowser(url); err != nil {
		fyne.Do(func() {
			p.status.Text = "Could not open browser; paste into your browser: " + url
			p.status.Color = edStatusWarn
			p.status.Refresh()
		})
		return
	}
	fyne.Do(func() {
		p.status.Text = "Opened Frontier auth in your browser — finish there, then return here."
		p.status.Color = edStatusInfo
		p.status.Refresh()
	})
}

func (p *frontierPanel) doSignout() {
	if err := p.srv.FrontierSignout(); err != nil {
		fyne.Do(func() {
			p.status.Text = "Sign-out failed: " + err.Error()
			p.status.Color = edStatusError
			p.status.Refresh()
		})
		return
	}
	p.refresh()
}
