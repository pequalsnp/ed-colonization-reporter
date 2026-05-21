# ed-colonization-reporter

A small cross-platform desktop client that reports Elite Dangerous colonization
progress to [ravencolonial.com](https://ravencolonial.com) by tailing your game
journal in the background.

Built primarily for Linux (where [SrvSurvey](https://github.com/njthomson/SrvSurvey)
is not available) but runs on Windows too. Scope is intentionally narrow:
**colonization reporting only**. No exploration overlays, no SRV survey tooling.

## Status

Alpha. The core colonization-reporting loop, Fleet Carrier cargo sync, and
in-flight backfill are working. Project creation and the system-claim flow
are not yet implemented.

## What it does

- Watches your Elite Dangerous journal directory and detects new entries in
  real time.
- On `ColonisationConstructionDepot` events, reports per-commodity supply
  state for the construction site you are docked at.
- On `ColonisationContribution` events, posts your commander's delivery to
  the matching project so contributions are attributed to you.
- On `CarrierStats` / `CarrierLocation`, registers your Fleet Carrier with
  ravencolonial; on `Market` events at your own FC, posts the current
  cargo snapshot. Fleet Carrier sync requires an `rcc-key` from
  [ravencolonial.com/user](https://ravencolonial.com/user); without one,
  carrier sync is silently skipped.
- Optional **backfill** mode replays the current journal file from the
  start on launch, so a mid-session restart re-reports any depots and
  contributions the running game has already recorded.
- Surfaces the projects you are linked to, their per-commodity progress, and
  any reporting errors in a small GUI.

## Install

### From source

Requires Go 1.24+. No CGO and no system dependencies — a single static
binary on every platform.

```
CGO_ENABLED=0 go build -o edcolreport ./cmd/edcolreport
./edcolreport
```

The binary starts a local HTTP server on a random loopback port and opens
your default browser to it. On Linux you can pass `--no-browser` and follow
the printed URL instead.

### Pre-built releases

Pre-built Linux and Windows binaries are attached to
[GitHub Releases](https://github.com/pequalsnp/ed-colonization-reporter/releases).

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
- `internal/journal` — journal directory detection, JSONL tailing, event types, Market.json reader.
- `internal/ravencolonial` — HTTP client for the ravencolonial.com API.
- `internal/state` — in-memory session state (commander, system, dock, owned carriers).
- `internal/reporter` — orchestrates journal events → API calls.
- `internal/config` — load/save user settings.
- `internal/web` — local HTTP server with an embedded browser UI.

### Design choices

- **Browser UI, not a native desktop app.** The binary spins up a local
  HTTP server on a random loopback port and opens your default browser
  to it. This avoids any CGO/GUI-framework dependency, so the binary is a
  single ~10 MB static executable on every platform with no system libs
  required. The downside is the UI is a browser tab, not a window.
- **Pure Go.** `CGO_ENABLED=0` everywhere. Cross-compile to Windows
  amd64 from Linux is `GOOS=windows go build`, no toolchain dance.

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
