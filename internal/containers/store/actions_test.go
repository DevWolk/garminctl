package store

import (
	"context"
	"errors"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/apps"
	"github.com/DevWolk/garminctl/internal/ship/ciqstore"
)

const installedID = "app"

func TestInstalledDecoration(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		entries   []apps.App
		err       error
		installed bool
		version   int
		update    bool
	}{
		{name: "native entry excluded", entries: []apps.App{{ID: installedID, Version: 1}}},
		{name: "case folded", entries: []apps.App{{ID: "APP", FileName: "FILE", Version: 2}}, installed: true, version: 2, update: true},
		{name: "newer installed", entries: []apps.App{{ID: installedID, FileName: "FILE", Version: 4}}, installed: true, version: 4},
		{name: "last duplicate wins", entries: []apps.App{{ID: installedID, FileName: "ONE", Version: 1}, {ID: "APP", FileName: "TWO", Version: 3}}, installed: true, version: 3},
		{name: "registry failure ignored", err: errors.New("registry unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			reads := 0
			read := func(context.Context) ([]apps.App, error) { reads++; return test.entries, test.err }

			out := decorateStore(t.Context(), read, []ciqstore.StoreApp{{ID: installedID, InternalVersion: 3}})
			if reads != 1 || len(out) != 1 || out[0].Installed != test.installed || out[0].InstalledVersion != test.version || out[0].UpdateAvailable != test.update {
				t.Fatalf("reads=%d decoration=%+v", reads, out)
			}
		})
	}
}

func TestNoRegistryKeepsEmptyArray(t *testing.T) {
	t.Parallel()

	if out := decorateStore(t.Context(), nil, nil); out == nil || len(out) != 0 {
		t.Fatalf("empty catalog must encode as []: %#v", out)
	}
}
