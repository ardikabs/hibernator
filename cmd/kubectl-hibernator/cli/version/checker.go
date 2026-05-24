/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package version

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ardikabs/hibernator/internal/version"
	"github.com/samber/lo"
)

const (
	githubAPIURL     = "https://api.github.com/repos/ardikabs/hibernator/releases"
	httpTimeout      = 3 * time.Second
	installScriptURL = "https://hibernator.ardikabs.com/install-cli.sh"
)

// Release represents a GitHub release
type Release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Checker handles version checking
type Checker struct {
	client  *http.Client
	current string
}

// NewChecker creates a new version checker
func NewChecker() *Checker {
	return &Checker{
		client: &http.Client{
			Timeout: httpTimeout,
		},
		current: version.GetVersion(),
	}
}

// CheckForUpdate checks if a newer version is available
// Returns (newerVersion, shouldUpdate, error)
// Errors are silently ignored (returns "", false, nil) to handle offline cases
func (c *Checker) CheckForUpdate(ctx context.Context) (string, bool) {
	// Get current version
	if c.current == "" || strings.HasPrefix(c.current, "dev") {
		// Development build or unknown version, skip check
		return "", false
	}

	// Fetch releases from GitHub
	releases, err := c.fetchReleases(ctx)
	if err != nil {
		// Silently ignore errors (no internet, rate limit, etc.)
		return "", false
	}

	// Find the best version (preferring RC versions)
	latest := c.findBestVersion(releases)
	if latest == "" {
		return "", false
	}

	// Compare versions
	if c.isNewer(latest, c.current) {
		return latest, true
	}

	return "", false
}

// fetchReleases fetches releases from GitHub API
func (c *Checker) fetchReleases(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", githubAPIURL, nil)
	if err != nil {
		return nil, err
	}

	// Add headers to avoid rate limiting
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	// nolint:errcheck
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned status %d", resp.StatusCode)
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	return releases, nil
}

// findBestVersion finds the best version to recommend
// Prefers RC versions over stable versions
func (c *Checker) findBestVersion(releases []Release) string {
	var latestStable, latestRC string
	var stableVersion, rcVersion int

	for _, release := range releases {
		if release.Draft || release.Prerelease {
			continue
		}

		tag := release.TagName
		if tag == "" {
			continue
		}

		// Check if it's an RC version
		if isRCVersion(tag) {
			v := parseVersion(tag)
			if v > rcVersion {
				rcVersion = v
				latestRC = tag
			}
		} else {
			v := parseVersion(tag)
			if v > stableVersion {
				stableVersion = v
				latestStable = tag
			}
		}
	}

	// Prefer RC if available, otherwise use stable
	if latestRC != "" {
		return latestRC
	}
	return latestStable
}

// isRCVersion checks if a version is a release candidate
// Format: vM.m.p-rc.n
func isRCVersion(version string) bool {
	matched, _ := regexp.MatchString(`-rc\.\d+$`, version)
	return matched
}

// parseVersion converts version string to comparable integer
// v1.2.3 -> 10203, v1.2.3-rc.1 -> 10203
func parseVersion(v string) int {
	// Remove 'v' prefix
	v = strings.TrimPrefix(v, "v")

	// Cut off anything after '-' (tags like -rc.1, -beta, etc.)
	if idx := strings.Index(v, "-"); idx != -1 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return 0
	}

	// strconv.Atoi is the specialized tool for string-to-int conversion
	major := lo.Must(strconv.Atoi(parts[0]))
	minor := lo.Must(strconv.Atoi(parts[1]))
	patch := lo.Must(strconv.Atoi(parts[2]))

	return major*10000 + minor*100 + patch
}

// isNewer checks if newVersion is newer than currentVersion
func (c *Checker) isNewer(newVersion, currentVersion string) bool {
	return parseVersion(newVersion) > parseVersion(currentVersion)
}

// FormatUpdateMessage formats the update message
func FormatUpdateMessage(newVersion string) string {
	return fmt.Sprintf(
		"📦 A new version is available: %s\n"+
			"   Current version: %s\n"+
			"   Install with:\n"+
			"   curl -sSL %s | bash -s -- --version %s",
		newVersion,
		version.GetVersion(),
		installScriptURL,
		newVersion,
	)
}
