# TUI Issues

All items below have been fixed. `go build ./...` and `go vet ./...` pass cleanly.

## Recurring bug classes

### A. Stale detail-view pointer after background refresh
Fix applied: after a background refresh reassigns the backing slice, the selected-item pointer is re-resolved by its stable identifier (`.Name`/`.ID`/etc.) in the new slice.

- [x] `internal/services/gce/gce.go:144-153`
- [x] `internal/services/gke/gke.go:186-193`
- [x] `internal/services/disks/disks.go:173-180`
- [x] `internal/services/cloudrun/run.go:228-249`
- [x] `internal/services/dataflow/dataflow.go:207-209`
- [x] `internal/services/cloudbuild/service.go:294-297`
- [x] `internal/services/iam/iam.go:160`
- [x] `internal/services/gcs/gcs.go:288-292`
- [x] `internal/services/pubsub/pubsub.go:252-262`
- [x] `internal/services/dataproc/dataproc.go:207-211`
- [x] `internal/services/cloudsql/cloudsql.go:156/217`
- [x] `internal/services/bigtable/bigtable.go:167/215`
- [x] `internal/services/firestore/firestore.go:201/259`
- [x] `internal/services/spanner/spanner.go:164/208`
- [x] `internal/services/redis/redis.go:165/209`

### B. Shared filter textinput leaks across tabs
Fix applied: the shared filter is cleared (`ExitFilterMode()`) on every tab/view transition, and table cursors are reset to 0 where applicable.

- [x] `internal/services/cloudrun/run.go:64-67, 302-311` (Services ↔ Functions)
- [x] `internal/services/pubsub/pubsub.go:234-249` (Topics ↔ Subscriptions) — cursor reset included
- [x] `internal/services/gcs/gcs.go:301-323` (bucket list ↔ object list)

### C. No-op confirmation dialogs
Scope note: implementing real snapshot/delete API calls was out of scope for a bug-fix pass (new mutating GCP API integration). Fixed by removing the misleading/dead confirmation UI instead.

- [x] `internal/services/disks/disks.go:253-266` — removed the "s:Snapshot" affordance and the whole dead `ViewConfirmation` flow
- [x] `internal/services/artifactregistry/service.go:78-80, 317-329, 468-487` — removed unreachable `ViewConfirmation`/`pendingAction`/`performActionCmd` dead code
- [x] `internal/services/cloudbuild/service.go:480-483` — same dead-code removal

### D. No immediate fetch on `Init()` — view opens empty
Fix applied: `Init()` now fires an immediate (cache-aware) fetch + spinner alongside scheduling the background tick, mirroring each file's existing `Refresh()` pattern.

- [x] `internal/services/cloudsql/cloudsql.go:125-127`
- [x] `internal/services/bigtable/bigtable.go:112-114`
- [x] `internal/services/spanner/spanner.go:110-112`
- [x] `internal/services/redis/redis.go:111-113`
- [x] `internal/services/firestore/firestore.go:142-144`
- [x] `internal/services/bigquery/bigquery.go:139-141`
- [x] `internal/services/artifactregistry/service.go:164-166`
- [x] `internal/services/secrets/secrets.go:138-140`

## One-off bugs

- [x] `internal/services/gke/gke.go:409-424` — `launchK9s` fallback now returns a proper tagged message (`k9sCredsMsg`) instead of illegally nesting a `tea.Cmd` inside a `tea.Msg`-returning callback; `Update()` runs the real `tea.ExecProcess` for k9s on success.
- [x] `internal/services/net/net.go:203-208` — Detail-view tick now re-fetches subnets/firewalls, not just reschedules itself.
- [x] `internal/services/cloudrun/run.go:335-345` vs `:131` — `l:Logs` now works on the Functions tab too (added a Cloud Functions log filter mirroring the Services one).
- [x] `internal/services/bigtable/bigtable.go:171-174` — cluster fetches are now tagged with the requesting instance ID and discarded if stale.
- [x] `internal/services/firestore/firestore.go:205-215` — namespace/kind fetches are now tagged and discarded if stale.
- [x] `internal/services/bigquery/bigquery.go:51` — removed the unused `filterInput` field and its import.
- [x] `internal/services/logging/logging.go:157-166` — background tick now only auto-refreshes when on page 1 and not viewing a detail entry; otherwise just reschedules.
- [x] `internal/services/logging/logging.go:161-197` — covered by the same guard above.
- [x] `internal/services/overview/overview.go` — `Refresh()` now clears `s.data.Error` so a later successful refresh recovers from the error screen.
- [x] `internal/services/overview/overview.go` — added `tea.WindowSizeMsg` handling; cards use a computed, clamped width instead of a hardcoded `80`.
- [x] `internal/services/cloudbuild/service.go:497` — `capitalize` was removed along with the dead confirmation code that was its only caller.

