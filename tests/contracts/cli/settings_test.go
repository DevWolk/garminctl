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
		{name: "json", args: []string{settingsJSONFlag, settingsContractCommand}, hash: "ec6c8cb9e7554899b650d1bf18cd43c0126a83de7f4c6d449209dd2181872e87"},
		{name: "json all", args: []string{settingsJSONFlag, settingsContractCommand, "-all"}, hash: "ec6c8cb9e7554899b650d1bf18cd43c0126a83de7f4c6d449209dd2181872e87"},
		{name: "text", args: []string{settingsContractCommand}, hash: "b19af15b2660835354e98e49546af45575c61cfd34141ad7991c9177a5cd609c"},
		{name: "text all", args: []string{settingsContractCommand, "-all"}, hash: "e77b348989d4df93324d1d70bf9503636ac224f9396a22eb5d56b78fba5e6f97"},
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
