# ed-colonization-reporter

A small cross-platform desktop client that reports Elite Dangerous colonization
progress to [ravencolonial.com](https://ravencolonial.com) by tailing your game
journal in the background.

Built primarily for Linux (where [SrvSurvey](https://github.com/njthomson/SrvSurvey)
is not available) but runs on Windows too. Scope is intentionally narrow:
**colonization reporting only**. No exploration overlays, no SRV survey tooling.

## Status

Alpha. The core colonization-reporting loop, Fleet Carrier cargo sync,
in-flight backfill, and community uploads to **EDDN**, **EDSM**, and
**Inara** are working. Project creation, the system-claim flow, and
richer Inara mappings (ship loadout, cargo, materials, missions) are
not yet implemented.

## What it does

**Colonization (ravencolonial.com)**
- Watches your Elite Dangerous journal directory and detects new entries in
  real time.
- On `ColonisationConstructionDepot` events, reports per-commodity supply
  state for the construction site you are docked at.
- On `ColonisationContribution` events, posts your commander's delivery to
  the matching project so contributions are attributed to you.
- On `CarrierStats` / `CarrierLocation`, registers your Fleet Carrier with
  ravencolonial; on `Market` events at your own FC, posts the current
  cargo snapshot. Fleet Carrier sync requires an `rcc-key` from
  [ravencolonial.com/user](https://ravencolonial.com/user).

**Community uploads (EDMC-style)**
- **[EDDN](https://eddn.edcd.io/)** — anonymous: forwards `FSDJump`,
  `Location`, `Docked`, `CarrierJump`, and `Market.json` snapshots as
  schema-validated journal/1 and commodity/3 messages. No API key.
- **[EDSM](https://www.edsm.net/)** — relays journal events to the EDSM
  star-map and commander tracker, honouring the server-provided discard
  list and rate-limit headers. Requires an API key from
  [edsm.net/en/settings/api](https://www.edsm.net/en/settings/api).
- **[Inara](https://inara.cz/)** — pushes typed location/dock/carrier-jump
  events plus pilot ranks to your Inara profile, batched about once a minute.
  Requires an API key from [inara.cz/settings-api](https://inara.cz/settings-api/).
  Silently skips beta/legacy galaxies.

**Convenience**
- Optional **backfill** replays the current journal file from the start
  on launch so a mid-session restart re-reports anything the running
  game has already logged.
- Local browser UI surfaces active projects, a live activity log, and a
  settings page with toggles + API-key fields for each destination.

## Optional: feed your own tools

Everything above works on its own. This section is for people who run
something extra — you can ignore it entirely.

### edcolonize (self-hosted)

[edcolonize](https://github.com/pequalsnp/edcolonize) is a self-hosted tool
that ranks Elite Dangerous systems as colonization candidates, and also
exposes an **MCP server** so an AI assistant can query it directly. If you run
one, the reporter can push it a compact *commander-state snapshot* — where you
are, what you're flying, credits, cargo, engineering materials, active
missions, and the per-commodity state of construction sites you're
contributing to.

That turns "what should I haul next?" from a question you have to answer into
one the assistant can answer from fact: *you're two jumps out, and that site is
still short 340t of steel.*

Unlike every other destination this reports **inward** — to a server you run,
on your own machine or LAN. Nothing is sent to a third party.

Enable it in Settings, or by environment:

```
EDCOLONIZE_URL=http://<your-host>:3000/api/cmdr/snapshot
EDCOLONIZE_TOKEN=<same value as that instance's CMDR_INGEST_TOKEN>
```

Setting `EDCOLONIZE_URL` is enough to turn it on; environment values override
the config file.

Snapshots are debounced (at most one POST every few seconds, however busy the
journal gets) and **fail open** — if the box is rebooting or unreachable the
snapshot is dropped and every other destination carries on unaffected. With no
URL configured the integration opens no sockets and starts no goroutines.

## Install

### Flatpak (Linux)

Once published on Flathub:

```
flatpak install flathub ca.thegalloways.edcolreport
```

To build the Flatpak from source in the meantime, see
[`packaging/flatpak/README.md`](packaging/flatpak/README.md). Note: the journal
lives inside your Steam/Proton (or Wine) prefix — the sandbox grants read-only
access to the common Steam paths; for a custom prefix, add it with
`flatpak override --user --filesystem=PATH:ro ca.thegalloways.edcolreport`.

### Arch Linux (AUR)

Once published:

```
yay -S edcolreport     # or your preferred AUR helper
```

The [`PKGBUILD`](packaging/aur/PKGBUILD) builds from the release tarball.

### From source

Requires Go 1.26+. The UI is built with [Fyne](https://fyne.io), which
needs a C compiler and OpenGL/X11 dev headers on Linux. On Arch/CachyOS:

```
sudo pacman -S --needed base-devel libxcursor libxrandr libxinerama libxi \
    mesa libxxf86vm pkg-config
```

Build the binary:

```
go build -o edcolreport ./cmd/edcolreport
./edcolreport
```

The binary opens a native window. A small HTTP listener also runs on a
random loopback port to receive the Frontier OAuth redirect; you can use
`--headless` to disable the GUI and run backend-only (useful for debugging).

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
go test ./...                  # all tests
go build ./...
golangci-lint run ./...        # lint (standard set + modernize)
golangci-lint run --fix ./...  # auto-fix what's mechanical
golangci-lint fmt ./...        # apply gofmt
```

The CI workflow runs the same on every push, and fails on any lint finding or
gofmt diff. The tree is clean with **zero exclusions and zero `//nolint`
directives** — please keep it that way rather than growing an ignore list. If a
rule is genuinely wrong here, disable it in `.golangci.yml` with a comment
saying why.

### Layout

- `cmd/edcolreport` — main entry point (wires everything together).
- `internal/journal` — journal directory detection, JSONL tailing, event types, Market.json reader.
- `internal/ravencolonial` — HTTP client for the ravencolonial.com API.
- `internal/state` — in-memory session state (commander, system, dock, owned carriers).
- `internal/reporter` — orchestrates journal events → API calls.
- `internal/destinations` — one subpackage per place events are sent; `edcolonize` is the
  inward push target and holds a wire contract mirrored from the edcolonize repo.
- `internal/config` — load/save user settings.
- `internal/web` — local HTTP server with an embedded browser UI.

> **Destination order matters.** `reporter.Reporter` is what populates
> `internal/state.Session`, and the multiplex dispatches in registration
> order — so any destination that reads the session must be registered after
> it. `edcolonize` is registered last for this reason.

### Design choices

- **Native Fyne window.** Pure-Go GUI on Linux isn't possible without
  going through a browser engine, so we accept CGO for the UI layer.
  Backend code (journal tail, ravencolonial/EDDN/EDSM/Inara, Frontier
  cAPI) stays pure-Go.
- **Backend HTTP server still exists.** A tiny loopback listener handles
  the Frontier OAuth redirect (Frontier requires HTTPS, so we route the
  callback through a static GitHub Pages page that forwards to localhost).
- **Cross-compile** to Windows uses [fyne-cross](https://github.com/fyne-io/fyne-cross)
  (Docker-based). macOS is not built — Elite Dangerous has no native macOS
  client, so there's nothing to report from.

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