## Core UI framework (`internal/ui/`)

- [x] `internal/ui/model.go:667-679, 792-800` — the generic resize-forwarding path now subtracts sidebar width before forwarding, matching the other resize paths.
- [x] `internal/ui/model.go:277-286` — toggling the sidebar now forwards an adjusted `tea.WindowSizeMsg` to the active service.
- [x] `internal/ui/model.go:183-185` — toasts now carry a `CreatedAt` identity stamp so a stale dismiss timer can't clear a newer toast.
- [x] `internal/ui/components/statusbar.go:101-108` — long status messages are now truncated (rune-safe, with ellipsis) to fit the computed width.
- [x] `internal/ui/components/error.go:55-60` — guarded against the negative-index panic when width is very small (skips truncation instead of slicing with a negative bound).
- [x] `internal/ui/components/palette.go:63-84` — fuzzy-match highlighting now indexes by rune position instead of byte offset.

## tmux rendering corruption (separate investigation, fixed)

- [x] Service icons (⚙ ☸ ▷ ▤ ◔ ⛁ ⬡ ▦ ◇ ◲ ⊞ ⇢ ⎈ ⇌ ⚿ ✦ ⇄ ☰ ◈ ▣ ◉) are ambiguous-East-Asian-width Unicode — measured as 1 column by lipgloss but rendered as 2 columns by at least one of them inside tmux, permanently misaligning every row below as the list scrolls. Replaced with plain ASCII icons in `home_menu.go` and `sidebar.go`.
- [x] `internal/ui/home.go` — bare `"\n"` used as a `lipgloss.JoinVertical` gap silently doubled (splits into two empty lines), inflating landing-page height.
- [x] `internal/ui/components/home_menu.go` — box `.Height()` argument double-counted the border's own rows (lipgloss adds border on top of `.Height()`, not included in it).
- [x] `internal/ui/home.go` — landing page now adapts to small terminals (compact 1-line banner, drops hints/version if needed) instead of overflowing and relying on the renderer's crop-from-top fallback.
- [x] `internal/ui/model.go` — added `Ctrl+L` as a manual force-redraw key (standard terminal convention) as a recovery path for any future rendering desync.

---

# Enhancement Plan

Implemented via 6 parallel agents in one session (2026-08-26). All verified with `go build`/`go vet`/`go test`/`golangci-lint`, and several were runtime-tested against real GCP projects (read-only navigation only; the one intentional mutating test — disk snapshot code path — never got a live disk to act on, so nothing was created). One real bug from the parallel work (`internal/services/secrets/secrets.go`: `revealErrMsg` and `errMsg` were both `type X error`, two named interface types with identical method sets, so the first case in the type switch silently swallowed the other — fixed by making `revealErrMsg` a concrete struct).

## 1. Finish incomplete services

- [x] **Artifact Registry** (`internal/services/artifactregistry/`) — wired to `cloud.google.com/go/artifactregistry/apiv1`. The v1 API has no `"-"` wildcard aggregated-location list, so `ListRepositories` first calls `ListLocations` then fans out per-region (bounded concurrency via `errgroup`). Struct now has real fields (`Format`, `Mode`, `SizeBytes`, `Description`, `CreateTime`) matching `gcloud artifacts repositories list`. Runtime-verified: 2 real repositories listed, matched `gcloud` exactly. Delete-image action deliberately deferred (listed as still-open below).
- [x] **Cloud Build** (`internal/services/cloudbuild/`) — wired to `cloud.google.com/go/cloudbuild/apiv1/v2`. Lists most recent 100 builds with real fields (`Status`, `StatusDetail`, `TriggerID`, timestamps, computed `Duration`, `LogURL`, `Images`). Runtime-verified: 5 real builds listed, matched `gcloud builds list` exactly. Retry/cancel actions deliberately deferred (listed as still-open below).

**Still open**: delete-image action on Artifact Registry, retry/cancel actions on Cloud Build — both were explicitly scoped out of the read-only listing pass.

