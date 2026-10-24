# garminctl

Go tool that replaces Garmin Express for one user's vívoactive 5: CLI, local
web UI and MCP server over the watch's MTP file system.

- Read `docs/formats.md` before touching `internal/fit`, `internal/ciq`,
  `internal/garmin` or the WPD bridge; it records what the watch actually
  does and which facts were verified on the device.
- Live watch behavior has been verified through Windows; the new Linux libusb
  transport still needs hardware verification. From WSL the bridge spawns
  `/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe`; use
  `-root DIR` with a dump for offline work (a dump is a copy of `GARMIN/`).
- Never rewrite `GARMIN/Settings/Settings.fit` directly; device settings go
  through a type-41 settings-change file in `GARMIN/NewFiles` (see
  `garmin.BuildChangeFile`). The watch applies NewFiles on USB disconnect.
- `.SET` edits are immediate and the codec must round-trip byte for byte;
  `go test ./internal/ciq` guards that.
- Byte-exact tests exist for every format (`go test ./...`); keep them green
  and add a sample to `testdata/` for any new file type.
- `make check` (golangci-lint per `.golangci.yml` with govet inside,
  `go test -race`, govulncheck) must pass before a commit. Fix findings rather than silence
  them; a `//nolint` names its linter and gives the reason.
- For Go work load the `cc-skills-golang:golang-how-to` skill first; it
  routes to the lint, testing, security and concurrency guidance this
  project follows.
- Close Garmin Express before writing to the watch.
