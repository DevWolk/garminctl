package contracts

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const musicListTool = "music_list"

const musicPlaylistTool = "music_playlist"

func TestMusicMCPContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	for _, test := range []struct { //nolint:paralleltest // cases use isolated process account paths and transports.
		name, tool string
		args       map[string]any
		hash       string
	}{
		{name: "music list", tool: musicListTool, args: map[string]any{}, hash: "2f326e7dd17f69189e37f88ebd631a2a2bbb6a755439e8e5fe0d053b02ceb78a"},
		{name: "playlist", tool: musicPlaylistTool, args: map[string]any{"name": "Mix", "tracks": []string{testdump.MusicTrack, "Podcasts/episode.aac"}}, hash: "1b16813ae3c3410d144d9c8eb977b407d7d9d7d3675f70995d5840c059c1e539"},
		{name: "empty playlist", tool: musicPlaylistTool, args: map[string]any{"name": "Mix", "tracks": []string{}}, hash: "f88c7d28ea49057609b1e2ee1f5f2327d9b3c1783c04b018d74512fecfe3c38b"},
		{name: "invalid track", tool: musicPlaylistTool, args: map[string]any{"name": "Mix", "tracks": []string{"GARMIN/file.fit"}}, hash: "deeced24277387f28027fce3728bd95cc8ad4d61fc55b84f008998ca9fccde55"},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateContractAccount(t)
			client := newMCPContractClientForRoot(t, testdump.MusicRoot(t))

			result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: test.tool, Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}

			testdump.CheckJSON(t, result, test.hash)
		})
	}
}

func TestMusicMCPToolContracts(t *testing.T) { //nolint:paralleltest // the production backend reads isolated process account paths.
	checkToolContracts(t, map[string]string{musicListTool: "e3372bbda059f5f3e94b318148f8ec0c7c8bee93bdb6fd4e75d8f3b86ffe7a31", musicPlaylistTool: "8cd2188b3192deb5ca6f582d1560ef1171d1a160bdbe9f0c251c5d5875ed118c"})
}
