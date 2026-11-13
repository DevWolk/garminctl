package application

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

const (
	rollbackEventRestore = "restore"
	rollbackEventBackup  = "backup"
	rollbackEventPayload = "payload"
	rollbackEventStop    = "stop"
)

type rollbackInventory struct{ value InventoryResult }

func (s *rollbackInventory) Info(context.Context) (*InventoryResult, error) {
	v := s.value
	return &v, nil
}

type rollbackPackages struct {
	rows   []PackageRecord
	copies []*rollbackCopy
}
type rollbackCopy struct {
	record PackageRecord
	closed int
}

func (p *rollbackCopy) Record() PackageRecord { return p.record }
func (p *rollbackCopy) Close() error          { p.closed++; return nil }
func (s *rollbackPackages) List(context.Context, DownloadTarget) ([]PackageRecord, error) {
	return slices.Clone(s.rows), nil
}

func (s *rollbackPackages) Open(_ context.Context, _ DownloadTarget, id string) (VerifiedPackage, error) {
	for _, r := range s.rows {
		if r.ID == id {
			p := &rollbackCopy{record: r}
			s.copies = append(s.copies, p)

			return p, nil
		}
	}

	return nil, errors.New("package not in fixture")
}

type rollbackSnapshots struct {
	source *restoreSnapshots
	event  func(string) error
}

func (s rollbackSnapshots) Create(_ context.Context, info PreflightInfo) (*DataSnapshot, error) {
	if err := s.event(rollbackEventBackup); err != nil {
		return nil, err
	}

	s.source.safety.Current, s.source.safety.Update = info.Current, info.Update

	return &DataSnapshot{ID: s.source.safety.ID, Directory: "/private/safety", Files: 2, Bytes: 21}, nil
}

type rollbackRestoration struct{ event func(string) error }

func (s rollbackRestoration) Restore(_ context.Context, old, safety RecoverySnapshot) (*DataRestoreResult, error) {
	err := s.event(rollbackEventRestore)
	return &DataRestoreResult{TargetSnapshotID: old.ID, SafetySnapshotID: safety.ID, Attempted: true, Completed: err == nil, Replaced: 1, Removed: 1}, err
}

type rollbackInstaller struct{ f *rollbackFixture }

func (s rollbackInstaller) Prepare(_ context.Context, input InstallationInput) (PendingInstallation, error) {
	if err := s.f.event(rollbackEventPayload); err != nil {
		return nil, err
	}

	if input.Info.Current != s.f.packages.rows[0] || input.Info.Update != s.f.packages.rows[1] {
		return nil, errors.New("wrong rollback payload")
	}

	return rollbackPending(s), nil
}

type rollbackPending struct{ f *rollbackFixture }

func (s rollbackPending) Commit(context.Context) error { return s.f.event("commit") }
func (s rollbackPending) Close() error                 { return s.f.event("close payload") }

type rollbackFixture struct {
	action    *RollbackAction
	inventory *rollbackInventory
	packages  *rollbackPackages
	source    *restoreSnapshots
	gate      *lifecycle.Gate
	events    []string
	failAt    string
	onEvent   func(string)
}

func (f *rollbackFixture) event(name string) error {
	f.events = append(f.events, name)
	if f.onEvent != nil {
		f.onEvent(name)
	}

	if f.failAt == name {
		return errors.New("fixture " + name + " failure")
	}

	return nil
}

func newRollbackFixture() *rollbackFixture {
	_, source, _, current, target := restorePolicyFixture()
	version := current.Record().Tag
	f := &rollbackFixture{source: source, gate: new(lifecycle.Gate), packages: &rollbackPackages{rows: []PackageRecord{current.Record(), target.Record()}}}
	f.inventory = &rollbackInventory{InventoryResult{OS: "linux", Arch: "amd64", Desktop: BinaryVersion{Path: "/trusted/garmin-desktop", Version: version}, CLI: BinaryVersion{Path: "/trusted/garminctl", Version: version}, Backend: BackendInfo{Owned: true, Version: version}, Installation: InstallationInfo{Kind: "deb", Package: "garminctl"}, VersionAgreement: "matching"}}
	restore := NewRestoreDataAction(NewRecoverySnapshotsAction(source), rollbackRestoration{f.event}, func(context.Context) error {
		if f.gate.Snapshot().State != lifecycle.Closed {
			return errors.New("backend is live")
		}

		return nil
	})
	stop := func(context.Context) error {
		f.gate.Stop()
		return f.event(rollbackEventStop)
	}
	f.action = NewRollbackAction(NewPreflightAction(f.inventory, f.packages, f.gate.TryMaintenance), NewBackupDataAction(rollbackSnapshots{source, f.event}), restore, NewInstallHandoffAction(f.inventory, rollbackInstaller{f}, stop))

	return f
}

