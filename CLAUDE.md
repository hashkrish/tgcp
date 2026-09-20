# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

TGCP is a terminal UI (Bubble Tea / Elm Architecture) for browsing and managing Google Cloud Platform resources — think K9s, but for GCP. It authenticates via Application Default Credentials and talks directly to GCP APIs; there is no backend server.

## Commands

```bash
go build -o tgcp ./cmd/tgcp   # build (Makefile: make build)
go run ./cmd/tgcp --debug     # run locally, logs to ~/.tgcp/debug.log
go run ./cmd/tgcp --demo      # run against embedded JSON fixtures, no GCP auth needed — fast UI iteration
go vet ./...
go test ./...
go test ./internal/services/gce/...          # single package
go test ./internal/services/gce/ -run TestX  # single test
golangci-lint run             # CI also runs this (enables unconvert, misspell on top of defaults)
```

The `--demo` flag is intentionally undocumented for end users; keep it out of README/docs, but always wire new services to support it (see below).

## Architecture

### The Elm loop and where state lives

`internal/ui/model.go` (`MainModel`) is the single root Bubble Tea model: `Update()` handles all top-level messages (window resize, global keybindings, project switching, toasts) and delegates to whichever `services.Service` is currently active; `internal/ui/home.go` composes the final view (sidebar + service panel + status bar, with overlays for the command palette/help/spinner layered on top in `View()`).

Each GCP service (GCE, GKE, Cloud SQL, ...) lives in its own package under `internal/services/<name>/` and implements the `services.Service` interface (`internal/services/interface.go`): `Name/ShortName`, `InitService/Reinit` (client setup, called again on project switch), `Update/View`, `HelpText`, `Refresh`, `Focus/Blur`, `Reset`, `IsRootView`. Services with their own sub-tabs (Cloud Run, Cloud Logging, ...) additionally implement `TabCycler` so a real Tab/Shift+Tab key isn't misinterpreted when a filter box or form field has focus. `MainModel` never touches a service's internals directly — everything crosses the `Service` interface, so a service is free to structure its own file (`<name>.go` for Update, `views.go` for View, `api.go` for the GCP client calls, `models.go`, `actions.go`) however it likes as long as that contract holds.

Services are registered lazily through `internal/core/registry.go`'s `ServiceRegistry`: a factory is registered once in `model.go`'s `registerAllServices()`, and the concrete service is only constructed (and its API client only created) the first time the user navigates to it. `internal/core/cache.go` gives every service a shared, TTL-based in-memory cache (`*core.Cache` passed into each factory) — services key their own cache entries and decide their own TTL (`r` forces a bypass via `Refresh()`).

### Adding a new service

A new service must be wired into **5 places** beyond its own package: the registry (`model.go`), the landing-page menu (`components/home_menu.go`), the sidebar (`components/sidebar.go`, plus `groupBreaks` if it starts a new category), and the category→color mapping (`components/category.go`). `internal/services/service_template.go.txt` is the starting-point skeleton. See `docs/DEVELOPER_GUIDE.md` for the full walkthrough — it's the source of truth for this, don't duplicate it here.

Every service should short-circuit its `api.go` client/`List*`/`Get*` calls when `demo.Enabled` is true (see `internal/demo/`), so `--demo` never hits a real API; fixture JSON goes in `internal/demo/data/`.

### UI conventions (see `docs/ui_patterns.md` and `docs/DEVELOPER_GUIDE.md` for full detail)

- Reuse the shared components in `internal/ui/components/` rather than hand-rolling: `StandardTable` for lists, `DetailCard`/`DetailList` for detail views (`DetailList` adds an arrow-key-selectable row with `y`-to-copy via OSC 52 clipboard), `Breadcrumb`, `RenderStatus`/`StatusSummary`, `EmptyState`, `RenderConfirmation`, `RenderFooterHint`.
- Always style through the semantic tokens in `internal/styles/styles.go` (`PrimaryBoxStyle`/`SecondaryBoxStyle`/`OverlayBoxStyle`, `HeaderStyle`/`SectionStyle`/`GroupStyle`, `ColorBrandPrimary`/`ColorSuccess`/`ColorError`/etc.) instead of raw Lipgloss styles or hex codes.
- Sidebar service icons must be plain Unicode symbols (geometric shapes/arrows/misc symbols), never emoji — emoji are fine in dashboard/content views (`overview/views.go`) since those aren't reflecting live GCP data.
- Action feedback (success/error toasts) goes through `core.ToastMsg` returned from a `tea.Cmd`; `MainModel.Update()` is the single place that turns this into a status-bar message (there's no floating toast overlay) — individual services just emit the message and don't need to know how it's displayed.
- Never block the Bubble Tea update loop: all GCP API calls are wrapped in a `tea.Cmd` goroutine that returns a result message.

## Commits

Do not add a `Co-Authored-By` line or any other AI-attribution footer to commit messages or PR descriptions in this repo.

## Feature-specific state

- **Job history**: every mutating action (create/update/delete/...) is recorded synchronously by `internal/core/jobtrack.go`/`jobs.go` at the point of mutation (not on message delivery), persisted to `~/.tgcp/jobs.json`, with retention governed by `~/.tgcprc`'s `jobs.max_count`/`jobs.max_age_days`.
- **Config**: `~/.tgcprc` (YAML) holds the default project, a fixed `projects:` list for the `Ctrl+g` quick-switcher, `ui.sidebar_visible`, and job-history retention — loaded once into `MainModel.Config` and read at runtime, not just at startup.
