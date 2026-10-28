package garmin

import "context"

// read retains the legacy lookup/population policy while Session owns bytes.
func (s *Service) read(ctx context.Context, p string, cached bool) ([]byte, error) {
	return s.session.ReadThrough(ctx, p, cached)
}
