package web

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/garmin"
	"github.com/DevWolk/garminctl/internal/notify"
)

// Auto-sync: the background behaviour Garmin Express had. While the server
// runs it watches USB; when the watch appears it syncs with Garmin Connect
// (if signed in), checks for software updates and shows a notification.

const (
	watchTick    = 5 * time.Second
	probeTimeout = 30 * time.Second
	syncTimeout  = 2 * time.Hour
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

	if notifyUser {
		notify.Send(ctx, "garminctl", text)
	}
}

// watch polls for the watch and reacts to plug and unplug. It returns when
// ctx ends.
func (s *Server) watch(ctx context.Context) {
	connected := false
	misses := 0

	tick := time.NewTicker(watchTick)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		if svc := s.svc.Current(); svc != nil {
			connected = s.probe(ctx, svc, connected)
			continue
		}

		// Not connected: look for the watch, but not on every tick (each try starts a bridge).
		misses++
		if misses%2 != 0 {
			continue
		}

		svc, ok := s.tryOpen(ctx)
		if !ok {
			continue
		}

		connected = true

		name := "Watch"

		sctx, cancel := context.WithTimeout(ctx, probeTimeout)
		if sum, err := svc.Summary(sctx); err == nil && sum.Model != "" {
			name = sum.Model
		}

		cancel()

		s.autoNote(ctx, "plugged", name+" connected over USB", false)

		go s.autoActions(ctx, svc, name)
	}
}

// probe checks that an open device still answers; a failure drops it.
// It returns the new connected state.
func (s *Server) probe(ctx context.Context, svc *garmin.Service, connected bool) bool {
	cctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	if _, err := svc.Device().Stat(cctx, "GARMIN"); err != nil {
		s.svc.Drop()

		if connected {
			s.autoNote(ctx, "unplugged", "Watch unplugged", false)
		}

		return false
	}

	return true // connected, by the user or by us
}

func (s *Server) tryOpen(ctx context.Context) (*garmin.Service, bool) {
	svc, err := s.svc.Get(ctx) // the opener bounds its own waiting
	if err != nil {
		return nil, false
	}

	return svc, true
}

// autoActions runs what Express did on plug-in: sync and an update check.
func (s *Server) autoActions(ctx context.Context, svc *garmin.Service, name string) {
	var parts []string

	if s.cloud.LoggedIn() {
		if done, started := s.startSync(ctx, svc, true, true); started {
			select {
			case <-done:
				parts = s.syncSummary()
			case <-ctx.Done():
				return
			}
		}
	}

	parts = append(parts, s.updateNotes(ctx, svc)...)
	if len(parts) == 0 {
		parts = append(parts, "synced, nothing new")
	}

	s.autoNote(ctx, "synced", name+": "+strings.Join(parts, "; "), true)
}

// syncSummary describes the outcome of the last sync job in a few phrases.
func (s *Server) syncSummary() []string {
	s.job.mu.Lock()
	res, errText := s.job.Result, s.job.Error
	s.job.mu.Unlock()

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

func (s *Server) updateNotes(ctx context.Context, svc *garmin.Service) []string {
	ups, err := svc.CheckSoftwareUpdates(ctx)
	if err != nil || len(ups) == 0 {
		return nil
	}

	names := make([]string, 0, len(ups))
	for _, u := range ups {
		names = append(names, u.ProductName+" "+u.Version)
	}

	return []string{"update available: " + strings.Join(names, ", ")}
}

// startSync begins the background sync job. It reports whether it started
// and, when it did, a channel closed once the job ends. The job outlives ctx:
// a sync begun from a request keeps going after the request ends.
func (s *Server) startSync(ctx context.Context, svc *garmin.Service, up, down bool) (<-chan struct{}, bool) {
	j := &s.job

	j.mu.Lock()

	if j.Running {
		j.mu.Unlock()
		return nil, false
	}

	done := make(chan struct{})
	j.Running, j.StartedAt, j.EndedAt, j.Log, j.Result, j.Error = true, time.Now(), time.Time{}, nil, nil, ""

	j.mu.Unlock()

	cloud := garmin.NewCloud(svc, s.cloud)

	go func() {
		defer close(done)

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), syncTimeout)
		defer cancel()

		res, err := cloud.Sync(sctx, up, down, func(line string) {
			j.mu.Lock()
			j.Log = append(j.Log, line)
			j.mu.Unlock()
		})

		j.mu.Lock()
		defer j.mu.Unlock()

		j.Running, j.EndedAt, j.Result = false, time.Now(), res
		if err != nil {
			j.Error = err.Error()
		}
	}()

	return done, true
}
