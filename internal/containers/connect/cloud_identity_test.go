package connect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/device"
	"github.com/DevWolk/garminctl/internal/ship/gc"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

const socialProfilePath = "/userprofile-service/socialProfile"

type identityWatch struct {
	device.Device
	reads int
}

func (d *identityWatch) Read(_ context.Context, path string) ([]byte, error) {
	d.reads++

	if path != watchsession.PathDeviceXML {
		return nil, fmt.Errorf("unexpected identity read: %s", path)
	}

	return []byte(`<Device><Id>42</Id></Device>`), nil
}

func TestHealthIdentitySkipsWatchRegistrationButStatusRetainsIt(t *testing.T) {
	t.Parallel()

	watch := &identityWatch{}
	profileCalls, registrations := 0, 0
	client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case socialProfilePath:
			profileCalls++
			return syncResponse(req, []byte(`{"displayName":"opaque-user-id","fullName":"Synthetic Name"}`)), nil
		case "/device-service/deviceregistration/device/name/42":
			registrations++
			return syncResponse(req, []byte(`"synthetic watch"`)), nil
		default:
			return nil, fmt.Errorf("unexpected identity request: %s", req.URL.Path)
		}
	})

	cloud := NewCloud(watchsession.New(watch), client)
	for range 2 {
		name, err := cloud.DisplayName(t.Context())
		if err != nil || name != "opaque-user-id" {
			t.Fatalf("identity = %q, %v", name, err)
		}
	}

	if profileCalls != 1 || registrations != 0 || watch.reads != 0 {
		t.Fatalf("health identity: profile=%d registration=%d watch reads=%d", profileCalls, registrations, watch.reads)
	}

	st := cloud.Status(t.Context())

	want := &CloudStatus{
		LoggedIn: true, Registered: true, DeviceName: "synthetic watch", Email: "sync@example.invalid",
		Profile: &CloudProfile{DisplayName: "opaque-user-id", FullName: "Synthetic Name", Name: "Synthetic Name"},
	}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("status = %+v, want %+v", st, want)
	}

	if profileCalls != 1 || registrations != 1 || watch.reads != 1 {
		t.Fatalf("status: profile=%d registration=%d watch reads=%d", profileCalls, registrations, watch.reads)
	}
}

func TestCloudProfileCacheRemainsPerCloudAndForgettable(t *testing.T) {
	t.Parallel()

	requests := 0
	client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != socialProfilePath {
			return nil, fmt.Errorf("unexpected profile request: %s", req.URL.Path)
		}

		requests++

		return syncResponse(req, []byte(`{"displayName":"cached-identity","userName":"Synthetic User"}`)), nil
	})

	first, second := NewCloud(nil, client), NewCloud(nil, client)
	for _, cloud := range []*Cloud{first, first, second} {
		st := cloud.Status(t.Context())
		if st.Profile == nil || st.Profile.Name != "Synthetic User" || st.Error != "" {
			t.Fatalf("profile status = %+v", st)
		}
	}

	if requests != 2 {
		t.Fatalf("per-Cloud requests=%d, want 2", requests)
	}

	first.Forget()

	if st := first.Status(t.Context()); st.Profile == nil || requests != 3 {
		t.Fatalf("after Forget: status=%+v requests=%d", st, requests)
	}

	if err := client.Logout(); err != nil {
		t.Fatal(err)
	}

	if st := first.Status(t.Context()); st.LoggedIn || st.Profile != nil || requests != 3 {
		t.Fatalf("after logout: status=%+v requests=%d", st, requests)
	}
}

