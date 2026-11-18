package apps

import (
	"context"
	"fmt"
	"strings"

	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

// FindAction resolves a store app by file id, store id or name.
type FindAction struct{}

// Run matches exactly first, then by part of the name; a ref that matches
// more than one app is refused.
func (FindAction) Run(ctx context.Context, sess *watchsession.Session, ref string) (*App, error) {
	return findApp(ctx, sess, ref)
}

func findApp(ctx context.Context, sess *watchsession.Session, ref string) (*App, error) {
	apps, err := readRegistry(ctx, sess)
	if err != nil {
		return nil, err
	}

	ref = strings.TrimSpace(ref)

	var exact, partial []*App

	for i := range apps {
		a := &apps[i]
		if a.FileName == "" {
			continue
		}

		if strings.EqualFold(a.FileName, ref) || strings.EqualFold(a.ID, ref) || strings.EqualFold(a.Name, ref) {
			exact = append(exact, a)
		} else if strings.Contains(strings.ToLower(a.Name), strings.ToLower(ref)) {
			partial = append(partial, a)
		}
	}

	found := exact
	if len(found) == 0 {
		found = partial
	}

	if len(found) == 0 {
		return nil, fmt.Errorf("no Connect IQ app matches %q", ref)
	}

	if err := oneApp(ref, found); err != nil {
		return nil, err
	}

	return found[0], nil
}
