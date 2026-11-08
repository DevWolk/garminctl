package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/DevWolk/garminctl/internal/containers/application"
	"github.com/DevWolk/garminctl/internal/ship/hostinfo"
)

// ApplicationInventoryConfig is trusted local composition, never request input.
// DesktopVersion must be the desktop's compiled label, independently of backend.
type ApplicationInventoryConfig struct {
	DesktopVersion, DesktopPath, CLIPath string
	BackendURL                           *url.URL
	BackendOwned                         bool
	HTTPClient                           *http.Client
	LocalSource                          application.LocalInventorySource
}

// NewApplicationInventory constructs read-only probes without running them.
func NewApplicationInventory(config ApplicationInventoryConfig) (*application.ReadInventoryAction, error) {
	backend, err := applicationBackendInventory(config)
	if err != nil {
		return nil, err
	}

	local := config.LocalSource
	if local == nil {
		local = applicationLocalInventory{reader: hostinfo.New(hostinfo.Config{DesktopPath: config.DesktopPath, CLIPath: config.CLIPath, PackageName: "garminctl", RegistryKey: `Software\garminctl`})}
	}

	return application.NewReadInventoryAction(config.DesktopVersion, local, backend), nil
}

type applicationLocalInventory struct{ reader *hostinfo.Reader }

func (s applicationLocalInventory) Inspect(ctx context.Context) (application.LocalInventory, error) {
	value, err := s.reader.Inspect(ctx)
	return application.LocalInventory{OS: value.OS, Arch: value.Arch, Desktop: applicationBinaryObservation(value.Desktop), CLI: applicationBinaryObservation(value.CLI)}, err
}

func applicationBinaryObservation(binary hostinfo.Binary) application.BinaryObservation {
	return application.BinaryObservation{Path: binary.Path, Directory: binary.Directory, Name: binary.Name, Version: binary.Version, Error: binary.Error, UserDirectory: binary.UserDirectory, Ownership: application.OwnershipEvidence{Manager: binary.Ownership.Manager, Package: binary.Ownership.Package, Root: binary.Ownership.Root, Error: binary.Ownership.Error}}
}

type applicationBackendProbe struct {
	endpoint string
	owned    bool
	client   *http.Client
}

func applicationBackendInventory(config ApplicationInventoryConfig) (application.BackendInventorySource, error) {
	if config.BackendURL == nil {
		return nil, nil
	}

	endpoint := *config.BackendURL

	if !applicationInventoryEndpoint(endpoint) {
		return nil, errors.New("application backend inventory requires a plain loopback server URL")
	}

	endpoint.Path = "/api/server"

	client := http.Client{Timeout: 3 * time.Second}
	if config.HTTPClient != nil {
		client = *config.HTTPClient
	}

	client.Jar = nil

	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Transport == nil {
		transport := &http.Transport{ResponseHeaderTimeout: 3 * time.Second}
		if standard, ok := http.DefaultTransport.(*http.Transport); ok {
			transport = standard.Clone()
		}

		transport.Proxy = nil
		client.Transport = transport
	}

	return applicationBackendProbe{endpoint: endpoint.String(), owned: config.BackendOwned, client: &client}, nil
}

func (p applicationBackendProbe) Inspect(ctx context.Context) (application.BackendInfo, error) {
	result := application.BackendInfo{Owned: p.owned}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return result, err
	}

	response, err := p.client.Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("backend metadata HTTP %d", response.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return result, err
	}

	if len(body) > 64<<10 {
		return result, errors.New("backend metadata exceeds 64 KiB")
	}

	var info struct {
		Application string `json:"application"`
		DesktopAPI  int    `json:"desktopAPI"`
		Version     string `json:"version"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return result, err
	}

	if info.Application != "garminctl" || info.DesktopAPI != 1 || strings.TrimSpace(info.Version) == "" || len(info.Version) > 256 || strings.ContainsAny(info.Version, "\r\n") {
		return result, errors.New("backend metadata is not a compatible garminctl server")
	}

	result.Version = info.Version

	return result, nil
}

func applicationInventoryEndpoint(endpoint url.URL) bool {
	ip := net.ParseIP(endpoint.Hostname())
	if (endpoint.Scheme != "http" && endpoint.Scheme != "https") || ip == nil || !ip.IsLoopback() {
		return false
	}

	return endpoint.User == nil && endpoint.RawQuery == "" && !endpoint.ForceQuery && endpoint.Fragment == "" && endpoint.RawFragment == "" && endpoint.Opaque == "" && endpoint.RawPath == "" && (endpoint.Path == "" || endpoint.Path == "/")
}