func TestCloudStatusRegistrationErrorsRemainDistinctFromIdentity(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == socialProfilePath {
					return syncResponse(req, []byte(`{"displayName":"status-identity"}`)), nil
				}

				if req.URL.Path != "/device-service/deviceregistration/device/name/42" {
					return nil, fmt.Errorf("unexpected registration request: %s", req.URL.Path)
				}

				resp := syncResponse(req, []byte(`"registered watch"`))
				resp.StatusCode = status

				return resp, nil
			})

			st := NewCloud(watchsession.New(&identityWatch{}), client).Status(t.Context())
			if !st.LoggedIn || st.Profile == nil || st.Registered != (status == http.StatusOK) {
				t.Fatalf("registration %d status=%+v", status, st)
			}

			if (st.Error != "") != (status == http.StatusServiceUnavailable) {
				t.Fatalf("registration %d error=%q", status, st.Error)
			}
		})
	}
}

func TestHealthIdentityRetainsExistingFailureContracts(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		body     string
		status   int
		err      error
		loggedIn bool
		want     string
	}{
		{name: "missing display name", body: `{}`, status: http.StatusOK, loggedIn: true, want: "the Garmin Connect profile has no display name"},
		{name: "profile failure", body: `{}`, status: http.StatusServiceUnavailable, loggedIn: true, want: "the Garmin Connect profile has no display name"},
		{name: "invalid profile", body: `{`, status: http.StatusOK, loggedIn: true, want: "the Garmin Connect profile has no display name"},
		{name: "rejected session", err: gc.ErrNotLoggedIn, want: "not signed in to Garmin Connect"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			watch := &identityWatch{}
			client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path != socialProfilePath {
					return nil, fmt.Errorf("unexpected failed-profile request: %s", req.URL.Path)
				}

				if tt.err != nil {
					return nil, tt.err
				}

				resp := syncResponse(req, []byte(tt.body))
				resp.StatusCode = tt.status

				return resp, nil
			})
			cloud := NewCloud(watchsession.New(watch), client)

			st := cloud.Status(t.Context())
			if st.LoggedIn != tt.loggedIn || st.Profile != nil && st.Profile.DisplayName != "" {
				t.Fatalf("failed-profile status=%+v", st)
			}

			_, err := cloud.DisplayName(t.Context())
			if err == nil || err.Error() != tt.want {
				t.Fatalf("identity error=%v, want %q", err, tt.want)
			}
		})
	}
}

func TestCloudIdentityWithMissingOrInvalidAccountStore(t *testing.T) {
	t.Parallel()

	for _, store := range []string{"", filepath.Join(t.TempDir(), "missing.json")} {
		t.Run(store, func(t *testing.T) {
			t.Parallel()

			watch := &identityWatch{}
			cloud := NewCloud(watchsession.New(watch), gc.New(store))

			st := cloud.Status(t.Context())
			if st.LoggedIn || st.Profile != nil || watch.reads != 0 {
				t.Fatalf("unavailable account status=%+v watch reads=%d", st, watch.reads)
			}

			if store == "" && !strings.Contains(st.Error, "config path is unavailable") {
				t.Fatalf("invalid store error=%q", st.Error)
			}

			_, err := cloud.DisplayName(t.Context())
			if err == nil || err.Error() != gc.ErrNotLoggedIn.Error() {
				t.Fatalf("missing account identity error=%v", err)
			}
		})
	}
}

// unreadableWatch fails every read.
type unreadableWatch struct{ device.Device }

func (*unreadableWatch) Read(context.Context, string) ([]byte, error) {
	return nil, errors.New("synthetic read failure")
}

func TestCloudStatusReportsWatchReadError(t *testing.T) {
	t.Parallel()

	client := syncTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != socialProfilePath {
			return nil, fmt.Errorf("unexpected request: %s", req.URL.Path)
		}

		return syncResponse(req, []byte(`{"displayName":"status-identity"}`)), nil
	})

	st := NewCloud(watchsession.New(&unreadableWatch{}), client).Status(t.Context())
	if !st.LoggedIn || st.Registered || !strings.Contains(st.Error, "synthetic read failure") {
		t.Fatalf("status=%+v, want the watch read error", st)
	}
}
