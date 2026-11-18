// Package desktop manages the local server used by the native application.
package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const applicationName = "garminctl"

// Backend is either an existing local server or one owned by this application.
type Backend struct {
	URL       *url.URL
	cancel    context.CancelFunc
	done      <-chan error
	closeOnce sync.Once
	closeErr  error
}

// StartBackend reserves the loopback port or reuses a running garminctl server
// that reads the same source (see app.WatchSource). When the server at addr
// reads another watch or directory, it starts its own on a free port of the
// same loopback IP. It never takes over a port occupied by an unrelated program.
func StartBackend(ctx context.Context, addr, source string, serve func(context.Context, net.Listener) error) (*Backend, error) {
	host, err := loopbackHost(addr)
	if err != nil {
		return nil, err
	}

	var config net.ListenConfig

	listener, err := config.Listen(ctx, "tcp", addr)
	if err == nil {
		return ownedBackend(ctx, listener, serve)
	}

	running, err := existingBackend(ctx, addr, err)
	if err != nil {
		return nil, err
	}

	if running.source == source {
		return &Backend{URL: running.url}, nil
	}

	listener, err = config.Listen(ctx, "tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return nil, fmt.Errorf("cannot start the desktop server: %w", err)
	}

	return ownedBackend(ctx, listener, serve)
}

func loopbackHost(addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return "", fmt.Errorf("desktop server address must be a loopback IP and port: %q", addr)
	}

	return host, nil
}

func ownedBackend(ctx context.Context, listener net.Listener, serve func(context.Context, net.Listener) error) (*Backend, error) {
	if serve == nil {
		return nil, errors.Join(errors.New("desktop backend runner is required"), listener.Close())
	}

	runCtx, cancel := context.WithCancel(ctx)

	done := make(chan error, 1)
	go func() {
		done <- serve(runCtx, listener)
	}()

	return &Backend{URL: &url.URL{Scheme: "http", Host: listener.Addr().String()}, cancel: cancel, done: done}, nil
}

// runningServer is a garminctl server found at the requested address.
type runningServer struct {
	url    *url.URL
	source string
}

func existingBackend(ctx context.Context, addr string, listenErr error) (*runningServer, error) {
	u := &url.URL{Scheme: "http", Host: addr}

	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, u.String()+"/api/server", nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot start or reach garminctl at %s: %w", addr, errors.Join(listenErr, err))
	}
	defer response.Body.Close()

	var info struct {
		Application string `json:"application"`
		DesktopAPI  string `json:"desktopAPI"`
		Source      string `json:"source"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&info) != nil || info.Application != applicationName || info.DesktopAPI != "1" {
		return nil, fmt.Errorf("%s is occupied by another program or an older garminctl; stop it before opening the desktop app: %w", addr, listenErr)
	}

	return &runningServer{url: u, source: info.Source}, nil
}

// Close shuts down only a server started by this application. A server that
// was already running belongs to its caller and is left running.
func (b *Backend) Close() error {
	if b.cancel == nil {
		return nil
	}

	b.closeOnce.Do(func() {
		b.cancel()
		b.closeErr = <-b.done
	})

	return b.closeErr
}
