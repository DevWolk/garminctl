package web

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/garmin"
)

// Auto-sync watches for USB connections, syncs when signed in to Connect,
// checks software updates and sends desktop notifications.

const (
	watchTick    = 5 * time.Second
	probeTimeout = 30 * time.Second
	maxEvents    = 30
)

type autoEvent struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // plugged, unplugged, synced, updates, error
	Text string    `json:"text"`
}

type autoState struct {
	mu      sync.Mutex
	Enabled bool        `json:"enabled"`
	Events  []autoEvent `json:"events"`
}

func (s *Server) autoSnapshot() map[string]any {
	s.auto.mu.Lock()
	defer s.auto.mu.Unlock()

	ev := make([]autoEvent, len(s.auto.Events))
	copy(ev, s.auto.Events)

	return map[string]any{"enabled": s.auto.Enabled, "events": ev}
}

func (s *Server) autoNote(ctx context.Context, kind, text string, notifyUser bool) {
	s.auto.mu.Lock()

	s.auto.Events = append(s.auto.Events, autoEvent{At: time.Now(), Kind: kind, Text: text})
	if len(s.auto.Events) > maxEvents {
		s.auto.Events = s.auto.Events[len(s.auto.Events)-maxEvents:]
	}

	s.auto.mu.Unlock()

	if notifyUser && s.notify != nil {
		s.notify(ctx, "garminctl", text)
	}
}

// watch polls for the watch and reacts to plug and unplug. It returns when
// ctx ends.
type watchState struct {
	connected bool
	misses    int
	observed  garmin.ConnectionEpoch
}

func (s *Server) watch(ctx context.Context) {
	var (
		state   watchState
		actions sync.WaitGroup
	)
	defer actions.Wait()

	tick := time.NewTicker(s.watchEvery)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		epoch, name, ok := s.pollConnection(ctx, &state)
		if !ok {
			continue
		}

		s.autoNote(ctx, "plugged", name+" connected over USB", false)

		release, err := s.svc.Gate().EnterJob(ctx)
		if err != nil {
			continue
		}

		actions.Go(func() { defer release(); s.autoActions(ctx, epoch, name) })
	}
}

func (s *Server) pollConnection(ctx context.Context, state *watchState) (garmin.ConnectionEpoch, string, bool) {
	epoch := s.svc.CurrentEpoch()
	if epoch != 0 {
		state.connected = s.probe(ctx, state.connected && state.observed == epoch)
		if !state.connected || state.observed == epoch {
			return 0, "", false
		}
	} else {
		if state.connected {
			s.autoNote(ctx, "unplugged", "Watch unplugged", false)
		}

		state.connected = false

		state.misses++
		if state.misses%2 != 0 {
			return 0, "", false
		}
	}

	epoch, name, ok := s.tryOpen(ctx, epoch)
	if !ok {
		return 0, "", false
	}

	state.observed, state.connected = epoch, true

	return epoch, name, true
}

// probe checks that an open device still answers. Only a broken transport
// drops the session; other errors retain the previous connection state.
// It returns the new connected state.
func (s *Server) probe(ctx context.Context, connected bool) bool {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	observation, _ := s.svc.ProbeIfIdle(cctx, s.probeQuiet)
	switch observation {
	case garmin.ProbeDisconnected:
		if connected {
			s.autoNote(ctx, "unplugged", "Watch unplugged", false)
		}

		return false
	case garmin.ProbeConnected:
		return true
	default:
		return connected
	}
}

func (s *Server) tryOpen(ctx context.Context, expected garmin.ConnectionEpoch) (garmin.ConnectionEpoch, string, bool) {
	lease, err := s.svc.AcquireAt(ctx, expected)
	if err != nil {
		return 0, "", false
	}

	name := "Watch"
	sctx, cancel := context.WithTimeout(lease.Context(), probeTimeout)
	sum, summaryErr := lease.Service().Summary(sctx)

	cancel()

	if summaryErr == nil && sum.Model != "" {
		name = sum.Model
	}

	if errors.Is(summaryErr, device.ErrDisconnected) {
		summaryErr = errors.Join(summaryErr, s.svc.DropAt(lease.Epoch()))
	}

	epoch := lease.Epoch()
	if err := lease.Release(); err != nil || errors.Is(summaryErr, device.ErrDisconnected) || ctx.Err() != nil {
		return 0, "", false
	}

	return epoch, name, true
}

// autoActions starts sync and an update check after connection.
func (s *Server) autoActions(ctx context.Context, epoch garmin.ConnectionEpoch, name string) {
	var parts []string

	if s.cloud.LoggedIn() {
		cloud := garmin.NewJobCloud(s.svc, epoch, s.cloud)

		job, _, err := s.runtime.Sync.JoinOrStart(ctx, garmin.SyncOptions{Upload: true, Download: true, Epoch: epoch}, func(ctx context.Context, progress garmin.Progress) (*garmin.SyncResult, error) {
			return cloud.Sync(ctx, true, true, progress)
		})
		if err == nil {
			select {
			case <-job.Done():
				parts = s.syncSummary(job)
			case <-ctx.Done():
				return
			}
		} else {
			parts = append(parts, "sync failed: "+err.Error())
		}
	}

	parts = append(parts, s.updateNotes(ctx, epoch)...)
	if len(parts) == 0 {
		parts = append(parts, "synced, nothing new")
	}

	s.autoNote(ctx, "synced", name+": "+strings.Join(parts, "; "), true)
}

// syncSummary describes the outcome of the last sync job in a few phrases.
func (s *Server) syncSummary(job *garmin.SyncJob) []string {
	state := job.Snapshot()
	res, errText := state.Result, state.Error

	if errText != "" {
		return []string{"sync failed: " + errText}
	}

	if res == nil {
		return nil
	}

	var parts []string

	if res.Upload != nil && res.Upload.Uploaded > 0 {
		parts = append(parts, fmt.Sprintf("%d file(s) uploaded to Garmin Connect", res.Upload.Uploaded))
	}

	if len(res.Pulled) > 0 {
		parts = append(parts, fmt.Sprintf("%d item(s) from Connect written to the watch", len(res.Pulled)))
	}

	if len(res.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("%d sync error(s)", len(res.Errors)))
	}

	return parts
}

func (s *Server) updateNotes(ctx context.Context, epoch garmin.ConnectionEpoch) []string {
	lease, err := s.svc.AcquireAt(ctx, epoch)
	if err != nil {
		return nil
	}

	ups, err := lease.Service().CheckSoftwareUpdates(lease.Context())
	if errors.Is(err, device.ErrDisconnected) {
		err = errors.Join(err, s.svc.DropAt(epoch))
	}

	err = errors.Join(err, lease.Release())
	if err != nil || len(ups) == 0 {
		return nil
	}

	names := make([]string, 0, len(ups))
	for _, u := range ups {
		names = append(names, u.ProductName+" "+u.Version)
	}

	return []string{"update available: " + strings.Join(names, ", ")}
}

// startSync starts runtime-owned work after its initial lease is released.
func (s *Server) startSync(ctx context.Context, epoch garmin.ConnectionEpoch, up, down bool) (*garmin.SyncJob, bool, error) {
	cloud := garmin.NewJobCloud(s.svc, epoch, s.cloud)

	return s.runtime.Sync.Start(ctx, garmin.SyncOptions{Upload: up, Download: down, Epoch: epoch}, func(ctx context.Context, progress garmin.Progress) (*garmin.SyncResult, error) {
		return cloud.Sync(ctx, up, down, progress)
	})
}
