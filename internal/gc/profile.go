package gc

import (
	"context"
	"strings"
)

// CloudProfile is the signed-in user's public profile. Garmin's displayName
// is an opaque id for most accounts; Name holds the best human label.
type CloudProfile struct {
	DisplayName string `json:"displayName"`
	FullName    string `json:"fullName"`
	UserName    string `json:"userName"`
	Name        string `json:"name"`
}

// SocialProfile fetches a fresh account profile with its human-readable label.
// Callers own any cache and its lifetime; Client retains no profile cache.
func (c *Client) SocialProfile(ctx context.Context) (*CloudProfile, error) {
	var profile CloudProfile
	if err := c.GetJSON(ctx, "/userprofile-service/socialProfile", &profile); err != nil {
		return nil, err
	}

	profile.Name = profileName(profile)

	return &profile, nil
}

// profileName picks the best human label of a profile.
func profileName(sp CloudProfile) string {
	switch {
	case sp.FullName != "":
		return sp.FullName
	case sp.UserName != "":
		return sp.UserName
	case !strings.Contains(sp.DisplayName, "-"):
		return sp.DisplayName
	default:
		return ""
	}
}
