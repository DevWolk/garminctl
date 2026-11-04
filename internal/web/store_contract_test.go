package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestStoreHTTPContracts(t *testing.T) { //nolint:paralleltest // fixture replaces the process store client
	for _, test := range []struct { //nolint:paralleltest // cases replace the process store client
		name, path, fail string
		connected        bool
		status           int
		hash             string
	}{
		{name: "offline list", path: "/api/store", status: http.StatusOK, hash: "4b9b2975bdb54807b13d587d54477ba8f8a7da5facbb203cb1849d4605c9d44e"},
		{name: "connected list", path: "/api/store", connected: true, status: http.StatusOK, hash: "205c888e4274699254e7a036b3654ef664939daf8684b12bcea2b4edc81646ef"},
		{name: "offline detail", path: "/api/store/apps/" + testdump.StoreAppID, status: http.StatusOK, hash: "62571ad0e3919fd95f2d6b161777960b212d08fed94559be379ed7360c049a9c"},
		{name: "connected detail", path: "/api/store/apps/" + testdump.StoreAppID, connected: true, status: http.StatusOK, hash: "984e4ab2dd233b3b8db4171c76efac850a01f15598b3eb820b5492813143e638"},
		{name: "filtered search", path: "/api/store?q=face&type=watchface&page=2", connected: true, status: http.StatusOK, hash: "e9b3d625ef8d889f47d2353e50bfef721912c39ae6f62c999c84bbe582f12db3"},
		{name: "invalid page defaults", path: "/api/store?page=bad", status: http.StatusOK, hash: "4b9b2975bdb54807b13d587d54477ba8f8a7da5facbb203cb1849d4605c9d44e"},
		{name: "reviews unavailable", path: "/api/store/apps/" + testdump.StoreAppID, connected: true, fail: "/apps/" + testdump.StoreAppID + "/reviews", status: http.StatusOK, hash: "785ea6742f62a57344c84e6a3fc7990be22049fececac047ee619bc6bd62ff54"},
		{name: "browse unavailable", path: "/api/store", fail: "/apps", status: http.StatusInternalServerError, hash: "4fe69e6b27cda04068fdc954a0aadae95721dd7bf4aebac4586780ac2794dbf4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := testdump.StoreEnvironment(t, test.fail)
			_, ts, opens := newStoreContractServer(t, test.connected)

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+test.path, nil)
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

			wantOpens := int32(0)
			if test.connected {
				wantOpens = 1
			}

			if opens.Load() != wantOpens {
				t.Fatalf("store opened a watch: opens=%d want=%d", opens.Load(), wantOpens)
			}

			if test.name == "filtered search" {
				fixture.CheckPage(t, 24, 48)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}

func newStoreContractServer(t *testing.T, connected bool) (*Server, *httptest.Server, *atomic.Int32) {
	t.Helper()
	root := testdump.Root(t)
	opens := new(atomic.Int32)
	s := newServer(t.Context(), func(context.Context) (device.Device, error) {
		opens.Add(1)
		return device.NewDirFS(root)
	}, false)

	if connected {
		lease, err := s.svc.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if err := lease.Release(); err != nil {
			t.Fatal(err)
		}
	}

	ts := httptest.NewUnstartedServer(nil)

	handler, err := s.handler(ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	ts.Config.Handler = handler
	ts.Start()
	t.Cleanup(func() {
		ts.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		if err := errors.Join(s.mcp.Close(ctx), s.runtime.Close(ctx)); err != nil {
			t.Error(err)
		}
	})

	return s, ts, opens
}
