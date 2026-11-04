// Package health owns local activities and Garmin Connect health scenarios.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DevWolk/garminctl/internal/device"
	"github.com/DevWolk/garminctl/internal/fit"
	"github.com/DevWolk/garminctl/internal/paths"
	"github.com/DevWolk/garminctl/internal/ship/watchsession"
)

type activityReader struct {
	session *watchsession.Session
	profile *fit.Profile
}

func newActivityReader(profile *fit.Profile, sess *watchsession.Session) *activityReader {
	return &activityReader{session: sess, profile: profile}
}

// ---------- activities from the files on the watch ----------

// ActivitySummary is the session of one activity file.
type ActivitySummary struct {
	File       string    `json:"file"`
	Sport      string    `json:"sport"`
	SubSport   string    `json:"subSport,omitempty"`
	Start      time.Time `json:"start"`
	Elapsed    float64   `json:"elapsedSec"`
	Timer      float64   `json:"timerSec"`
	Distance   float64   `json:"distanceM"`
	Calories   int       `json:"calories"`
	AvgHR      int       `json:"avgHr"`
	MaxHR      int       `json:"maxHr"`
	AvgSpeed   float64   `json:"avgSpeedMs"`
	Ascent     int       `json:"ascentM"`
	Descent    int       `json:"descentM"`
	AvgCadence int       `json:"avgCadence"`
	HasGPS     bool      `json:"hasGps"`
	Size       int64     `json:"size"`
}

type activityCache struct {
	Entries map[string]ActivitySummary `json:"entries"` // file -> summary (Size guards staleness)
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case uint32:
		return float64(t)
	case uint16:
		return float64(t)
	case uint8:
		return float64(t)
	case int32:
		return float64(t)
	case int16:
		return float64(t)
	case int8:
		return float64(t)
	}

	return math.NaN()
}

func fieldNum(m *fit.NamedMessage, name string) float64 {
	f, ok := m.FieldByName(name)
	if !ok {
		return math.NaN()
	}

	return num(f.Value)
}

func fieldStr(m *fit.NamedMessage, name string) string {
	f, ok := m.FieldByName(name)
	if !ok || f.Value == nil {
		return ""
	}

	return fmt.Sprint(f.Value)
}

func semicircles(v float64) float64 { return v * 180 / 2147483648 }

func (s *activityReader) activityCachePath(unit uint64) string {
	return filepath.Join(paths.Cache(), fmt.Sprintf("activities-%d.json", unit))
}

// summarize decodes the session of one activity file.
func (s *activityReader) summarize(data []byte, name string) (ActivitySummary, error) {
	f, err := fit.Decode(data)
	if err != nil {
		return ActivitySummary{}, err
	}

	sess := f.MessagesByNum(18)
	if len(sess) == 0 {
		return ActivitySummary{}, errors.New("no session message")
	}

	m := s.profile.DescribeMessage(sess[len(sess)-1])

	a := ActivitySummary{File: name, Sport: fieldStr(&m, "sport"), SubSport: fieldStr(&m, "subSport"), Size: int64(len(data))}
	if a.SubSport == "generic" {
		a.SubSport = ""
	}

	if st := fieldNum(&m, "startTime"); !math.IsNaN(st) {
		a.Start = fit.TimestampTime(uint32(st))
	} else if ts := fieldNum(&m, "timestamp"); !math.IsNaN(ts) {
		a.Start = fit.TimestampTime(uint32(ts))
	}

	get := func(n string) float64 {
		v := fieldNum(&m, n)
		if math.IsNaN(v) {
			return 0
		}

		return v
	}
	a.Elapsed, a.Timer, a.Distance = get("totalElapsedTime"), get("totalTimerTime"), get("totalDistance")
	a.Calories, a.AvgHR, a.MaxHR = int(get("totalCalories")), int(get("avgHeartRate")), int(get("maxHeartRate"))

	a.AvgSpeed = get("enhancedAvgSpeed")
	if a.AvgSpeed == 0 {
		a.AvgSpeed = get("avgSpeed")
	}

	a.Ascent, a.Descent, a.AvgCadence = int(get("totalAscent")), int(get("totalDescent")), int(get("avgCadence"))
	a.HasGPS = !math.IsNaN(fieldNum(&m, "startPositionLat"))

	return a, nil
}

// ActivityList is the result of Activities: what is known so far.
type ActivityList struct {
	Activities []ActivitySummary `json:"activities"`
	Pending    int               `json:"pending"` // files not decoded yet
	Total      int               `json:"total"`
}

// activityJobs shares decode ownership for Services using the same cache file.
// Runtime-owned job cancellation and per-file leases are integrated separately.
var activityJobs = struct {
	sync.Mutex
	running map[string]*activityDecodeJob
}{running: map[string]*activityDecodeJob{}}

type activityDecodeJob struct {
	done chan struct{}
	err  error // published before closing done
}

// activityCacheMu serialises access to the on-disk activity caches, which
// every Service for the same watch shares.
var activityCacheMu sync.Mutex

