// Package application owns release selection and application update policy.
// Watch firmware and Connect IQ updates remain in containers/updates.
package application

import "time"

// Release is feed metadata, not a downloaded or verified installation package.
// Notes remain plain text; rendering belongs to the caller.
type Release struct {
	Tag         string    `json:"tag"`
	Name        string    `json:"name"`
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"publishedAt"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
}

// CheckRequest compares a full semantic current version against one channel.
// IncludePrerelease is false by default. A leading v is optional in versions.
type CheckRequest struct {
	CurrentVersion    string `json:"currentVersion"`
	IncludePrerelease bool   `json:"includePrerelease"`
}

// CheckResult contains the channel's history and its newest newer candidate.
// Available is nil for an empty history or when no release is newer; it is not
// permission to install. History is ordered by semantic version, descending.
type CheckResult struct {
	CurrentVersion string    `json:"currentVersion"`
	History        []Release `json:"history"`
	Available      *Release  `json:"available"`
}
