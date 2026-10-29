package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	settingshttp "github.com/DevWolk/garminctl/internal/containers/settings/ui/http"
	settingsmcp "github.com/DevWolk/garminctl/internal/containers/settings/ui/mcp"
	"github.com/DevWolk/garminctl/internal/containers/watch"
	watchhttp "github.com/DevWolk/garminctl/internal/containers/watch/ui/http"
	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/gc"
	"github.com/DevWolk/garminctl/internal/ship/httpx"
	"github.com/DevWolk/garminctl/internal/ship/mcpx"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
	"github.com/DevWolk/garminctl/internal/testdump"
)

type closeTrackedDevice struct {
	device.Device
	closes *int
}

func (d closeTrackedDevice) Close() error {
	*d.closes++
	return d.Device.Close()
}

func TestWithServiceDropsOnlyDisconnectedSessions(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		err    error
		status int
		drop   bool
	}{
		{name: "disconnected", err: device.ErrDisconnected, status: http.StatusServiceUnavailable, drop: true},
		{name: "busy", err: device.ErrBusy, status: http.StatusConflict},
		{name: "missing", err: device.ErrNotFound, status: http.StatusNotFound},
		{name: "validation", err: device.ErrInvalidArgument, status: http.StatusBadRequest},
		{name: "cancelled before I/O", err: context.Canceled, status: http.StatusRequestTimeout},
		{name: "in-flight deadline", err: errors.Join(device.ErrDisconnected, context.DeadlineExceeded), status: http.StatusGatewayTimeout, drop: true},
		{name: "domain error mentioning bridge", err: errors.New("invalid bridge label"), status: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			opens, closes := 0, 0
			lazy := garmin.NewLazy(func(context.Context) (device.Device, error) {
				opens++

				d, err := device.NewDirFS(root)

				return closeTrackedDevice{Device: d, closes: &closes}, err
			})
			t.Cleanup(lazy.Drop)

			s := &Server{svc: lazy}
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/files", nil)
			s.withService(w, r, func(context.Context, *garmin.Service) (any, error) { return nil, tt.err })

			if w.Code != tt.status || (closes == 1) != tt.drop {
				t.Fatalf("response status=%d, closes=%d, body=%s", w.Code, closes, w.Body)
			}

			if _, err := lazy.Get(t.Context()); err != nil {
				t.Fatal(err)
			}

			wantOpens := 1
			if tt.drop {
				wantOpens = 2
			}

			if opens != wantOpens {
				t.Fatalf("connection opens=%d, want=%d", opens, wantOpens)
			}
		})
	}
}

const (
	originHeader    = "Origin"
	fetchSiteHeader = "Sec-Fetch-Site"
	fetchModeHeader = "Sec-Fetch-Mode"
	crossSite       = "cross-site"
	loopback        = "127.0.0.1"
)

// newTestServer serves the API over a dump of a watch built from the
// garmin package's fixtures. It returns the server and the dump root.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	_, ts, root := newTestBackend(t, nil)

	return ts, root
}

// newServer retains the test fixture's constructor while production wiring
// supplies process owners through ServeRuntime.
func newServer(ctx context.Context, open garmin.Opener, autoSync bool) *Server {
	runtime := garmin.NewRuntime(ctx, open)

	return serverWithRuntime(runtime, gc.New(gc.DefaultStorePath()), autoSync, Features{
		HTTP: []httpx.RouteRegistrar{&settingshttp.Handler{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile()}, &watchhttp.Handler{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile()}},
		MCP:  []mcpx.ToolRegistrar{&settingsmcp.Tools{Manager: runtime.Watch.Manager(), Profile: fit.MustProfile(), BindContext: runtime.RequestContext}},
		WatchSummary: func(ctx context.Context, sess *watchsession.Session) (any, error) {
			return watch.NewSummaryAction(fit.MustProfile(), func(ctx context.Context, sess *watchsession.Session) (int, error) {
				apps, err := garmin.NewFromSession(sess).Apps(ctx)
				return len(apps), err
			}).Run(ctx, sess)
		},
	})
}