// loadActivityCache reads a cache file; a missing or damaged file is empty.
// The caller holds activityCacheMu.
func loadActivityCache(cp string) activityCache {
	cache := activityCache{Entries: map[string]ActivitySummary{}}

	b, err := os.ReadFile(cp) //nolint:gosec // path is inside garminctl's cache directory
	if err == nil {
		_ = json.Unmarshal(b, &cache)
	}

	if cache.Entries == nil {
		cache.Entries = map[string]ActivitySummary{}
	}

	return cache
}

// saveActivityCache replaces a complete cache. The caller holds activityCacheMu.
func saveActivityCache(cp string, cache activityCache) (err error) {
	b, err := json.MarshalIndent(cache, "", " ")
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(cp), 0o700); err != nil {
		return err
	}

	f, err := os.CreateTemp(filepath.Dir(cp), ".activities-*.tmp")
	if err != nil {
		return err
	}

	defer func() {
		_ = f.Close() // close the descriptor on failure; successful close is checked below
		if cleanupErr := os.Remove(f.Name()); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			err = errors.Join(err, cleanupErr)
		}
	}()

	if _, err := f.Write(b); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(f.Name(), cp)
}

// mergeActivityCache reads current entries and replaces the cache under one lock.
// The caller has already finished device I/O and never holds this lock over USB.
func mergeActivityCache(cp string, a ActivitySummary) error {
	activityCacheMu.Lock()
	defer activityCacheMu.Unlock()

	cache := loadActivityCache(cp)
	cache.Entries[a.File] = a

	return saveActivityCache(cp, cache)
}

func cachedActivity(cp string, entry device.Entry) bool {
	activityCacheMu.Lock()
	defer activityCacheMu.Unlock()

	a, ok := loadActivityCache(cp).Entries[entry.Name]

	return ok && a.Size == entry.Size
}

// decodeActivities rechecks entries after obtaining decode ownership and merges
// each result into the latest cache, including independent concurrent entries.
func (s *activityReader) decodeActivities(ctx context.Context, cp string, entries []device.Entry) error {
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}

		if cachedActivity(cp, entry) {
			continue
		}

		data, err := s.session.ReadFile(ctx, "GARMIN/Activity/"+entry.Name)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			if errors.Is(err, device.ErrDisconnected) {
				return err
			}

			continue
		}

		a, err := s.summarize(data, entry.Name)
		if err != nil {
			a = ActivitySummary{File: entry.Name, Sport: "unreadable", Size: int64(len(data))}
		}

		if err := mergeActivityCache(cp, a); err != nil {
			return fmt.Errorf("save activity cache: %w", err)
		}
	}

	return nil
}

// reserveActivityJob publishes ownership before either background or wait=true
// decoding begins. A waiter joins its handle rather than starting another read.
func reserveActivityJob(cp string) (*activityDecodeJob, bool) {
	activityJobs.Lock()
	defer activityJobs.Unlock()

	if job := activityJobs.running[cp]; job != nil {
		return job, false
	}

	job := &activityDecodeJob{done: make(chan struct{})}
	activityJobs.running[cp] = job

	return job, true
}

func (s *activityReader) runActivityJob(ctx context.Context, cp string, todo []device.Entry, job *activityDecodeJob) {
	job.err = s.decodeActivities(ctx, cp, todo)

	activityJobs.Lock()
	delete(activityJobs.running, cp)
	close(job.done)
	activityJobs.Unlock()
}

func (s *activityReader) decodeOrJoin(ctx context.Context, cp string, todo []device.Entry, wait bool) error {
	job, started := reserveActivityJob(cp)
	if started {
		if wait {
			s.runActivityJob(ctx, cp, todo, job)
		} else {
			bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Hour)
			go func() {
				defer cancel()

				s.runActivityJob(bctx, cp, todo, job)

				if job.err != nil {
					slog.WarnContext(bctx, "activity decode failed", "error", job.err)
				}
			}()
		}
	}

	if !wait {
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-job.done:
		return job.err
	}
}

// Activities lists the activities on the watch. Files not decoded before are
// decoded in the background (reading 200 files over USB takes a while);
// call again until Pending is 0.
func (s *activityReader) Activities(ctx context.Context, wait bool) (*ActivityList, error) {
	scan, err := s.scanActivities(ctx)
	if err != nil {
		return nil, err
	}

	if len(scan.todo) > 0 {
		if err := s.decodeOrJoin(ctx, scan.cachePath, scan.todo, wait); err != nil {
			return nil, err
		}

		if wait {
			return s.Activities(ctx, false)
		}
	}

	return scan.list, nil
}

type activityScan struct {
	list      *ActivityList
	cachePath string
	todo      []device.Entry
}

