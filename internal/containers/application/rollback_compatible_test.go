package application

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/lifecycle"
)

type compatibleDataFixture struct {
	f           *rollbackFixture
	failStopped bool
	checks      int
}

func (s *compatibleDataFixture) Verify(_ context.Context, _, _ RecoverySnapshot, stopped bool) error {
	s.checks++
	if stopped && s.f.gate.Snapshot().State != lifecycle.Closed {
		return errors.New("compatibility check preceded backend join")
	}

	if stopped && s.failStopped {
		return errors.New("current data drifted after preparation")
	}

	return nil
}

func compatibleRollbackFixture() (*rollbackFixture, *compatibleDataFixture) {
	f := newRollbackFixture()
	formats := CurrentStateFormats()
	f.source.target.Formats, f.source.safety.Formats = &formats, &formats
	data := &compatibleDataFixture{f: f}
	f.action.compatibility = data

	return f, data
}

func TestCompatibleRollbackKeepsCurrentDataWithoutRestoreAndCommitsOnce(t *testing.T) {
	t.Parallel()

	f, data := compatibleRollbackFixture()
	request := f.request()
	request.KeepCurrentData = true

	p, err := f.action.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Error(err)
		}
	})

	view := p.View()
	if !view.Data.KeepCurrentData || len(view.Data.Changes) != 0 || view.Data.ReviewID == p.data.view.ReviewID {
		t.Fatalf("keep review=%+v", view.Data)
	}

	result, err := f.action.Run(t.Context(), p, rollbackConsent(p))
	if err != nil || result == nil || result.Data != nil || result.Handoff.State != HandoffAccepted || slices.Contains(f.events, rollbackEventRestore) || data.checks != 3 {
		t.Fatalf("result=%+v events=%v checks=%d error=%v", result, f.events, data.checks, err)
	}

	if value, err := f.action.Run(t.Context(), p, rollbackConsent(p)); value != nil || err == nil {
		t.Fatal("compatible handoff replayed")
	}
}

func TestCompatibleRollbackRefusesMissingChangedOrUnknownFormatsBeforeStop(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"legacy", "different format", "unknown", "missing reader"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f, _ := compatibleRollbackFixture()

			switch name {
			case "legacy":
				f.source.target.Formats = nil
			case "different format":
				v := CurrentStateFormats()
				v.MFA++
				f.source.target.Formats = &v
			case "unknown":
				v := CurrentStateFormats()
				v.Account++
				f.source.target.Formats, f.source.safety.Formats = &v, &v
			case "missing reader":
				f.action.compatibility = nil
			}

			request := f.request()

			request.KeepCurrentData = true
			if p, err := f.action.Prepare(t.Context(), request); err == nil || p != nil || f.gate.Snapshot().State != lifecycle.Running || slices.Contains(f.events, rollbackEventStop) {
				t.Fatalf("unsafe compatible preparation=%+v events=%v error=%v", p, f.events, err)
			}
		})
	}
}

func TestCompatibleRollbackRechecksStoppedBytesAndRefusesModeConsentSubstitution(t *testing.T) {
	t.Parallel()

	for _, wrongConsent := range []bool{false, true} {
		t.Run(map[bool]string{false: "drift", true: "restore consent"}[wrongConsent], func(t *testing.T) {
			t.Parallel()

			f, data := compatibleRollbackFixture()
			request := f.request()
			request.KeepCurrentData = true

			p, err := f.action.Prepare(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = p.Close() })

			consent := rollbackConsent(p)
			if wrongConsent {
				consent.Data.ReviewID = p.data.view.ReviewID
			} else {
				data.failStopped = true
			}

			result, err := f.action.Run(t.Context(), p, consent)
			if err == nil || slices.Contains(f.events, "commit") || slices.Contains(f.events, rollbackEventRestore) {
				t.Fatalf("unsafe commit=%+v events=%v error=%v", result, f.events, err)
			}

			if !wrongConsent && (result == nil || result.Handoff.State != HandoffStopRequested) {
				t.Fatalf("lost stopped receipt=%+v", result)
			}
		})
	}
}
