# ed-colonization-reporter

A small cross-platform desktop client that reports Elite Dangerous colonization
progress to [ravencolonial.com](https://ravencolonial.com) by tailing your game
journal in the background.

Built primarily for Linux (where [SrvSurvey](https://github.com/njthomson/SrvSurvey)
is not available) but runs on Windows too. Scope is intentionally narrow:
**colonization reporting only**. No exploration overlays, no SRV survey tooling.

## Status

Pre-release. v1 implements the core journal-tail → API-report loop and a
project-progress view. Fleet Carrier sync and project creation are planned for
a later release.

## What it does

- Watches your Elite Dangerous journal directory and detects new entries in
  real time.
- On `ColonisationConstructionDepot` events, reports per-commodity supply
  state for the construction site you are docked at.
- On `ColonisationContribution` events, posts your commander's delivery to
  the matching project so contributions are attributed to you.
- Surfaces the projects you are linked to, their per-commodity progress, and
  any reporting errors in a small GUI.

## Install

### From source

Requires Go 1.24+. Linux needs the usual OpenGL/X11 dev packages for Fyne; on
Arch/CachyOS:

```
sudo pacman -S --needed base-devel libxcursor libxrandr libxinerama libxi \
    mesa libxxf86vm
```

Build the binary:

```
go build -o edcolreport ./cmd/edcolreport
./edcolreport
```

### Pre-built releases

Pre-built Linux and Windows binaries are attached to GitHub Releases once v1
ships.

## Configuration

On first launch the app tries to auto-detect your journal directory:

- **Linux (Steam Proton)**: `~/.steam/steam/steamapps/compatdata/359320/pfx/drive_c/users/steamuser/Saved Games/Frontier Developments/Elite Dangerous`
- **Windows**: `%USERPROFILE%\Saved Games\Frontier Developments\Elite Dangerous`

If you run the game via Lutris/standalone Wine, point the app at your prefix
in Settings. Config is stored under your XDG config directory (Linux) or
`%APPDATA%` (Windows).

## Development

```
go test ./...           # all tests
go vet ./...
go build ./...
```

The CI workflow runs the same on every push.

### Layout

- `cmd/edcolreport` — main entry point (wires everything together).
- `internal/journal` — journal directory detection, JSONL tailing, event types.
- `internal/ravencolonial` — HTTP client for the ravencolonial.com API.
- `internal/state` — in-memory session state (commander, system, dock).
- `internal/reporter` — orchestrates journal events → API calls.
- `internal/config` — load/save user settings.
- `internal/ui` — Fyne GUI.

## Acknowledgements

- [njthomson/SrvSurvey](https://github.com/njthomson/SrvSurvey) — the Windows
  tool this is loosely modelled after; its `RavenColonial.cs` was the primary
  reference for the API surface.
- [EDMC-Ravencolonial](https://github.com/EDToolbox/EDMC-Ravencolonial) — a
  minimal Python reference for the same event-to-API mapping.
- ravencolonial.com — the service this client reports to.

This project is not affiliated with Frontier Developments or ravencolonial.com.

## License

MIT. See [LICENSE](LICENSE).
