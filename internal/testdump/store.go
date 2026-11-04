package testdump

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/DevWolk/garminctl/internal/ciqstore"
)

// StoreAppID matches the real installed Easy+ registry entry, case-insensitively.
const StoreAppID = "7c993e2b-70b3-459a-bbe0-db48a89af5a8"

const storeRecord = `{"id":"7C993E2B-70B3-459A-BBE0-DB48A89AF5A8","typeId":"1","appLocalizations":[{"locale":"en-US","name":"Fixture 世界 watch face with a long name","description":"Fixture description.","whatsNew":"New fixture features."}],"latestExternalVersion":"2.4","latestInternalVersion":24,"downloadCount":1234,"changedDate":946684800000,"averageRating":4.5,"reviewCount":2,"compatibleDevicePartNumbers":["006-B4426-00"],"permissions":["Fit"],"developer":{"developerDisplayName":"Fixture Developer"},"fileSizeInfo":{"byteCountByDeviceTypeId":{"42":1024}},"settingsAvailabilityInfo":{"availabilityByDeviceTypeId":{"42":false}}}`

const storeOtherRecord = `{"id":"new-app","typeId":"2","appLocalizations":[{"locale":"en","name":"New app"}],"latestInternalVersion":1,"compatibleDevicePartNumbers":["other-device"],"pricing":{"salePrice":{"formattedPrice":"$1.99"}}}`

// StoreRequests serves only synthetic public-store data and records requests.
type StoreRequests struct {
	mu       sync.Mutex
	requests []string
	failPath string
}

// StoreEnvironment replaces the process store client until cleanup. A matching
// API suffix in failPath returns an HTTP error; an empty value serves all data.
// Call from sequential tests only; no request reaches a network transport.
func StoreEnvironment(tb testing.TB, failPath string) *StoreRequests {
	tb.Helper()

	fixture := &StoreRequests{failPath: failPath}
	previous := ciqstore.HTTPClient
	ciqstore.HTTPClient = &http.Client{Transport: fixture}

	tb.Cleanup(func() { ciqstore.HTTPClient = previous })

	return fixture
}

// Count reports requests sent to the synthetic transport.
func (f *StoreRequests) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.requests)
}

// RoundTrip answers the exact endpoints used by the store contracts.
func (f *StoreRequests) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req.URL.RequestURI())
	f.mu.Unlock()

	if req.URL.Host != "apps.garmin.com" || req.Method != http.MethodGet {
		return nil, fmt.Errorf("unexpected store fixture request: %s %s", req.Method, req.URL)
	}

	p := strings.TrimPrefix(req.URL.Path, "/api/appsLibraryExternalServices/api/asw")
	status, body := http.StatusOK, ""

	switch p {
	case "/deviceTypes":
		body = `[{"id":"42","partNumber":"006-B4426-00"}]`
	case "/apps":
		body = "[" + storeRecord + "," + storeOtherRecord + "]"
	case "/apps/keywords":
		body = `{"totalCount":2,"apps":[` + storeRecord + "," + storeOtherRecord + "]}"
	case "/apps/" + StoreAppID:
		body = storeRecord
	case "/apps/" + StoreAppID + "/reviews":
		body = `[{"reviewerFullName":"Fixture Reviewer","rating":5,"text":"  Useful face.  ","createdDate":946771200000,"appVersionName":"2.4"}]`
	default:
		return nil, fmt.Errorf("unexpected store fixture path: %s", p)
	}

	if p == f.failPath {
		status, body = http.StatusConflict, `{"message":"fixture unavailable"}`
	}

	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// CheckPage asserts the actual HTTP pagination, independently of response data.
func (f *StoreRequests) CheckPage(tb testing.TB, size, offset int) {
	tb.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, raw := range f.requests {
		u, err := url.Parse(raw)
		if err != nil {
			tb.Fatal(err)
		}

		if !strings.HasSuffix(u.Path, "/apps") && !strings.HasSuffix(u.Path, "/apps/keywords") {
			continue
		}

		if u.Query().Get("pageSize") != strconv.Itoa(size) || u.Query().Get("startPageIndex") != strconv.Itoa(offset) {
			tb.Fatalf("pagination request=%s; want size=%d offset=%d", raw, size, offset)
		}

		return
	}

	tb.Fatal("no store page requested")
}