func (s *activityReader) scanActivities(ctx context.Context) (*activityScan, error) {
	dx, err := s.session.DeviceXML(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := s.session.List(ctx, "GARMIN/Activity")
	if err != nil {
		return nil, err
	}

	cp := s.activityCachePath(dx.UnitID)

	activityCacheMu.Lock()
	cache := loadActivityCache(cp)
	activityCacheMu.Unlock()

	var todo []device.Entry

	out := &ActivityList{}

	for _, e := range entries {
		if e.Dir || !strings.EqualFold(path.Ext(e.Name), ".fit") {
			continue
		}

		out.Total++
		if c, ok := cache.Entries[e.Name]; ok && c.Size == e.Size {
			out.Activities = append(out.Activities, c)
		} else {
			todo = append(todo, e)
		}
	}

	sort.Slice(out.Activities, func(i, j int) bool { return out.Activities[i].Start.After(out.Activities[j].Start) })
	out.Pending = len(todo)

	return &activityScan{list: out, cachePath: cp, todo: todo}, nil
}

// TrackPoint is one downsampled record of an activity.
type TrackPoint struct {
	T    float64 `json:"t"` // seconds since start
	Lat  float64 `json:"lat,omitempty"`
	Lon  float64 `json:"lon,omitempty"`
	HR   int     `json:"hr,omitempty"`
	Spd  float64 `json:"spd,omitempty"` // m/s
	Alt  float64 `json:"alt,omitempty"`
	Dist float64 `json:"dist,omitempty"`
	Cad  int     `json:"cad,omitempty"`
}

// LapSummary is one lap.
type LapSummary struct {
	Start    float64 `json:"startSec"`
	Elapsed  float64 `json:"elapsedSec"`
	Distance float64 `json:"distanceM"`
	AvgHR    int     `json:"avgHr"`
	AvgSpeed float64 `json:"avgSpeedMs"`
}

// ActivityDetail is the summary plus track and laps.
type ActivityDetail struct {
	ActivitySummary
	Points []TrackPoint `json:"points"`
	Laps   []LapSummary `json:"laps"`
}

// Activity decodes one activity with its track (downsampled to maxPoints).
func (s *activityReader) Activity(ctx context.Context, name string, maxPoints int) (*ActivityDetail, error) {
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		return nil, errors.New("bad activity name")
	}

	data, err := s.session.ReadFile(ctx, "GARMIN/Activity/"+name)
	if err != nil {
		return nil, err
	}

	sum, err := s.summarize(data, name)
	if err != nil {
		return nil, err
	}

	f, _ := fit.Decode(data)
	d := &ActivityDetail{ActivitySummary: sum}
	start := float64(fit.Timestamp(sum.Start))
	recs := f.MessagesByNum(20)

	if maxPoints <= 0 {
		maxPoints = 1500
	}

	step := 1
	if len(recs) > maxPoints {
		step = (len(recs) + maxPoints - 1) / maxPoints
	}

	for i := 0; i < len(recs); i += step {
		m := s.profile.DescribeMessage(recs[i])
		d.Points = append(d.Points, trackPoint(&m, start))
	}

	for _, lm := range f.MessagesByNum(19) {
		m := s.profile.DescribeMessage(lm)
		d.Laps = append(d.Laps, lapSummary(&m, start))
	}

	return d, nil
}

// numOr returns the first of the named fields that is present.
func numOr(m *fit.NamedMessage, names ...string) (float64, bool) {
	for _, n := range names {
		if v := fieldNum(m, n); !math.IsNaN(v) {
			return v, true
		}
	}

	return 0, false
}

// trackPoint reads one record message.
func trackPoint(m *fit.NamedMessage, start float64) TrackPoint {
	p := TrackPoint{}
	if ts, ok := numOr(m, "timestamp"); ok {
		p.T = ts - start
	}

	if lat, lon := fieldNum(m, "positionLat"), fieldNum(m, "positionLong"); !math.IsNaN(lat) && !math.IsNaN(lon) {
		p.Lat, p.Lon = semicircles(lat), semicircles(lon)
	}

	if v, ok := numOr(m, "heartRate"); ok {
		p.HR = int(v)
	}

	if v, ok := numOr(m, "enhancedSpeed", "speed"); ok {
		p.Spd = v
	}

	if v, ok := numOr(m, "enhancedAltitude", "altitude"); ok {
		p.Alt = v
	}

	if v, ok := numOr(m, "distance"); ok {
		p.Dist = v
	}

	if v, ok := numOr(m, "cadence"); ok {
		p.Cad = int(v)
	}

	return p
}

// lapSummary reads one lap message.
func lapSummary(m *fit.NamedMessage, start float64) LapSummary {
	l := LapSummary{}
	if v, ok := numOr(m, "startTime"); ok {
		l.Start = v - start
	}

	if v, ok := numOr(m, "totalElapsedTime"); ok {
		l.Elapsed = v
	}

	if v, ok := numOr(m, "totalDistance"); ok {
		l.Distance = v
	}

	if v, ok := numOr(m, "avgHeartRate"); ok {
		l.AvgHR = int(v)
	}

	if v, ok := numOr(m, "enhancedAvgSpeed", "avgSpeed"); ok {
		l.AvgSpeed = v
	}

	return l
}
