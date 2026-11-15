package updatetransport

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	debTestStable = "1.2.3"
	debTestNewer  = "1.2.4"
)

func TestPrepareNativeInstallerPrereleaseToStable(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	request := installRequest{
		Target:  MatchRequest{Installation: InstallDEB},
		Release: ResolvedRelease{Package: AssetRef{Name: "garminctl_1.2.3_linux_amd64.deb"}},
	}

	var calls []string

	query := func(_ context.Context, program string, args ...string) (string, error) {
		calls = append(calls, program)
		switch program {
		case debMetadataTool:
			if !slices.Equal(args, []string{"--field", filepath.Join(directory, request.Release.Package.Name), "Version"}) {
				t.Fatalf("package query args=%v", args)
			}

			return "1.2.3\n", nil
		case debQueryTool:
			if !slices.Equal(args, []string{"-W", "-f=${Version}", "garminctl"}) {
				t.Fatalf("installed query args=%v", args)
			}

			return "1.2.3.rc.1\n", nil
		case debCompareTool:
			if !slices.Equal(args, []string{"--compare-versions", debTestStable, "lt", "1.2.3.rc.1"}) {
				t.Fatalf("comparison args=%v", args)
			}

			return "", nil
		default:
			t.Fatalf("unexpected query tool %s", program)
			return "", nil
		}
	}

	got, err := prepareNativeInstaller(t.Context(), request, directory, query)
	if err != nil || !slices.Contains(got.args, "--allow-downgrades") {
		t.Fatalf("confirmed stable target needs native downgrade permission: invocation=%+v err=%v queries=%v", got, err, calls)
	}

	if len(calls) != 3 || request.Install.AllowDowngrade {
		t.Fatalf("queries=%v request=%+v", calls, request)
	}
}

type nativeQueryExitError int

func (e nativeQueryExitError) Error() string { return fmt.Sprintf("query exit %d", e) }
func (e nativeQueryExitError) ExitCode() int { return int(e) }

func TestPrepareNativeInstallerUsesNativeComparison(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		target  string
		current string
		result  error
		allow   bool
		fail    bool
	}{
		{"native upgrade", debTestNewer, debTestStable, nativeQueryExitError(1), false, false},
		{"same native version", debTestStable, debTestStable, fmt.Errorf("wrapped: %w", nativeQueryExitError(1)), false, false},
		{"epoch reverses order", debTestNewer, "2:1.2.3", nil, true, false},
		{"query failure", debTestNewer, debTestStable, nativeQueryExitError(2), false, true},
		{"query canceled", debTestNewer, debTestStable, context.Canceled, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			query := func(_ context.Context, program string, _ ...string) (string, error) {
				switch program {
				case debMetadataTool:
					return tc.target, nil
				case debQueryTool:
					return tc.current, nil
				default:
					return "", tc.result
				}
			}

			got, err := prepareNativeInstaller(t.Context(), installRequest{Target: MatchRequest{Installation: InstallDEB}}, t.TempDir(), query)
			if (err != nil) != tc.fail || slices.Contains(got.args, "--allow-downgrades") != tc.allow {
				t.Fatalf("invocation=%+v err=%v", got, err)
			}
		})
	}
}

func TestPrepareNativeInstallerRefusesUnreadableVersions(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		tool string
		text string
		err  error
	}{
		{"package unreadable", debMetadataTool, "", errors.New("cannot read package")},
		{"not installed", debQueryTool, "", nativeQueryExitError(1)},
		{"empty target", debMetadataTool, "", nil},
		{"empty installed", debQueryTool, "", nil},
		{"multiple versions", debQueryTool, "1.2.3\n1.2.4", nil},
		{"option", debMetadataTool, "--force", nil},
		{"oversize", debMetadataTool, "1" + strings.Repeat("2", 256), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			query := func(_ context.Context, program string, _ ...string) (string, error) {
				if program == debCompareTool {
					t.Fatal("comparison attempted despite failed metadata")
				}

				if program == tc.tool {
					return tc.text, tc.err
				}

				return debTestStable, nil
			}

			got, err := prepareNativeInstaller(t.Context(), installRequest{Target: MatchRequest{Installation: InstallDEB}}, t.TempDir(), query)
			if err == nil || got.program != "" {
				t.Fatalf("unusable metadata produced invocation=%+v err=%v", got, err)
			}
		})
	}
}

func TestPrepareNativeInstallerPreservesExplicitRollbackAndOtherPlatforms(t *testing.T) {
	t.Parallel()

	for _, kind := range []InstallKind{InstallDEB, InstallRPM, InstallSetup} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			request := installRequest{Target: MatchRequest{Installation: kind}, Install: InstallOptions{AllowDowngrade: kind == InstallDEB}}
			query := func(context.Context, string, ...string) (string, error) {
				t.Fatal("unexpected native version query")
				return "", nil
			}
			directory := t.TempDir()

			want, err := nativeInstallerCommand(request, directory)
			if err != nil {
				t.Fatal(err)
			}

			got, err := prepareNativeInstaller(t.Context(), request, directory, query)
			if err != nil || got.program != want.program || !slices.Equal(got.args, want.args) {
				t.Fatalf("changed existing invocation=%+v err=%v", got, err)
			}
		})
	}
}

func TestPrepareNativeInstallerCancellationDoesNotBecomeComparisonFalse(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	query := func(_ context.Context, program string, _ ...string) (string, error) {
		if program == debCompareTool {
			cancel()
			return "", nativeQueryExitError(1)
		}

		return debTestStable, nil
	}

	got, err := prepareNativeInstaller(ctx, installRequest{Target: MatchRequest{Installation: InstallDEB}}, t.TempDir(), query)
	if err == nil || got.program != "" {
		t.Fatalf("canceled comparison produced invocation=%+v err=%v", got, err)
	}

	_, err = prepareNativeInstaller(ctx, installRequest{Target: MatchRequest{Installation: InstallDEB}}, t.TempDir(), func(context.Context, string, ...string) (string, error) {
		t.Fatal("canceled acquisition queried metadata")
		return "", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation=%v", err)
	}
}
