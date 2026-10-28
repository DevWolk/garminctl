package watchsession

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/DevWolk/garminctl/internal/device"
)

// EnsureDir creates every missing segment of dir on the watch.
func (s *Session) EnsureDir(ctx context.Context, dir string) error {
	cur := ""

	for seg := range strings.SplitSeq(dir, "/") {
		if seg == "" {
			continue
		}

		if cur == "" {
			cur = seg
		} else {
			cur += "/" + seg
		}

		if _, err := s.Stat(ctx, cur); err == nil {
			continue
		} else if !errors.Is(err, device.ErrNotFound) {
			return fmt.Errorf("stat %s: %w", cur, err)
		}

		if err := s.Mkdir(ctx, cur); err != nil {
			return fmt.Errorf("mkdir %s: %w", cur, err)
		}
	}

	return nil
}
