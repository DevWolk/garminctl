package mcpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const probeTestVersion = "v1.2.3"

func TestProbeServerChecksVersionAndPingWithoutCallingTools(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"ready", "wrong version", "ping fails"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			version := probeTestVersion
			if name == "wrong version" {
				version = "v0.9.0"
			}

			server := mcp.NewServer(&mcp.Implementation{Name: "garminctl", Version: version}, nil)

			var pings, tools atomic.Int32

			server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
				return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
					if method == "tools/call" {
						tools.Add(1)
					}

					if method == "ping" {
						pings.Add(1)

						if name == "ping fails" {
							return nil, errors.New("fixture ping refused")
						}
					}

					return next(ctx, method, req)
				}
			})

			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{JSONResponse: true})
			httpServer := httptest.NewServer(handler)
			t.Cleanup(httpServer.Close)

			err := ProbeServer(t.Context(), httpServer.URL, probeTestVersion)
			if (err == nil) != (name == "ready") || tools.Load() != 0 {
				t.Fatalf("probe error=%v tools=%d", err, tools.Load())
			}

			if name != "wrong version" && pings.Load() != 1 {
				t.Fatalf("pings=%d", pings.Load())
			}
		})
	}
}

func TestProbeServerRejectsRedirectsAndOversizedResponses(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"redirect", "oversized"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var redirected atomic.Int32

			destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
			t.Cleanup(destination.Close)

			protocol := mcp.NewServer(&mcp.Implementation{Name: "garminctl", Version: probeTestVersion}, &mcp.ServerOptions{Instructions: strings.Repeat("x", 65<<10)})
			large := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return protocol }, &mcp.StreamableHTTPOptions{JSONResponse: true})

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "redirect" {
					http.Redirect(w, r, destination.URL, http.StatusFound)
					return
				}

				large.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)

			if err := ProbeServer(t.Context(), server.URL, probeTestVersion); err == nil {
				t.Fatal("unbounded/redirected startup MCP accepted")
			}

			if redirected.Load() != 0 {
				t.Fatal("startup MCP followed a response-selected URL")
			}
		})
	}
}
