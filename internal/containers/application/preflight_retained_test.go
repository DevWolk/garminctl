package application_test

import (
	"context"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/containers/application"
)

func TestRetainedUpdatesReadsOnlyEligibleMetadataWithoutMaintenance(t *testing.T) {
	t.Parallel()

	inventory, packages, request := preflightFixture()
	pre := packages.rows[1]
	pre.Tag = "v9.0.0-beta.1"
	pre.ID = strings.Repeat("e", 64)
	pre.Prerelease = true
	foreign := packages.rows[1]
	foreign.OS = preflightTestWindows
	foreign.ID = strings.Repeat("f", 64)
	draft := pre
	draft.Draft = true
	draft.ID = strings.Repeat("0", 64)
	packages.rows = append(packages.rows, pre, foreign, draft)
	action := application.NewPreflightAction(inventory, packages, func(context.Context) (func(), error) { t.Fatal("listing obtained maintenance"); return nil, nil })

	rows, err := action.RetainedUpdates(t.Context(), false)
	if err != nil || len(rows) != 1 || rows[0].ID != request.CacheID || len(packages.opened) != 0 {
		t.Fatalf("rows=%+v %v opens=%v", rows, err, packages.opened)
	}

	rows, err = action.RetainedUpdates(t.Context(), true)
	if err != nil || len(rows) != 2 || rows[0].Tag != pre.Tag || len(packages.opened) != 0 {
		t.Fatalf("prerelease rows=%+v %v", rows, err)
	}
}
