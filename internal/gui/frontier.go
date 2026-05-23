package gui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/pequalsnp/ed-colonization-reporter/internal/web"
)

// frontierPanel handles the Sign in / Sign out controls and status line.
type frontierPanel struct {
	srv *web.Server
	win fyne.Window

	status  *canvas.Text
	signin  *widget.Button
	signout *widget.Button
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

	// Refresh FC / Heal RC buttons removed when we dropped cAPI as the
	// cargo source — RC is now authoritative (SrvSurvey model). Sign-in
	// stays for now in case future non-cargo features want cAPI access.
	return p
}

func (p *frontierPanel) SetWindow(w fyne.Window) { p.win = w }

func (p *frontierPanel) content() fyne.CanvasObject {
	return container.NewVBox(
		container.NewPadded(p.status),
		container.NewHBox(p.signin, p.signout),
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
		} else {
			p.status.Text = "Not currently used — cAPI is no longer the cargo source. Sign-in kept for future features."
			p.status.Color = edFgMuted
			p.signin.Show()
			p.signout.Hide()
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
