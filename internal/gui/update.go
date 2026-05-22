package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// updateCheckEndpoint is the GitHub Releases "latest" API for our repo.
// The "/releases/latest" endpoint returns the latest non-prerelease;
// "/releases" lists everything. We use a small twist: query /releases
// and filter to the first entry (which is the most recent regardless
// of prerelease flag) since we're shipping -alpha tags right now.
const updateCheckEndpoint = "https://api.github.com/repos/pequalsnp/ed-colonization-reporter/releases"

// updateInfo summarises what a check found. NewerThanCurrent is true
// when LatestTag sorts after Current.
type updateInfo struct {
	Current          string
	LatestTag        string
	LatestURL        string
	LatestName       string
	NewerThanCurrent bool
}

// checkForUpdate hits the GitHub releases API. Best-effort: any error
// returns a non-nil error and the caller logs/ignores it.
func checkForUpdate(ctx context.Context, currentVersion string) (*updateInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, updateCheckEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "edcolreport-update-check")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("update check: HTTP %d: %s", resp.StatusCode, body)
	}
	var releases []struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Name       string `json:"name"`
		Draft      bool   `json:"draft"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for _, r := range releases {
		if r.Draft {
			continue
		}
		info := &updateInfo{
			Current:          currentVersion,
			LatestTag:        r.TagName,
			LatestURL:        r.HTMLURL,
			LatestName:       r.Name,
			NewerThanCurrent: versionLess(currentVersion, r.TagName),
		}
		return info, nil
	}
	return nil, errors.New("update check: no non-draft releases returned")
}

// versionLess reports whether a is older than b for our tag scheme
// (vN.M.P[-suffix]). String compare works as long as components are
// zero-padded to a single digit, which is what we ship; if you bump
// past v9 this needs replacing with a real semver parser.
func versionLess(a, b string) bool {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")
	// "dev" sentinel: anything other than dev wins.
	if a == "dev" {
		return b != "dev"
	}
	if b == "dev" {
		return false
	}
	return a < b
}
