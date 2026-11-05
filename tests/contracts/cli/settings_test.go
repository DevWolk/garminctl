package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const (
	settingsContractCommand = "settings"
	settingsJSONFlag        = "-json"
)

func TestSettingsCLIContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		hash string
	}{
		{name: "json", args: []string{settingsJSONFlag, settingsContractCommand}, hash: "937b641e6ed3438ff9cbac2714a20c0317503ab9517e143d5a0f99f11711e55d"},
		{name: "json all", args: []string{settingsJSONFlag, settingsContractCommand, "-all"}, hash: "937b641e6ed3438ff9cbac2714a20c0317503ab9517e143d5a0f99f11711e55d"},
		{name: "text", args: []string{settingsContractCommand}, hash: "0bb799b58bcedb164e8ac94a895c95815c3d81b758a213baf29634ee2107a47a"},
		{name: "text all", args: []string{settingsContractCommand, "-all"}, hash: "bffa5e7ed42b6e8db534e043390653bee4baa2999bd2db330e25a0c50925654a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var out, errOut bytes.Buffer

			args := append([]string{rootFlag, testdump.Root(t)}, test.args...)
			if err := run(args, &out, &errOut); err != nil || errOut.Len() != 0 {
				t.Fatalf("CLI error=%v stderr=%s", err, errOut.String())
			}

			testdump.CheckDigest(t, out.Bytes(), test.hash)
		})
	}
}

func TestSettingsCLIApplyContract(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)
	if err := os.Mkdir(filepath.Join(root, "GARMIN", "NewFiles"), 0o750); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := run([]string{rootFlag, root, settingsJSONFlag, settingsContractCommand, "set", "2.58=1500"}, &out, &errOut); err != nil || errOut.Len() != 0 {
		t.Fatalf("apply error=%v stderr=%s", err, errOut.String())
	}

	data := testdump.NormalizePendingPath(t, out.Bytes())
	testdump.CheckDigest(t, data, "f51bebe61f75eeaccf26320c6a5dc38952897e300e5842433e4ac326bf50c6f6")
}
