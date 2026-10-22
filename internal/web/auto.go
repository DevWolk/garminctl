package web

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"garminctl/internal/garmin"
	"garminctl/internal/notify"
)

// Auto-sync: the background behaviour Garmin Express had. While the server
// runs it watches USB; when the watch appears it syncs with Garmin Connect
// (if signed in), checks for software updates and shows a notification.

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

func (s *Server) autoNote(kind, text string, notifyUser bool) {
	s.auto.mu.Lock()
	s.auto.Events = append(s.auto.Events, autoEvent{At: time.Now(), Kind: kind, Text: text})
	if len(s.auto.Events) > 30 {
		s.auto.Events = s.auto.Events[len(s.auto.Events)-30:]
	}
	s.auto.mu.Unlock()
	if notifyUser {
		notify.Send("garminctl", text)
	}
}

// watch polls for the watch and reacts to plug and unplug.
func (s *Server) watch(ctx context.Context) {
	connected := false
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.mu.Lock()
		svc := s.svc
		s.mu.Unlock()
		if svc != nil {
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err := svc.Device().Stat(cctx, "GARMIN")
			cancel()
			if err != nil {
				s.disconnectLocked()
				if connected {
					s.autoNote("unplugged", "Watch unplugged", false)
				}
				connected = false
				continue
			}
			if !connected { // connected by the user, not by us
				connected = true
			}
			continue
		}
		// Not connected: look for the watch, but not on every tick (each try starts a bridge).
		misses++
		if misses%2 != 0 {
			continue
		}
		octx, cancel := context.WithTimeout(ctx, 60*time.Second)
		svc, err := s.service(octx)
		cancel()
		if err != nil {
			continue
		}
		connected = true
		name := "Watch"
		if sum, err := svc.Summary(ctx); err == nil && sum.Model != "" {
			name = sum.Model
		}
		s.autoNote("plugged", name+" connected over USB", false)
		go s.autoActions(ctx, svc, name)
	}
}

// autoActions runs what Express did on plug-in: sync and an update check.
func (s *Server) autoActions(ctx context.Context, svc *garmin.Service, name string) {
	var parts []string
	if s.cloud.LoggedIn() {
		if s.startSync(svc, true, true) {
			for {
				time.Sleep(2 * time.Second)
				s.job.mu.Lock()
				running := s.job.Running
				res, errText := s.job.Result, s.job.Error
				s.job.mu.Unlock()
				if running {
					continue
				}
				if errText != "" {
					parts = append(parts, "sync failed: "+errText)
				} else if res != nil {
					if res.Upload != nil && res.Upload.Uploaded > 0 {
						parts = append(parts, fmt.Sprintf("%d file(s) uploaded to Garmin Connect", res.Upload.Uploaded))
					}
					if len(res.Pulled) > 0 {
						parts = append(parts, fmt.Sprintf("%d item(s) from Connect written to the watch", len(res.Pulled)))
					}
					if len(res.Errors) > 0 {
						parts = append(parts, fmt.Sprintf("%d sync error(s)", len(res.Errors)))
					}
				}
				break
			}
		}
	}
	if ups, err := svc.CheckSoftwareUpdates(ctx); err == nil && len(ups) > 0 {
		names := make([]string, 0, len(ups))
		for _, u := range ups {
			names = append(names, u.ProductName+" "+u.Version)
		}
		parts = append(parts, "update available: "+strings.Join(names, ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, "synced, nothing new")
	}
	s.autoNote("synced", name+": "+strings.Join(parts, "; "), true)
}

// startSync begins the background sync job and reports whether it started.
func (s *Server) startSync(svc *garmin.Service, up, down bool) bool {
	j := &s.job
	j.mu.Lock()
	if j.Running {
		j.mu.Unlock()
		return false
	}
	j.Running, j.StartedAt, j.EndedAt, j.Log, j.Result, j.Error = true, time.Now(), time.Time{}, nil, nil, ""
	j.mu.Unlock()
	cloud := garmin.NewCloud(svc, s.cloud)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		res, err := cloud.Sync(ctx, up, down, func(line string) {
			j.mu.Lock()
			j.Log = append(j.Log, line)
			j.mu.Unlock()
		})
		j.mu.Lock()
		j.Running, j.EndedAt, j.Result = false, time.Now(), res
		if err != nil {
			j.Error = err.Error()
		}
		j.mu.Unlock()
	}()
	return true
}
