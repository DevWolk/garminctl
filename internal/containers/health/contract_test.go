package health

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/DevWolk/garminctl/internal/testdump"
)

func TestHealthModelContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
		hash  string
	}{
		{name: "summary zero", value: ActivitySummary{}, hash: "30cfdb04a52244e3f275e7beaf8bd2a2bca15a7d2146197d5e8054f29650bafa"},
		{name: "activity list nil", value: ActivityList{}, hash: "7509498699e4b3374da9ae4b819060f3417c3bce751eaf9ac56d75c010a10092"},
		{name: "activity list empty", value: ActivityList{Activities: []ActivitySummary{}}, hash: "3e810b7b2d9fc72234ee1be63fc61d09d3aca592b91334aa637a9806f1b0b3d7"},
		{name: "detail nil track laps", value: ActivityDetail{}, hash: "977f282a11351924217fd52d724515a5738f8818cee26f707d0dfac164dbc955"},
		{name: "daily zero", value: DailyHealth{}, hash: "d3dd054706bf264ceaf4affea09f19388fb51e1f60aa6c8681aa6cd718520987"},
		{name: "day nil series", value: DayDetail{}, hash: "d4eb3ebd6353120f52536c9b808e75e756cea6a8a21d5b96ac8cb8073a83acaf"},
		{name: "sleep zero", value: SleepSummary{}, hash: "ac3d171e69a4e343dbcfdadec727190fce41d8148709cac2adb3c3999d8e4323"},
		{name: "extras nil lists", value: Extras{}, hash: "16ba73e1f7c737ab70d9c2297de52d1204fac8804e1c8aa659820981a5743e9d"},
		{name: "extras empty lists", value: Extras{Records: []PersonalRecord{}, Badges: []Badge{}, Weight: []WeightEntry{}}, hash: "9c3a62c5ad0c1c1577fbd4052827c70dec34470035e811d99fd02794674479c0"},
	} {
		t.Run(test.name, func(t *testing.T) { t.Parallel(); testdump.CheckJSON(t, test.value, test.hash) })
	}
}

// weightTransport adapts a function to http.RoundTripper.
type weightTransport func(*http.Request) (*http.Response, error)

func (f weightTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHealthWeightPayloadContract(t *testing.T) {
	t.Parallel()
	client := testdump.HealthClient(t, "")
	delegate := client.HTTP.Transport

	var sent []byte

	client.HTTP.Transport = weightTransport(func(req *http.Request) (*http.Response, error) {
		var err error

		sent, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}

		return delegate.RoundTrip(req)
	})

	when := time.Date(2000, time.January, 2, 3, 4, 5, 0, time.FixedZone("fixture", 2*60*60))
	if err := (LogWeightAction{Access: AccountAccess{Client: client}}).Run(t.Context(), 70.5, when); err != nil {
		t.Fatal(err)
	}

	testdump.CheckDigest(t, sent, "ff82aa6925e1adac56696980578508d74a6b903a83939a095205aee4b7774c64")
}
