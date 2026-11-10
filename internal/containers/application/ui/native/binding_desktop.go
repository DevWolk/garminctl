//go:build desktop

package native

import "github.com/wailsapp/wails/v3/pkg/application"

// Binding registers only the three read-only Service methods with Wails.
func Binding(scope Scope, factory Factory) application.Service {
	return application.NewService(NewService(scope, factory))
}
