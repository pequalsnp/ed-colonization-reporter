// Package ravencolonial is the HTTP client for the ravencolonial.com colonization API.
package ravencolonial

// Project mirrors the per-build project payload returned by ravencolonial.
// We only model the fields the reporter and the UI actually use.
type Project struct {
	BuildID       string                    `json:"buildId"`
	BuildName     string                    `json:"buildName,omitempty"`
	BuildType     string                    `json:"buildType,omitempty"`
	SystemAddress int64                     `json:"systemAddress,omitempty"`
	SystemName    string                    `json:"systemName,omitempty"`
	MarketID      int64                     `json:"marketId,omitempty"`
	Complete      bool                      `json:"complete,omitempty"`
	MaxNeed       int                       `json:"maxNeed,omitempty"`
	Commodities   map[string]int            `json:"commodities,omitempty"`
	Commanders    map[string]CommanderEntry `json:"commanders,omitempty"`
}

// CommanderEntry tracks a commander's relationship to a project.
type CommanderEntry struct {
	Assigned map[string]int `json:"assigned,omitempty"`
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
