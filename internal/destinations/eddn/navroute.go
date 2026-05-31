package eddn

import (
	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

// buildNavRouteMessage transforms NavRoute.json (already loaded) into the
// navroute/1 message body. Returns nil if the route is empty.
//
// The schema is strict (additionalProperties:false) on both the message and
// each Route entry; required entry fields are StarSystem, SystemAddress,
// StarPos, StarClass.
func buildNavRouteMessage(nr *journal.NavRouteFile, sess *state.Session) map[string]any {
	if nr == nil || len(nr.Route) == 0 {
		return nil
	}
	route := make([]map[string]any, 0, len(nr.Route))
	for _, e := range nr.Route {
		if e.StarSystem == "" || e.StarClass == "" {
			continue
		}
		route = append(route, map[string]any{
			"StarSystem":    e.StarSystem,
			"SystemAddress": e.SystemAddress,
			"StarPos":       []any{e.StarPos[0], e.StarPos[1], e.StarPos[2]},
			"StarClass":     e.StarClass,
		})
	}
	if len(route) == 0 {
		return nil
	}
	msg := map[string]any{
		"timestamp": nr.Timestamp,
		"event":     "NavRoute",
		"Route":     route,
	}
	addDLCFlags(msg, sess)
	return msg
}