func newTestBackend(t *testing.T, cloud *gc.Client) (*Server, *httptest.Server, string) {
	t.Helper()

	root := testdump.Root(t)

	s := newServer(t.Context(), func(_ context.Context) (device.Device, error) { return device.NewDirFS(root) }, false)
	if cloud != nil {
		s.cloud = cloud
	} else {
		s.cloud = gc.New(filepath.Join(t.TempDir(), "garmin-connect.json"))
	}

	ts := httptest.NewUnstartedServer(nil)

	h, err := s.handler(ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	ts.Config.Handler = h
	ts.Config.BaseContext = func(net.Listener) context.Context { return s.runtime.Context() }
	ts.Start()

	t.Cleanup(func() {
		s.runtime.Stop()
		ts.Close()

		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 3*time.Second)
		defer cancel()

		if err := errors.Join(s.runtime.Close(ctx), s.mcp.Close(ctx)); err != nil {
			t.Error(err)
		}
	})

	return s, ts, root
}

func getJSON(t *testing.T, ts *httptest.Server, path string, v any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}

	return resp.StatusCode
}

func TestServer_StatusBeforeConnecting(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)

	var st statusResponse
	if code := getJSON(t, ts, "/api/status", &st); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}

	if st.Connected || st.Summary != nil || st.Auto["enabled"] != false {
		t.Fatalf("unexpected status before connecting: %+v", st)
	}
}