## 2. New GCP service coverage

- [x] **Cloud Scheduler** (`internal/services/scheduler/`) — `cloud.google.com/go/scheduler/apiv1`. Discovers regions via `ListLocations` (no hardcoded region list), fans out `ListJobs` per region. Shows schedule/target/state/next-run in list, full retry config in detail.
- [x] **Cloud Tasks** (`internal/services/cloudtasks/`) — `cloud.google.com/go/cloudtasks/apiv2`, same region-discovery pattern. Runtime-verified against a real project: listed a real queue (`webhook-delivery`) correctly. Task-count column deliberately omitted — the API doesn't return it without an N+1 per-queue call.

Read-only only (no pause/resume/delete/run-now) — deliberately scoped out.

**Still open, not attempted this round**: Cloud Functions (Gen2), Load Balancing, Cloud DNS, Instance Groups/MIGs, Cloud KMS, Filestore, Cloud Monitoring.

## 3. UX polish

- [x] **Disk snapshot creation** — implemented for real in `internal/services/disks/{api,disks,views}.go`: `s:Snapshot` → confirm dialog (same `ViewConfirmation`/`pendingAction` pattern as GCE start/stop) → `compute.disks.createSnapshot`, toast + list refresh on completion. The configured test project had Compute Engine API disabled, so this was verified two other ways instead: a full `go build`, and a temporary throwaway test that confirmed the call reaches a genuine `403 SERVICE_DISABLED` from the real API (proving the wiring is correct) rather than a compile/logic error. No snapshot was created.
- [x] **Audit `HelpText()` vs `?` help screen** — 4 parallel sub-agents covered all 20+ services. Result: no other instance of the "advertises a key that isn't wired up" bug class exists elsewhere (the cloudrun Functions-tab one from the bug-fix pass was the only one). Fixed a real gap in the *global* help overlay instead: `internal/ui/help.go` was missing `K` (k9s shell) and `[ ]` (tab switching), both real cross-service keybindings — added.
- [x] **Secret Manager: render version values** (added to plan mid-session, not originally listed) — `v` key in a secret version's detail view explicitly reveals the plaintext (never auto-fetched), toggle to hide again, value is never persisted or logged. Live-tested against a real secret.
- [x] **Parameter Manager** (added to plan mid-session) — new service, `google.golang.org/api/parametermanager/v1` (no dedicated `cloud.google.com/go` client module exists yet for this newer API). Mirrors the Secret Manager structure; values shown directly (non-secret by design, no reveal-gating). Runtime-verified: 47 real parameters listed and decoded correctly.

**Still open, not attempted this round**: extending mutating actions (start/stop/restart) to services beyond GCE and the new disk-snapshot action; detail-view richness pass against `gcloud describe` output; empty/loading-state consistency spot-check.

## 4. Engineering health

- [x] **CI test/lint workflow** — added `.github/workflows/test.yml` (build/vet/test job + golangci-lint job), runs on push/PR to main.
- [x] **Lint config** — added `.golangci.yml` (golangci-lint v2 schema, default linters + `unconvert`/`misspell`).
- [x] **Deprecated `lipgloss.Style.Copy()` calls** — all removed (plain assignment), across all 9 originally-listed files plus more found via the actual lint run.
- [x] **Lint cleanup** — 49 → 20 issues repo-wide in the agent's assigned scope (`internal/ui/`, `gke/views.go`, `net.go`, `overview/`) went to 0; remaining ~19 are in files other concurrent agents were actively editing (mostly `QF1003` tagged-switch style suggestions, a couple of genuine minor items like an uncapitalized-error-string lint and two `S1009` redundant nil-checks in `firestore/api.go`) — not yet swept.
- [x] **Test coverage for shared UI primitives** — added `internal/ui/components/{filter_test.go, table_test.go, home_menu_test.go}` covering `FilterSession`/`FilterModel`, `StandardTable` cursor-clamping, and `HomeMenuModel` cursor/scroll bounds under the exact edge cases (empty list, single item, filter-to-zero, rapid top/bottom scrolling) that hid this session's bugs.

**Still open**: the ~19 remaining lint issues in service files not covered by this pass (`bigquery.go`, `cloudrun/{api,run}.go`, `cloudsql.go`, `firestore/api.go`, `gce/{actions,gce}.go`, `gcs.go`, `secrets.go`, `core/{client,version}.go`, `utils/logger.go`); one full list+detail+filter *service* (as opposed to shared UI component) still has no test coverage to use as a template for others.

