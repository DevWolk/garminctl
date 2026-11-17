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
	debTestOlder  = "0.5.1"
)

func TestPrepareNativeInstallerPrereleaseToStable(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	request := installRequest{
		Target:  MatchRequest{ReleaseTag: "v" + debTestStable, Installation: InstallDEB},
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

			got, err := prepareNativeInstaller(t.Context(), installRequest{Target: MatchRequest{ReleaseTag: "v" + tc.target, Installation: InstallDEB}}, t.TempDir(), query)
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

			got, err := prepareNativeInstaller(t.Context(), installRequest{Target: MatchRequest{ReleaseTag: "v" + debTestStable, Installation: InstallDEB}}, t.TempDir(), query)
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

			request := installRequest{Target: MatchRequest{ReleaseTag: "v" + debTestStable, Installation: kind}, Install: InstallOptions{AllowDowngrade: kind == InstallDEB}}

			var tools []string

			query := func(_ context.Context, program string, _ ...string) (string, error) {
				tools = append(tools, program)

				return debTestStable, nil
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

			// Only the package's own version is read; an explicit rollback and the other platforms compare nothing.
			wantTools := map[InstallKind][]string{InstallDEB: {debMetadataTool}, InstallRPM: {rpmMetadataTool}}[kind]
			if !slices.Equal(tools, wantTools) {
				t.Fatalf("native queries=%v, want %v", tools, wantTools)
			}
		})
	}
}

func TestPrepareNativeInstallerRefusesPackageOfAnotherRelease(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, tag, packaged string
		kind                InstallKind
		rollback, valid     bool
	}{
		{"DEB of the selected release", "v" + debTestNewer, debTestNewer, InstallDEB, false, true},
		{"DEB prerelease spelling", "v1.2.4-rc.1", "1.2.4.rc.1", InstallDEB, false, true},
		{"older signed DEB under a newer name", installTestTag, debTestOlder, InstallDEB, false, false},
		{"older signed DEB in a confirmed rollback", installTestTag, debTestOlder, InstallDEB, true, false},
		{"DEB with an epoch", "v" + debTestNewer, "1:" + debTestNewer, InstallDEB, false, false},
		{"RPM of the selected release", "v" + debTestNewer, debTestNewer, InstallRPM, false, true},
		{"older signed RPM under a newer name", installTestTag, debTestOlder, InstallRPM, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			query := func(_ context.Context, program string, _ ...string) (string, error) {
				switch program {
				case debMetadataTool, rpmMetadataTool:
					return tc.packaged + "\n", nil
				case debQueryTool:
					return "0.6.0\n", nil
				default:
					return "", nativeQueryExitError(1)
				}
			}
			request := installRequest{Target: MatchRequest{ReleaseTag: tc.tag, Installation: tc.kind}, Install: InstallOptions{AllowDowngrade: tc.rollback}}

			got, err := prepareNativeInstaller(t.Context(), request, t.TempDir(), query)
			if (err == nil) != tc.valid || (!tc.valid && got.program != "") {
				t.Fatalf("package version %s for %s: invocation=%+v err=%v", tc.packaged, tc.tag, got, err)
			}

			if !tc.valid && !strings.Contains(err.Error(), tc.packaged) {
				t.Fatalf("refusal does not name the packaged version: %v", err)
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

	request := installRequest{Target: MatchRequest{ReleaseTag: "v" + debTestStable, Installation: InstallDEB}}

	got, err := prepareNativeInstaller(ctx, request, t.TempDir(), query)
	if !errors.Is(err, context.Canceled) && ctx.Err() == nil {
		t.Fatalf("comparison was not reached: %v", err)
	}

	if err == nil || got.program != "" {
		t.Fatalf("canceled comparison produced invocation=%+v err=%v", got, err)
	}

	_, err = prepareNativeInstaller(ctx, request, t.TempDir(), func(context.Context, string, ...string) (string, error) {
		t.Fatal("canceled acquisition queried metadata")
		return "", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preparation=%v", err)
	}
}