func (f *rollbackFixture) request() RollbackRequest {
	r := f.packages.rows[1]
	return RollbackRequest{Package: PreflightRequest{Tag: r.Tag, CacheID: r.ID}, SnapshotID: f.source.target.ID}
}

func prepareRollback(t *testing.T, f *rollbackFixture) *PreparedRollback {
	t.Helper()

	p, err := f.action.Prepare(t.Context(), f.request())
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})

	return p
}

func rollbackConsent(p *PreparedRollback) RollbackConsent {
	return RollbackConsent{Installation: InstallConsent{Confirmed: true, EditsResolved: true}, Data: RestoreConsent{Confirmed: true, EditsResolved: true, ReviewID: p.View().Data.ReviewID}}
}

func TestRollbackOrdersSafetyPayloadStopRestoreAndSingleCommit(t *testing.T) {
	t.Parallel()

	f := newRollbackFixture()
	p := prepareRollback(t, f)
	view := p.View()

	view.Data.Changes[0].Path = "caller-altered-review"
	if p.View().Data.Changes[0].Path == "caller-altered-review" {
		t.Fatal("review aliases preparation")
	}

	if f.gate.Snapshot().State != lifecycle.Maintenance || !slices.Equal(f.events, []string{rollbackEventBackup}) {
		t.Fatalf("prepare did effects: %v", f.events)
	}

	consent := rollbackConsent(p)

	value, err := f.action.Run(t.Context(), p, consent)
	if err != nil || value == nil || value.Handoff.State != HandoffAccepted || value.Data == nil || !value.Data.Completed {
		t.Fatalf("rollback=%+v %v", value, err)
	}

	if !slices.Equal(f.events, []string{rollbackEventBackup, rollbackEventPayload, rollbackEventStop, rollbackEventRestore, "commit", "close payload"}) {
		t.Fatalf("order=%v", f.events)
	}

	if again, err := f.action.Run(t.Context(), p, consent); err == nil || again != nil || len(f.events) != 6 {
		t.Fatalf("replayed=%+v %v events=%v", again, err, f.events)
	}

	for _, artifact := range f.packages.copies {
		if artifact.closed != 1 {
			t.Fatalf("closed=%d", artifact.closed)
		}
	}
}

func TestRollbackRefusesBeforeStopWithoutExactConsent(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"install", rollbackEventRestore, "edits", "review", "foreign"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newRollbackFixture()
			p := prepareRollback(t, f)
			c := rollbackConsent(p)
			action := f.action

			switch name {
			case "install":
				c.Installation.Confirmed = false
			case rollbackEventRestore:
				c.Data.Confirmed = false
			case "edits":
				c.Data.EditsResolved = false
			case "review":
				c.Data.ReviewID = "other"
			case "foreign":
				action = newRollbackFixture().action
			}

			value, err := action.Run(t.Context(), p, c)
			if err == nil || value != nil || !slices.Equal(f.events, []string{rollbackEventBackup}) {
				t.Fatalf("accepted=%+v %v events=%v", value, err, f.events)
			}
		})
	}
}

func TestRollbackFailureNeverCommitsAfterRestoreFailureOrReplays(t *testing.T) {
	t.Parallel()

	for _, name := range []string{rollbackEventPayload, rollbackEventStop, rollbackEventRestore, "commit"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newRollbackFixture()
			p := prepareRollback(t, f)
			f.failAt = name

			value, err := f.action.Run(t.Context(), p, rollbackConsent(p))
			if err == nil || value == nil || value.Handoff.State == HandoffAccepted {
				t.Fatalf("failure=%+v %v", value, err)
			}

			if name != "commit" && slices.Contains(f.events, "commit") {
				t.Fatal("committed after failure")
			}

			if name == rollbackEventRestore && (value.Data == nil || !value.Data.Attempted || value.Data.Completed) {
				t.Fatalf("lost partial data effect=%+v", value)
			}

			count := len(f.events)
			if again, err := f.action.Run(t.Context(), p, rollbackConsent(p)); err == nil || again != nil || len(f.events) != count {
				t.Fatalf("failure replay=%+v %v", again, err)
			}
		})
	}
}

