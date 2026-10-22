# Contributing

garminctl is a small Go project; a change should build, pass `make check`
and keep the byte-exact codec tests green.

## Prerequisites

- Go 1.27 or newer (the `go` directive in `go.mod` is the floor).
- `make`.
- golangci-lint and govulncheck are pinned as `tool` directives in
  `tools.mod` (a separate module file, so the app's `go.mod` stays small);
  `make lint` and `make vuln` run them through `go tool -modfile=tools.mod`
  and nothing else has to be installed. `make tools-update` bumps them;
  Dependabot does not read `tools.mod`.
- A watch is needed only for live tests; everything else runs against a
  dump (`-root DIR`, a copy of the watch's `GARMIN/` folder) or the fixtures
  under `*/testdata`.

## Build, test, lint

```sh
git clone git@github.com:DevWolk/garminctl.git
cd garminctl
make            # bin/garminctl (Linux/WSL) and bin/garminctl.exe (Windows)
make check      # golangci-lint (govet included), go test -race, govulncheck
make lint-fix   # apply the auto-fixable findings and formatting
```

## Rules of the road

- Read `docs/formats.md` before touching `internal/fit`, `internal/ciq`,
  `internal/garmin` or the WPD bridge; it records what the watch actually
  does and which facts were verified on the device.
- Every file format has a round-trip test; a decoded file must re-encode
  byte for byte. Add a sample to the package's `testdata/` for any new file
  type. The samples come from the author's own watch and carry his profile
  data, as the README says; do not add files from anyone else's watch.
- `.golangci.yml` is the source of truth for the linters. Fix findings
  rather than silence them; a `//nolint` names its linter and gives the
  reason on the same line.
- Device settings never rewrite `GARMIN/Settings/Settings.fit`; they go
  through a settings-change file in `GARMIN/NewFiles`.
- Keep the dependency list short. Ask before adding a module.

## Pull requests

One change per pull request, with the user-visible effect in the first line
of the description. CI runs the tests on Linux and Windows, the linter,
govulncheck and CodeQL; all of them must pass.