---

# Enhancement Plan — Round 2

Implemented via parallel agents (2026-08-27), scoped to 3 of the 4 areas per explicit choice — mutating actions/destructive-op safety stayed deliberately excluded (delete-image, retry/cancel, extending start/stop/restart to more services). This round's agents hit a mid-session usage-limit reset partway through; several were resumed/finished manually afterward. All verified with `go build`/`go vet`/`go test`/`golangci-lint` at the end — 0 lint issues, all tests pass.

## A. Remaining new GCP service coverage

- [x] **Cloud DNS** (`internal/services/dns/`) — managed zones list, detail view shows record sets.
- [x] **Cloud KMS** (`internal/services/kms/`) — key rings → keys, read-only. No crypto material ever fetched (list/get metadata calls only).
- [x] **Filestore** (`internal/services/filestore/`) — NFS instances: capacity, tier, network, state.
- [x] **Instance Groups / MIGs** — added as a new tab within the existing `gce` package (`[`/`]` to switch, mirroring the cloudrun/net tab pattern) rather than a separate service, using the same `compute/v1` client already in use there. Shows target size, instance template, autoscaling on/off, and a Stable/Updating status derived from `Status.IsStable` — no per-group "current size" field, since that needs an extra `instanceGroups.get` call per group (N+1 pattern deliberately avoided, matching scheduler/cloudtasks precedent). Verified via direct Go-level render tests (not full tmux, see note below): both tabs render correctly with real data shapes, tab-switching works, and the pre-existing Instances tab behavior/tests are unchanged.
- [x] **Cloud Functions (Gen1+Gen2)** (`internal/services/cloudfunctions/`) — distinct from the Cloud Run Functions tab.
- [x] **Load Balancing** (`internal/services/loadbalancing/`) — MVP scope: Backend Services + Health Checks tabs. URL maps, forwarding rules, and SSL certs deliberately left out of this pass.
- [x] **Cloud Monitoring** (`internal/services/monitoring/`) — Uptime Checks + Alert Policies. Notification channel contact details are never rendered, only a count.

All read-only, no mutating actions. Two services (`dns`, `kms`) and Cloud Functions/Load Balancing were built by agents that got cut off mid-session by a usage-limit reset — their packages were already complete and building cleanly on resume, but the registration wiring (registry, sidebar, home menu, category color mapping) hadn't been done yet for `dns`/`kms`/`functions`/`loadbalancing`; that wiring was finished manually afterward. All 30 services now registered with zero name/icon collisions (verified by script, not just by eye).

**Note on verification**: a live tmux run showed a blank content pane when navigating into *any* service view (reproduced identically on `disks`, an untouched service, ruling out a regression from this round's changes). Direct Go-level tests of `MainModel`/service `View()` output confirm the actual rendering logic is correct; the blank pane is most likely an artifact specific to that headless tmux capture session. Flagged here rather than silently assumed fixed — worth a real terminal check.

## B. Detail-view & UX consistency pass

- [x] Added missing-but-already-fetched fields to detail views across the services that had gaps: `cloudsql` (`api.go`/`models.go`/`views.go` — new fields plus their fetch), `dataflow`, `dataproc`, `gke`, `iam`, `logging`, `pubsub`, `disks` (`views.go`, small additions).
- [x] Empty/loading-state consistency spot-check — folded into the same pass.

## C. Finish engineering health

- [x] All 19 previously-remaining lint issues fixed (`core/client.go`, `core/version.go`, `utils/logger.go`, `cloudrun/{api,run}.go`, `cloudsql.go`, `firestore/api.go`, `gce/{actions,gce}.go`, `gcs.go`) — `golangci-lint run ./...` now reports **0 issues** repo-wide.
- [x] **Test coverage for `disks` as a template service** — `internal/services/disks/disks_test.go`: filter logic, cursor-bounds safety (empty list, out-of-range cursor — including one test whose original premise was wrong, since `bubbles/table`'s `SetCursor` already self-clamps; fixed to assert the actual correct clamping behavior instead of a scenario the library doesn't allow), confirmation-flow view-state transitions driven through real `Update()` calls, and `Reset()` clearing state.

**Still open**: nothing from this round's explicit scope. Round 1's still-open items (delete-image/retry-cancel actions, extending mutating actions beyond GCE/disks) remain untouched by choice.

