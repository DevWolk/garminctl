package updatetransport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

func awaitStartup(ctx context.Context, root *os.Root, ticket startupTicket, version string) (startupReceipt, error) {
	ready, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	return waitStartup(ready, root, ticket, version)
}

func waitStartup(ctx context.Context, root *os.Root, ticket startupTicket, version string) (startupReceipt, error) {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return startupReceipt{}, err
		}

		report, err := readStartupReceipt(root)
		if err == nil {
			return report, validateStartupReceipt(report, ticket, version)
		}

		if !errors.Is(err, os.ErrNotExist) {
			return startupReceipt{}, err
		}

		select {
		case <-ctx.Done():
			return startupReceipt{}, ctx.Err()
		case <-tick.C:
		}
	}
}

func validateStartupReceipt(report startupReceipt, ticket startupTicket, version string) error {
	if report.Protocol != 1 || report.Operation != ticket.Operation || report.Token != ticket.Token {
		return errors.New("startup report does not match this restart")
	}

	if report.DesktopVersion != version || !report.BackendOwned {
		return errors.New("restarted desktop version or owned backend does not match")
	}

	_, err := startupBackendURL(report.BackendURL)

	return err
}

func startupBackendURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}

	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.Port() == "" || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("startup probe requires a plain loopback backend address")
	}

	return u, nil
}

func probeStartupBackend(ctx context.Context, address, version string) error {
	u, err := startupBackendURL(address)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	transport := &http.Transport{ResponseHeaderTimeout: 3 * time.Second}
	defer transport.CloseIdleConnections()

	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	u.Path = "/api/server"

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("restarted backend metadata HTTP %d", response.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return err
	}

	if len(data) > 64<<10 {
		return errors.New("startup metadata exceeds 64 KiB")
	}

	var info struct {
		Application string `json:"application"`
		DesktopAPI  string `json:"desktopAPI"`
		Version     string `json:"version"`
	}
	if err := decodeStageJSON(data, &info); err != nil {
		return err
	}

	if info.Application != "garminctl" || info.DesktopAPI != "1" || info.Version != version {
		return errors.New("restarted backend version does not match the selected release")
	}

	return nil
}

func checkStartupServices(ctx context.Context, report startupReceipt, version string, probe func(context.Context, string, string) error) error {
	if probe == nil {
		return errors.New("startup MCP probe is not configured")
	}

	if err := probeStartupBackend(ctx, report.BackendURL, version); err != nil {
		return err
	}

	return probe(ctx, report.BackendURL+"/mcp", version)
}
