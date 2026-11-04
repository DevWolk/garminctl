package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

const (
	healthFixtureDayURL    = "/api/health/day?date=2000-01-02"
	emptyHealthFixtureMode = "empty"
)

func TestHealthHTTPContracts(t *testing.T) { //nolint:paralleltest // cases isolate the process cache directory
	for _, test := range []struct { //nolint:paralleltest // cases isolate process state
		name, method, path, body, mode string
		empty                          bool
		status                         int
		hash                           string
	}{
		{name: "decoded activities", method: http.MethodGet, path: "/api/health/activities?wait=1", status: 200, hash: "6efaacfd0ac437c57e041b2f625fa02d74df98237c0d8aae569e1d4e05a36604"},
		{name: "empty activities", method: http.MethodGet, path: "/api/health/activities?wait=1", empty: true, status: 200, hash: "6a4d086f763e723a70dd42b0ceed984087e6789bd5789bb733646261503cd05b"},
		{name: "activity detail", method: http.MethodGet, path: "/api/health/activities/fixture.fit", status: 200, hash: "447b4bbce5048ad4a50dda92fb512b1a7670b66cd884809cdf7a64733738715a"},
		{name: "invalid activity name", method: http.MethodGet, path: "/api/health/activities/bad..fit", status: 500, hash: "f1140f0490e18b802a0d75490f9a0154e1bcac81bbaad0331c5898436b3c8d74"},
		{name: "daily two", method: http.MethodGet, path: "/api/health/daily?days=2", status: 200, hash: "f7364a11ce1b4ef5cba53188c035f7a25bbf4cd5c8656b5c54826f06d36dba38"},
		{name: "daily default", method: http.MethodGet, path: "/api/health/daily?days=0", status: 200, hash: "e94ccc9da6b8f23f6863a36adb7de3abad8ed24c5c9e3ca8a5727cb971296bf2"},
		{name: "daily long range", method: http.MethodGet, path: "/api/health/daily?days=15", status: 200, hash: "7bf2334c88ed26a3d64fa2ef265b4ac9aff5d2ca8bb0dc246d13484f6719111c"},
		{name: "daily row errors", method: http.MethodGet, path: "/api/health/daily?days=2", mode: "daily error", status: 200, hash: "84ddebf4013ed78e9f3ad29f9886f0eb051ab49645c43069ee1dcc74c4e307e3"},
		{name: "day series", method: http.MethodGet, path: healthFixtureDayURL, status: 200, hash: "c306a3ecc8eae2bce601d26a5cb7a8eece4719a82ee111fb3792fb8fd435389a"},
		{name: "day partial", method: http.MethodGet, path: healthFixtureDayURL, mode: "partial day", status: 200, hash: "7afb16639049fdb356ade503c587c6bb6d03df868efca164142177df9a92c12c"},
		{name: "day empty", method: http.MethodGet, path: healthFixtureDayURL, mode: emptyHealthFixtureMode, status: 200, hash: "6b2ae963a7ff65e66f2a08da1907680d8656b47f09a5959e26f0d33f60b6313c"},
		{name: "invalid day", method: http.MethodGet, path: "/api/health/day?date=wrong", status: 500, hash: "a551dbdf82a03543736bf9e164a8461860c777e62761d358df28f9176e7581f0"},
		{name: "missing profile", method: http.MethodGet, path: healthFixtureDayURL, mode: "missing profile", status: 500, hash: "73b49bf2d7d55b00db3a291eb40e96636c877aa406539943c13858eb6e122813"},
		{name: "fitness extras", method: http.MethodGet, path: "/api/health/extras", status: 200, hash: "937a74d970b0873f987fb09bec0503191edb700512ddea175c00742af958157f"},
		{name: "empty extras", method: http.MethodGet, path: "/api/health/extras", mode: emptyHealthFixtureMode, status: 200, hash: "0d9ebc2a930c0e9c94663feaa92691ef4490285fedce5f82120a05c56edc1d37"},
		{name: "partial extras", method: http.MethodGet, path: "/api/health/extras", mode: "partial extras", status: 200, hash: "8e4b47e71d825566fce1266b468ffadb0198c625c61aadc6dfae5c912251ec3e"},
		{name: "log weight", method: http.MethodPost, path: "/api/health/weight", body: `{"kg":70.5}`, status: 200, hash: "0be5db28e3e9d802f2b7d1b995c5e33b74ad2f0855307b7d944705ab24d47cd8"},
		{name: "invalid weight", method: http.MethodPost, path: "/api/health/weight", body: `{"kg":19}`, status: 500, hash: "a40a21987eac1147b5babfe9b22c115eecfb391299d2b73ae6dfc8ea09a0b422"},
		{name: "trailing weight JSON", method: http.MethodPost, path: "/api/health/weight", body: `{"kg":70} {}`, status: 400, hash: "a91149e68d1e9075873e9379fce36cec342265fe2ce7993a85c1e0bd2bfee77f"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.HealthEnvironment(t)

			root := testdump.HealthRoot(t)
			if test.empty {
				root = testdump.Root(t)
			}

			s := newServer(t.Context(), func(context.Context) (device.Device, error) { return device.NewDirFS(root) }, false, testdump.HealthClient(t, test.mode))
			ts := serveContractServer(t, s)

			req, err := http.NewRequestWithContext(t.Context(), test.method, ts.URL+test.path, bytes.NewBufferString(test.body))
			if err != nil {
				t.Fatal(err)
			}

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			data, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != test.status {
				t.Fatalf("status=%d body=%s error=%v", resp.StatusCode, data, err)
			}

			testdump.CheckDigest(t, testdump.NormalizeHealthDates(data), test.hash)
		})
	}
}
