package garmin

import (
	"bytes"
	"io"
	"net/http"
)

const (
	deviceMessagesPath = "/device-service/devicemessage/messages"
	socialProfilePath  = "/userprofile-service/socialProfile"
)

// Compatibility tests share the synthetic transport until the facade is removed.
type syncTransport func(*http.Request) (*http.Response, error)

func (f syncTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func syncResponse(req *http.Request, body []byte) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}
}
