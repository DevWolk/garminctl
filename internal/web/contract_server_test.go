package web

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
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
