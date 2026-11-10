//go:build desktop

package desktop

import (
	"context"
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Scope lends the existing window lifetime to services assembled by the caller.
// Backend is observed only while holding an acquired scope after startup.
type Scope interface {
	Acquire() (context.Context, func(), error)
	Backend() (string, bool, error)
}

// ServiceRegistrar returns typed bindings without doing IO or starting work.
type ServiceRegistrar func(Scope) []application.Service

type serviceScope struct{ app *App }

func (s serviceScope) Acquire() (context.Context, func(), error) {
	return s.app.operations.begin()
}

func (s serviceScope) Backend() (string, bool, error) {
	if s.app.backend == nil || s.app.backend.URL == nil {
		return "", false, errors.New("desktop backend is not ready")
	}

	return s.app.backend.URL.String(), s.app.backend.cancel != nil, nil
}

func nativeServices(service *App, registrars []ServiceRegistrar) []application.Service {
	services := []application.Service{application.NewService(service)}
	for _, register := range registrars {
		services = append(services, register(serviceScope{app: service})...)
	}

	return services
}
