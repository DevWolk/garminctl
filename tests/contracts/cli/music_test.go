package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func TestMusicCLIContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		args   []string
		json   bool
		upload bool
		hash   string
	}{
		{name: "default text", hash: "5f87618b21f9a7748efc110775fdacc123bd32eeffe042e10b1c4b701d14d358"},
		{name: "library JSON", args: []string{"ls"}, json: true, hash: "2a884d34e405cf1d3fd613573f5737d8c721892b49a4f1899a90370d61a4fd49"},
		{name: "add text", args: []string{musicAddSubcommand}, upload: true, hash: "eb6a5e6fb3da7a61a7f20d1488c342d91afed8570d9c753ea08562a1db8e71b4"},
		{name: "add JSON flag", args: []string{musicAddSubcommand}, upload: true, json: true, hash: "eb6a5e6fb3da7a61a7f20d1488c342d91afed8570d9c753ea08562a1db8e71b4"},
		{name: "remove text", args: []string{"rm", testdump.MusicTrack}, hash: "2517836304f6dba02f41b060438947dd631e4dbd7e58fd346d85dcd64efeb802"},
		{name: "playlist text", args: []string{"playlist", "  Mix:/  ", testdump.MusicTrack, "Podcasts/episode.aac"}, hash: "c4a66c0912372af2ae1b283dd7ec53049a5f902b70347e78569d8fa86361439e"},
		{name: "playlist JSON flag", args: []string{"playlist", "Mix", testdump.MusicTrack}, json: true, hash: "c4a66c0912372af2ae1b283dd7ec53049a5f902b70347e78569d8fa86361439e"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := testdump.MusicRoot(t)

			args := append([]string(nil), test.args...)
			if test.upload {
				input := filepath.Join(t.TempDir(), "Fixture.MP3")
				if err := os.WriteFile(input, testdump.MusicPayload(), 0o600); err != nil {
					t.Fatal(err)
				}

				args = append(args, input, "-folder", "pODCASTS")
			}

			argv := []string{rootFlag, root}
			if test.json {
				argv = append(argv, "-json")
			}

			argv = append(append(argv, "music"), args...)

			var output, diagnostics bytes.Buffer
			if err := run(argv, &output, &diagnostics); err != nil || diagnostics.Len() != 0 {
				t.Fatalf("error=%v diagnostics=%s", err, &diagnostics)
			}

			testdump.CheckDigest(t, output.Bytes(), test.hash)
		})
	}
}
