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