func TestRollbackKeepsOrdinaryUpdateDirectionAndRequiresOlderSelection(t *testing.T) {
	t.Parallel()

	f := newRollbackFixture()
	if value, err := f.action.preflight.Run(t.Context(), f.request().Package); err == nil || value != nil {
		t.Fatalf("ordinary update permitted downgrade=%+v %v", value, err)
	}

	if f.gate.Snapshot().State != lifecycle.Running || len(f.packages.copies) != 0 {
		t.Fatal("refused downgrade retained admission or opened packages")
	}

	r := f.request()
	r.Package.Tag = f.packages.rows[0].Tag

	r.Package.CacheID = f.packages.rows[0].ID
	if value, err := f.action.Prepare(t.Context(), r); err == nil || value != nil {
		t.Fatalf("rollback permitted equal version=%+v %v", value, err)
	}

	if f.gate.Snapshot().State != lifecycle.Running || len(f.events) != 0 {
		t.Fatal("invalid rollback started effects")
	}
}

func TestRollbackPreparationFailureReleasesOwnedResources(t *testing.T) {
	t.Parallel()

	for _, name := range []string{rollbackEventBackup, "snapshot mismatch", "reused backend", "unsupported portable", "tampered verified identity"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newRollbackFixture()

			switch name {
			case rollbackEventBackup:
				f.failAt = rollbackEventBackup
			case "snapshot mismatch":
				f.source.target.Current.Tag = "v0.1.0"
			case "reused backend":
				f.inventory.value.Backend.Owned = false
			case "unsupported portable":
				f.inventory.value.Installation.Kind = installationPortable

				f.inventory.value.Installation.Root = "/trusted"
				for i := range f.packages.rows {
					f.packages.rows[i].Installation = installationPortable
				}
			case "tampered verified identity":
				f.packages.rows[1].Digest = "bad"
			}

			value, err := f.action.Prepare(t.Context(), f.request())
			if err == nil || value != nil || f.gate.Snapshot().State != lifecycle.Running {
				t.Fatalf("prepare=%+v %v gate=%+v", value, err, f.gate.Snapshot())
			}

			for _, artifact := range f.packages.copies {
				if artifact.closed != 1 {
					t.Fatal("failed preparation retained a package")
				}
			}

			if slices.Contains(f.events, rollbackEventPayload) || slices.Contains(f.events, rollbackEventStop) {
				t.Fatal("failed preparation reached handoff")
			}
		})
	}
}

func TestRollbackCancellationStopsBeforeCommit(t *testing.T) {
	t.Parallel()

	for _, phase := range []string{rollbackEventPayload, rollbackEventStop, rollbackEventRestore} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()

			f := newRollbackFixture()
			p := prepareRollback(t, f)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			f.onEvent = func(name string) {
				if name == phase {
					cancel()
				}
			}

			value, err := f.action.Run(ctx, p, rollbackConsent(p))
			if !errors.Is(err, context.Canceled) || value == nil || slices.Contains(f.events, "commit") {
				t.Fatalf("canceled=%+v %v order=%v", value, err, f.events)
			}

			if phase == rollbackEventPayload && f.gate.Snapshot().State != lifecycle.Running {
				t.Fatal("pre-stop cancellation stranded maintenance")
			}
		})
	}
}

func TestRollbackContenderCannotCancelAndCloseJoinsActiveRestore(t *testing.T) {
	t.Parallel()

	for _, closeActive := range []bool{false, true} {
		t.Run(map[bool]string{false: "contender", true: "close"}[closeActive], func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				f := newRollbackFixture()
				p := prepareRollback(t, f)
				consent := rollbackConsent(p)
				entered, release := make(chan struct{}), make(chan struct{})

				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()

				f.onEvent = func(name string) {
					if name == rollbackEventRestore {
						close(entered)
						<-release
					}
				}
				done := make(chan error, 1)

				go func() { _, err := f.action.Run(t.Context(), p, consent); done <- err }()

				<-entered

				closed := make(chan error, 1)

				if closeActive {
					go func() { closed <- p.Close() }()

					<-p.prepared.lifetime.Done()

					select {
					case err := <-closed:
						t.Fatalf("Close returned before restore joined: %v", err)
					default:
					}
				} else {
					if value, err := f.action.Run(t.Context(), p, consent); value != nil || !errors.Is(err, ErrPreparationBusy) {
						t.Fatalf("contender=%+v %v", value, err)
					}
				}

				close(release)

				if err := <-done; (err != nil) != closeActive {
					t.Fatalf("active result=%v close=%v", err, closeActive)
				}

				if closeActive {
					if err := <-closed; err != nil {
						t.Fatal(err)
					}

					if slices.Contains(f.events, "commit") {
						t.Fatal("Close allowed installer commit")
					}
				}
			})
		})
	}
}
