//go:build desktop

package app

import (
	"net/url"

	applicationnative "github.com/DevWolk/garminctl/internal/containers/application/ui/native"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// NewApplicationNativeService supplies metadata and package preparation to the window.
// The actual compiled desktop version is independent of a reused backend.
func NewApplicationNativeService(version, compiledKey string, scope applicationnative.Scope) application.Service {
	return applicationnative.Binding(scope, func(address string, owned bool) (applicationnative.Reader, error) {
		endpoint, err := url.Parse(address)
		if err != nil {
			return nil, err
		}

		return newApplicationNativeReader(ApplicationReadConfig{Inventory: ApplicationInventoryConfig{DesktopVersion: version, BackendURL: endpoint, BackendOwned: owned}}, compiledKey)
	})
}
