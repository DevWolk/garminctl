package architecture

import (
	"go/constant"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestArchitectureLoadsTheSelectedBuildMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		env  []string
		race bool
	}{
		{name: "active", race: architectureRaceEnabled},
		{name: "linux portable", env: []string{"GOOS=linux", architectureCGODisabled}},
		{name: "windows desktop", env: []string{"GOOS=windows", architectureCGODisabled}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			view := architectureView{name: test.name, tags: architectureBuildTags, env: test.env}

			loaded := loadArchitecture(t.Context(), t, view, packages.LoadTypes, "./tests/architecture/testdata/buildmode")
			if len(loaded) != 1 {
				t.Fatalf("build fixture matched %d packages", len(loaded))
			}

			enabled, ok := loaded[0].Types.Scope().Lookup("RaceEnabled").(*types.Const)
			if !ok {
				t.Fatal("build fixture has no race-mode constant")
			}

			if got := constant.BoolVal(enabled.Val()); got != test.race {
				t.Fatalf("loaded race mode = %t, test build requires %t", got, test.race)
			}
		})
	}
}
