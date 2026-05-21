// Package inara uploads typed events to the Inara API
// (https://inara.cz/inapi/v1/), which mirrors the player's Elite Dangerous
// state on their Inara profile.
//
// Independent reimplementation; EDMC's plugins/inara.py (GPLv2) was consulted
// for protocol shape but no code was copied.
package inara

// Endpoint is the live Inara API URL.
const Endpoint = "https://inara.cz/inapi/v1/"

// Header is the credentials/identity block sent on every Inara request.
// Note the lowercase `k` in APIkey — Inara rejects other casings.
type Header struct {
	AppName             string `json:"appName"`
	AppVersion          string `json:"appVersion"`
	IsDeveloped         bool   `json:"isDeveloped,omitempty"`
	APIKey              string `json:"APIkey"`
	CommanderName       string `json:"commanderName"`
	CommanderFrontierID string `json:"commanderFrontierID,omitempty"`
}

// Event is one entry in the batched events array.
type Event struct {
	Name      string `json:"eventName"`
	Timestamp string `json:"eventTimestamp"`
	Data      any    `json:"eventData"`
}

// Request is the top-level JSON body Inara accepts.
type Request struct {
	Header Header  `json:"header"`
	Events []Event `json:"events"`
}

// Reply is what Inara returns on success.
type Reply struct {
	Header struct {
		EventStatus     int    `json:"eventStatus"`
		EventStatusText string `json:"eventStatusText"`
	} `json:"header"`
	Events []EventReply `json:"events"`
}

// EventReply is the per-event status entry returned in Reply.Events. Indices
// match the request's Events array.
type EventReply struct {
	EventStatus     int    `json:"eventStatus"`
	EventStatusText string `json:"eventStatusText"`
	EventData       any    `json:"eventData"`
}

// Inara eventName constants for the events this uploader emits.
const (
	EventSetCommanderTravelLocation  = "setCommanderTravelLocation"
	EventAddCommanderTravelFSDJump   = "addCommanderTravelFSDJump"
	EventAddCommanderTravelDock      = "addCommanderTravelDock"
	EventAddCommanderTravelCarrier   = "addCommanderTravelCarrierJump"
	EventSetCommanderRankPilot       = "setCommanderRankPilot"
)
