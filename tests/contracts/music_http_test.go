package contracts

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestMusicHTTPContracts(t *testing.T) { //nolint:paralleltest // cases isolate process account paths and transports.
	for _, test := range []struct { //nolint:paralleltest // cases isolate process account paths and transports.
		name, method, path string
		body               []byte
		status             int
		hash               string
	}{
		{name: "music list", method: http.MethodGet, path: "/api/music", status: http.StatusOK, hash: "35ccd9b0daf083e1b1197209034a020c6c9ce9ff8fd7cc004ee3732bdb746883"},
		{name: "add", method: http.MethodPost, path: "/api/music?folder=pODCASTS&name=Fixture.MP3", body: testdump.MusicPayload(), status: http.StatusOK, hash: "e83ca3b7b445bd892758856d6454c48c9a50ad3e42f846b3d4c98885a5647896"},
		{name: "remove", method: http.MethodDelete, path: "/api/music?path=" + testdump.MusicTrack, status: http.StatusOK, hash: contractOKHash},
		{name: "playlist", method: http.MethodPut, path: "/api/music/playlists/Mix", body: []byte(`{"tracks":["Music/Artist/Album/Local.mp3","Podcasts/episode.aac"]}`), status: http.StatusOK, hash: "072b09ff682df5a1995436f2712903a3cb7ae8f8d88875dd8b77e2347febee0b"},
		{name: "invalid format", method: http.MethodPost, path: "/api/music?name=file.txt", body: []byte("not audio"), status: http.StatusInternalServerError, hash: "1698fb8a8e88d88f352c66cb81a4c961ca5421d00f5857e614d4543459c25181"},
		{name: "invalid remove", method: http.MethodDelete, path: "/api/music?path=GARMIN/file.fit", status: http.StatusInternalServerError, hash: "7db627e7de8e2c7c980eb07c4a17f89e00b6117ee0a407d6672ea3ee3a2bec7a"},
		{name: "empty playlist", method: http.MethodPut, path: "/api/music/playlists/Mix", body: []byte(`{"tracks":[]}`), status: http.StatusInternalServerError, hash: "b4fa3b4392f052744ee76d34d8079a59714c9c76e7221cb097af0084e65c76d6"},
		{name: "trailing JSON", method: http.MethodPut, path: "/api/music/playlists/Mix", body: []byte(`{"tracks":[]} {}`), status: http.StatusBadRequest, hash: contractTrailingJSONHash},
	} {
		t.Run(test.name, func(t *testing.T) {
			isolateContractAccount(t)
			root := testdump.MusicRoot(t)
			ts := serveContractRoot(t, func(context.Context) (device.Device, error) { return device.NewDirFS(root) })

			req, err := http.NewRequestWithContext(t.Context(), test.method, ts.URL+test.path, bytes.NewReader(test.body))
			if err != nil {
				t.Fatal(err)
			}

			response, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()

			data, err := io.ReadAll(response.Body)
			if err != nil || response.StatusCode != test.status {
				t.Fatalf("status=%d body=%s error=%v", response.StatusCode, data, err)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}
