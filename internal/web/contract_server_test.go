package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

const (
	contractOKHash           = "f9e952a6078dd131bab1fc9c7c94a923ee48bc2d0b2ebb670349397efcf5e1bb"
	contractTrailingJSONHash = "a91149e68d1e9075873e9379fce36cec342265fe2ce7993a85c1e0bd2bfee77f"
)

func serveContractServer(t *testing.T, s *Server) *httptest.Server {
	t.Helper()

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

	return ts
}
