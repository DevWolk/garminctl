package garmin

import (
	"context"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// Diagnostics is a borrowed scope of handle operations with joined cleanup.
type Diagnostics = watchsession.Diagnostics

// WithDiagnostics lends handle operations for this callback and current lease.
func (s *Service) WithDiagnostics(ctx context.Context, run func(*Diagnostics) error) error {
	return s.session.WithDiagnostics(ctx, run)
}