func TestServer_StoreDeviceSelection(t *testing.T) {
	t.Parallel()

	root := testdump.Root(t)
	s := newServer(t.Context(), func(_ context.Context) (device.Device, error) { return device.NewDirFS(root) }, false)
	t.Cleanup(s.svc.Drop)

	_, err := s.withOptionalService(t.Context(), func(ctx context.Context, svc *garmin.Service) (any, error) {
		if part := storePart(ctx, svc); svc != nil || part != "" {
			t.Fatalf("disconnected store must not assume a device: service=%v part=%q", svc, part)
		}

		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	lease, err := s.svc.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	svc := lease.Service()

	dx, err := svc.DeviceXML(lease.Context())
	if err := errors.Join(err, lease.Release()); err != nil {
		t.Fatal(err)
	}

	_, err = s.withOptionalService(t.Context(), func(ctx context.Context, selected *garmin.Service) (any, error) {
		if part := storePart(ctx, selected); selected != svc || part != dx.PartNumber {
			t.Fatalf("connected store selected service=%v part=%q, want service=%v part=%q", selected, part, svc, dx.PartNumber)
		}

		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServer_FilesConnectOnDemand(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)

	var entries []device.Entry
	if code := getJSON(t, ts, "/api/files?path=GARMIN", &entries); code != http.StatusOK {
		t.Fatalf("files = %d", code)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}

	if !strings.Contains(strings.Join(names, ","), "GarminDevice.xml") {
		t.Fatalf("GARMIN listing = %v", names)
	}

	var st statusResponse
	if code := getJSON(t, ts, "/api/status", &st); code != http.StatusOK || !st.Connected || st.Summary == nil {
		t.Fatalf("after a file listing the device should be connected: %d %+v", code, st)
	}

	if code := getJSON(t, ts, "/api/file?path=GARMIN/missing.fit", nil); code != http.StatusNotFound {
		t.Fatalf("missing file = %d, want 404", code)
	}
}

func TestServer_FileDownload(t *testing.T) {
	t.Parallel()

	ts, root := newTestServer(t)

	want, err := os.ReadFile(filepath.Join(root, "GARMIN", "GarminDevice.xml")) //nolint:gosec // test fixture in the temp dir
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/file?path=garmin/garmindevice.xml", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	got, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(got) != string(want) {
		t.Fatalf("download: %d, %d bytes (want %d)", resp.StatusCode, len(got), len(want))
	}

	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="garmindevice.xml"`) {
		t.Fatalf("Content-Disposition = %q", cd)
	}

	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff header missing")
	}
}

func TestServer_RefusesOtherOrigins(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)
	own := ts.Listener.Addr().String()

	tests := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		host    string
		want    int
	}{
		{name: "no browser headers", want: http.StatusOK},
		{name: "same origin", headers: map[string]string{originHeader: "http://" + own, fetchSiteHeader: "same-origin"}, want: http.StatusOK},
		{name: "localhost host", host: "localhost:" + own[strings.LastIndex(own, ":")+1:], want: http.StatusOK},
		{name: "foreign origin", headers: map[string]string{originHeader: "http://evil.example"}, want: http.StatusForbidden},
		{name: "cross-site fetch", headers: map[string]string{fetchSiteHeader: crossSite, fetchModeHeader: "cors"}, want: http.StatusForbidden},
		{name: "link from another site into the UI", path: "/", headers: map[string]string{fetchSiteHeader: crossSite, fetchModeHeader: "navigate", "Sec-Fetch-Dest": "document"}, want: http.StatusOK},
		{name: "link from another site into the API", headers: map[string]string{fetchSiteHeader: crossSite, fetchModeHeader: "navigate", "Sec-Fetch-Dest": "document"}, want: http.StatusForbidden},
		{name: "cross-site form post", method: http.MethodPost, headers: map[string]string{fetchSiteHeader: crossSite, fetchModeHeader: "navigate", originHeader: "http://evil.example"}, want: http.StatusForbidden},
		{name: "null origin post", method: http.MethodPost, headers: map[string]string{originHeader: "null"}, want: http.StatusForbidden},
		{name: "rebound host", host: "evil.example:8787", want: http.StatusForbidden},
		{name: "wrong port", host: loopback + ":1", want: http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := tt.method
			if method == "" {
				method = http.MethodGet
			}

			path := tt.path
			if path == "" {
				path = "/api/cloud/sync"
			}

			req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}

			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			if tt.host != "" {
				req.Host = tt.host
			}

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}

			defer resp.Body.Close()

			if resp.StatusCode != tt.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tt.want)
			}
		})
	}
}

func TestServer_NotFrameable(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}

	if got := resp.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
}

func TestServer_UploadTooLargeIsRefused(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)

	body := bytes.NewReader(make([]byte, maxUploadBytes+1))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/api/file?path=GARMIN/NewFiles/big.bin", body)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: status = %d, want 413", resp.StatusCode)
	}
}

func TestServer_SyncRejectsMalformedBody(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+"/api/cloud/sync", strings.NewReader("{not json"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed sync body: status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_UIRoutes(t *testing.T) {
	t.Parallel()

	ts, _ := newTestServer(t)

	for _, tc := range []struct {
		path string
		want int
		page bool
	}{
		{"/", http.StatusOK, true},
		{"/apps/EASY+", http.StatusOK, true},
		{"/files/GARMIN/Settings", http.StatusOK, true},
		{"/files/GARMIN/Activity/run.fit?view=decode", http.StatusOK, true},
		{"/store?q=solar", http.StatusOK, true},
		{"/app.js", http.StatusOK, false},
		{"/nope/EASY+", http.StatusNotFound, false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}

			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}

			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.want {
				t.Fatalf("status %d, want %d", resp.StatusCode, tc.want)
			}

			if got := strings.Contains(string(body), "<title>garminctl"); got != tc.page {
				t.Fatalf("serves the page: %v, want %v", got, tc.page)
			}
		})
	}

	t.Run("manifest", func(t *testing.T) {
		t.Parallel()
		testUIManifest(t, ts)
	})
}

func testUIManifest(t *testing.T, ts *httptest.Server) {
	t.Helper()

	body := getUIAsset(t, ts, "/manifest.json", "application/json")

	var manifest struct {
		ID            string `json:"id"`
		StartURL      string `json:"start_url"`
		Scope         string `json:"scope"`
		Display       string `json:"display"`
		LaunchHandler struct {
			ClientMode string `json:"client_mode"`
		} `json:"launch_handler"`
		Icons []struct {
			Src  string `json:"src"`
			Type string `json:"type"`
		} `json:"icons"`
	}

	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"id", manifest.ID, "/"},
		{"start_url", manifest.StartURL, "/"},
		{"scope", manifest.Scope, "/"},
		{"display", manifest.Display, "minimal-ui"},
		{"launch_handler", manifest.LaunchHandler.ClientMode, "focus-existing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}

	if len(manifest.Icons) == 0 {
		t.Fatal("manifest has no icons")
	}

	for _, icon := range manifest.Icons {
		t.Run(icon.Src, func(t *testing.T) {
			t.Parallel()
			getUIAsset(t, ts, icon.Src, icon.Type)
		})
	}
}

func getUIAsset(t *testing.T, ts *httptest.Server, path, contentType string) []byte {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d, want %d", path, resp.StatusCode, http.StatusOK)
	}

	if got := resp.Header.Get("Content-Type"); got != contentType {
		t.Errorf("%s: Content-Type = %q, want %q", path, got, contentType)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return body
}
