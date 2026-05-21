package reporter

import "strings"

// NormalizeCommodity converts a journal-style commodity symbol like
// "$titanium_name;" into the short form "titanium" that the ravencolonial
// API expects. Names already in short form are returned unchanged
// (case-folded). Empty input returns empty.
func NormalizeCommodity(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = strings.TrimPrefix(name, "$")
	name = strings.TrimSuffix(name, ";")
	name = strings.TrimSuffix(name, "_name")
	return strings.ToLower(name)
}
