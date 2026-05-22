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

// Contribution is the body of POST /api/project/{buildId}/contribute/{cmdr}.
// The map keys are commodity symbol names (e.g. "titanium"); values are the
// integer amount delivered by the commander on this contribution.
type Contribution map[string]int

// FleetCarrier is the body of PUT /api/fc/{marketId} — the metadata about a
// commander's Fleet Carrier. The cargo is sent separately via Cargo.
type FleetCarrier struct {
	MarketID      int64  `json:"marketId"`
	Name          string `json:"name"`
	Callsign      string `json:"callsign"`
	StarSystem    string `json:"starSystem,omitempty"`
	SystemAddress int64  `json:"systemAddress,omitempty"`
}

// Cargo is the body of POST /api/fc/{marketId}/cargo — a {commodity: stock}
// snapshot of the Fleet Carrier's current cargo. Sending overwrites the
// server-side value entirely (PATCH does deltas; we don't use it yet).
type Cargo map[string]int
