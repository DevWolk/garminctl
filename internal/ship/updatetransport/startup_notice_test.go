package updatetransport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

const startupTestVersion = "v1.2.3"

func startupFixture(t *testing.T) (string, *os.Root, startupTicket) {
	t.Helper()
	cache := t.TempDir()

	directory := filepath.Join(cache, "stage-fixture")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := writeOperationMarker(directory, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}

	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = root.Close() })

	ticket, err := newStartupTicket(root, directory)
	if err != nil {
		t.Fatal(err)
	}

	return cache, root, ticket
}

func startupEncoded(t *testing.T, ticket startupTicket) string {
	t.Helper()

	data, err := json.Marshal(ticket)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestStartupNoticeBindsOperationAndCompiledVersion(t *testing.T) {
	t.Parallel()
	cache, root, ticket := startupFixture(t)

	notify, err := NewStartupNotice(cache, startupEncoded(t, ticket), startupTestVersion)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := root.Stat(startupReceiptName); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("registration wrote readiness before the frontend loaded")
	}

	if err := notify(t.Context(), "http://127.0.0.1:8787", true); err != nil {
		t.Fatal(err)
	}

	report, err := awaitStartup(t.Context(), root, ticket, startupTestVersion)
	if err != nil || report.DesktopVersion != startupTestVersion || !report.BackendOwned {
		t.Fatalf("report=%+v error=%v", report, err)
	}

	if _, err := newStartupTicket(root, filepath.Join(cache, ticket.Directory)); err == nil {
		t.Fatal("a completed restart accepted another startup ticket")
	}
}

func TestStartupTicketAndReceiptRejectForeignOrUnboundedState(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"directory escape", "foreign marker", "unknown protocol", "bad token", "ticket exceeds limit", "second JSON", "symlink escape", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cache, _, ticket := startupFixture(t)
			encoded := ""

			switch name {
			case "directory escape":
				ticket.Directory = "../stage-outside"
			case "foreign marker":
				ticket.Operation = strings.Repeat("b", 64)
			case "unknown protocol":
				ticket.Protocol++
			case "bad token":
				ticket.Token = "invalid"
			case "ticket exceeds limit":
				encoded = strings.Repeat("x", 2049)
			case "symlink escape":
				ticket.Directory = "stage-link"

				outside := t.TempDir()
				if err := writeOperationMarker(outside, ticket.Operation); err != nil {
					t.Fatal(err)
				}

				if err := os.Symlink(outside, filepath.Join(cache, ticket.Directory)); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}

			if encoded == "" {
				encoded = startupEncoded(t, ticket)
			}

			if name == "second JSON" {
				encoded += " {}"
			}

			notify, err := NewStartupNotice(cache, encoded, startupTestVersion)
			if err == nil {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()

				if name == "cancelled" {
					cancel()
				}

				err = notify(ctx, "http://127.0.0.1:8787", true)
			}

			if err == nil {
				t.Fatal("invalid startup notice accepted")
			}
		})
	}
}

func TestStartupReportRejectsMalformedFiles(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"directory", "large report", "second value", "unknown field"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, root, _ := startupFixture(t)

			var err error

			switch name {
			case "directory":
				err = root.Mkdir(startupReceiptName, 0o700)
			case "large report":
				err = root.WriteFile(startupReceiptName, []byte(strings.Repeat("x", 4097)), 0o600)
			case "unknown field":
				err = root.WriteFile(startupReceiptName, []byte(`{"unexpected":true}`), 0o600)
			default:
				err = root.WriteFile(startupReceiptName, []byte(`{} {}`), 0o600)
			}

			if err != nil {
				t.Fatal(err)
			}

			if _, err := readStartupReceipt(root); err == nil {
				t.Fatal("malformed startup report accepted")
			}
		})
	}
}

func TestStartupWaitHasDefaultDeadlineAndPreservesCancellation(t *testing.T) {
	t.Parallel()
	_, root, ticket := startupFixture(t)
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()

		_, err := awaitStartup(t.Context(), root, ticket, startupTestVersion)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != 45*time.Second {
			t.Fatalf("startup wait=%s error=%v", time.Since(started), err)
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := awaitStartup(ctx, root, ticket, startupTestVersion); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled startup=%v", err)
	}
}

func TestStartupReceiptRejectsStaleVersionsOwnershipAndURLs(t *testing.T) {
	t.Parallel()
	_, _, ticket := startupFixture(t)
	valid := startupReceipt{Protocol: 1, Operation: ticket.Operation, Token: ticket.Token, DesktopVersion: startupTestVersion, BackendURL: "http://127.0.0.1:8787", BackendOwned: true}

	for _, tc := range []struct {
		name string
		edit func(*startupReceipt)
	}{
		{"foreign operation", func(r *startupReceipt) { r.Operation = strings.Repeat("b", 64) }},
		{"stale token", func(r *startupReceipt) { r.Token = strings.Repeat("b", 64) }},
		{"old protocol", func(r *startupReceipt) { r.Protocol++ }},
		{"mixed desktop", func(r *startupReceipt) { r.DesktopVersion = "v0.9.0" }},
		{"reused backend", func(r *startupReceipt) { r.BackendOwned = false }},
		{"remote backend", func(r *startupReceipt) { r.BackendURL = "http://192.0.2.1:8787" }},
		{"credentials", func(r *startupReceipt) { r.BackendURL = "http://user:secret@127.0.0.1:8787" }},
		{"query", func(r *startupReceipt) { r.BackendURL += "?x=1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := valid
			tc.edit(&r)

			if err := validateStartupReceipt(r, ticket, startupTestVersion); err == nil {
				t.Fatal("invalid startup receipt accepted")
			}
		})
	}
}
