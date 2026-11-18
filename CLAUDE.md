# garminctl

Garmin watch manager with a native desktop app, CLI, web UI and MCP server.

- Read `docs/formats.md` before touching `internal/ship/fit`, `internal/ship/ciq`,
  `internal/containers` or the WPD bridge; it documents implemented formats and
  transport constraints.
- Native Linux USB is experimental and needs hardware validation. From WSL
  the bridge spawns
  `/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe`; use
  `-root DIR` for offline work; `DIR` must contain the copied `GARMIN/` tree.
- Never rewrite `GARMIN/Settings/Settings.fit` directly; device settings go
  through a type-41 settings-change file in `GARMIN/NewFiles` (see
  `settings.BuildChangeFile`). The watch applies NewFiles on USB disconnect.
- Preserve unknown fields and record order when editing binary formats.
  Keep codec round-trip tests passing and add fixtures for new formats.
- `make check` (module metadata, lint and govulncheck for Linux and Windows
  code, race tests, package-tool tests and UI tests) must pass before a commit. Fix findings rather than silence
  them; a `//nolint` names its linter and gives the reason.
- For Go work load the `cc-skills-golang:golang-how-to` skill first; it
  routes to the lint, testing, security and concurrency guidance this
  project follows.
- Keep documentation and UI text concise, current and device-neutral.
  Document supported behavior and constraints; keep local test reports out of
  user guides.
