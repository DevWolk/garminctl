package apps

import (
	"context"
	"fmt"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// FindAction resolves a store app by file id, store id or name.
type FindAction struct{}

// Run preserves the existing exact/partial matching and first-match policy.
func (FindAction) Run(ctx context.Context, sess *watchsession.Session, ref string) (*App, error) {
	return findApp(ctx, sess, ref)
}

func findApp(ctx context.Context, sess *watchsession.Session, ref string) (*App, error) {
	apps, err := readRegistry(ctx, sess)
	if err != nil {
		return nil, err
	}

	ref = strings.TrimSpace(ref)

	var partial *App

	for i := range apps {
		a := &apps[i]
		if strings.EqualFold(a.FileName, ref) || strings.EqualFold(a.ID, ref) || strings.EqualFold(a.Name, ref) {
			if a.FileName != "" {
				return a, nil
			}
		}

		if a.FileName != "" && partial == nil && strings.Contains(strings.ToLower(a.Name), strings.ToLower(ref)) {
			partial = a
		}
	}

	if partial != nil {
		return partial, nil
	}

	return nil, fmt.Errorf("no Connect IQ app matches %q", ref)
}
