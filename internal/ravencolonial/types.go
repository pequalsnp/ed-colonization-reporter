// Package ravencolonial is the HTTP client for the ravencolonial.com colonization API.
package ravencolonial

import "encoding/json"

// Project mirrors the per-build project payload returned by ravencolonial.
// We only model the fields the reporter and the UI actually use.
type Project struct {
	BuildID       string         `json:"buildId"`
	BuildName     string         `json:"buildName,omitempty"`
	BuildType     string         `json:"buildType,omitempty"`
	SystemAddress int64          `json:"systemAddress,omitempty"`
	SystemName    string         `json:"systemName,omitempty"`
	MarketID      int64          `json:"marketId,omitempty"`
	Complete      bool           `json:"complete,omitempty"`
	MaxNeed       int            `json:"maxNeed,omitempty"`
	Commodities   map[string]int `json:"commodities,omitempty"`
	// Commanders is opaque on purpose. Ravencolonial has historically
	// returned this as `{cmdr: {assigned: {...}}}` in some endpoints and
	// `{cmdr: [...]}` in others, and we don't actually read it anywhere —
	// so keep it as raw JSON to avoid coupling to a shape that drifts.
	Commanders json.RawMessage `json:"commanders,omitempty"`
}

// ProjectUpdate is the body of POST /api/project/{buildId} — a snapshot of
// outstanding commodity needs for a construction depot.
type ProjectUpdate struct {
	BuildID     string         `json:"buildId"`
	Commodities map[string]int `json:"commodities"`
	MaxNeed     int            `json:"maxNeed"`
}

// ProjectCreate is the body of PUT /api/project/ — used to register a new
// build with ravencolonial the first time we see its construction depot.
//
// Field names and required-vs-optional split mirror SrvSurvey's
// ProjectCreate model (RavenColonial.cs:700-710 + ProjectCore:673-698).
type ProjectCreate struct {
	// Required fields — server rejects when missing.
	BuildType     string         `json:"buildType"`
	BuildName     string         `json:"buildName"`
	MarketID      int64          `json:"marketId"`
	SystemAddress int64          `json:"systemAddress"`
	SystemName    string         `json:"systemName"`
	StarPos       [3]float64     `json:"starPos"`
	MaxNeed       int            `json:"maxNeed"`
	IsPrimaryPort bool           `json:"isPrimaryPort"`
	Commodities   map[string]int `json:"commodities"`

	// Optional fields — Newtonsoft.Json drops nulls on the C# side; we
	// achieve the same via omitempty.
	BodyNum                       *int                `json:"bodyNum,omitempty"`
	BodyName                      string              `json:"bodyName,omitempty"`
	FactionName                   string              `json:"factionName,omitempty"`
	ArchitectName                 string              `json:"architectName,omitempty"`
	DiscordLink                   string              `json:"discordLink,omitempty"`
	Notes                         string              `json:"notes,omitempty"`
	SystemSiteID                  string              `json:"systemSiteId,omitempty"`
	Commanders                    map[string][]string `json:"commanders,omitempty"`
	ColonisationConstructionDepot any                 `json:"colonisationConstructionDepot,omitempty"`
}

// Contribution is the body of POST /api/project/{buildId}/contribute/{cmdr}.
// The map keys are commodity symbol names (e.g. "titanium"); values are the
// integer amount delivered by the commander on this contribution.
type Contribution map[string]int

// FleetCarrier is the body of PUT /api/fc/{marketId} — and the response
// shape of GET /api/fc/{marketId}. The shape mirrors SrvSurvey's
// FleetCarrier model with the addition of the `newFC` flag the live
// server requires.
//
// Field semantics flip from Frontier's cAPI: ravencolonial's `name`
// is the callsign (e.g. "QZN-W6N"), `displayName` is the vanity name
// (e.g. "DREAMSTRIDER").
//
// **Cargo must be a pointer.** A nil pointer serializes to JSON null,
// which the server interprets as "leave cargo untouched". A pointer
// to an empty map serializes to {} which the server treats as
// "clear cargo". This distinction is what was wiping our RC state on
// every CarrierStats publish — see the comment on PutFleetCarrier
// for the rules.
type FleetCarrier struct {
	MarketID    int64           `json:"marketId"`
	Name        string          `json:"name"`        // callsign — required
	DisplayName string          `json:"displayName"` // vanity name — required
	NewFC       bool            `json:"newFC"`       // true on first publish; false on subsequent metadata updates
	Cargo       *map[string]int `json:"cargo"`       // nil = leave untouched, empty {} = clear, populated = overwrite
}

// Cargo is the body of POST /api/fc/{marketId}/cargo — a {commodity: stock}
// snapshot of the Fleet Carrier's current cargo. Sending overwrites the
// server-side value entirely (PATCH does deltas; we don't use it yet).
type Cargo map[string]int
