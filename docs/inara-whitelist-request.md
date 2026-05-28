# Inara whitelist request — draft

**To:** support@inara.cz (or whichever contact address Inara prefers — double-check on https://inara.cz/contact/)
**From:** kyle@thegalloways.ca
**Subject:** App whitelist request — ed-colonization-reporter (appName: edcolreport)

---

Hi Inara team,

I'd like to request whitelisting for a third-party Elite Dangerous
client I've written, so its Inara API uploads can be accepted instead
of receiving "this application has no access allowed".

**App name in API header:** `edcolreport`
**Display name:** ED Colonization Reporter
**Source:** https://github.com/pequalsnp/ed-colonization-reporter
**License:** MIT (open source, free for anyone to fork or audit)
**Platform:** Linux (primary), Windows builds via fyne-cross
**Author / contact:** Kyle Galloway — kyle@thegalloways.ca

**What it does:**
A small standalone GUI app, written in Go, that tails the
Elite Dangerous journal and:

- Reports colonization progress (depot snapshots, contributions, Fleet
  Carrier inventory) to ravencolonial.com — essentially a Linux-first
  alternative to SrvSurvey's colonization features.
- Optionally relays journal events to EDDN, EDSM, and Inara, behaving
  like EDMC for that subset (commander travel log, market data, ship
  loadout, materials). Implementation is independent — we consulted
  EDMC's protocol shape for reference but did not copy any code (EDMC
  is GPL, our project is MIT).

The Inara integration follows the standard `header` + `events`
envelope shape, with the API key sent per Inara's docs. App version
is the build tag passed via Go ldflags.

**Privacy:** the user's Inara API key lives only in their local config
file (`~/.config/ed-colonization-reporter/config.toml` on Linux, AppData
equivalent on Windows). The app is run-locally; there's no central
server.

If you need a sample request payload or want me to demonstrate the
batching / flush behavior, I'm happy to share. Thanks for considering!

Cheers,
Kyle Galloway
