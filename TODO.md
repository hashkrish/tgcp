# TODO

## Open

### High-value gaps (from gcloud CLI parity audit, see reference table below)
- [x] Create operations — implemented for 25 of ~26 services via a new shared `internal/ui/components/form.go` input-form component (2026-08-27 agent pass). Confirm-dialog pattern, never live-tested against a real project (per explicit scope). Still not done: MIGs, Cloud Functions deploy, Cloud Run jobs, VPC subnet create, full Load Balancing resource set (only Health Checks), Cloud Monitoring alert-policy create (only Uptime Checks), Cloud Scheduler pubsub/app-engine job variants (only HTTP), BigQuery table create (only dataset); Cloud Logging has no Create concept and is intentionally excluded — see reference table for exact per-service gaps.
- [x] IAM policy writes — `add-iam-policy-binding` implemented for 5 services with the simplest API shapes (2026-08-28 Round 8, see below): GCS buckets, Pub/Sub topics, Secret Manager secrets, KMS key rings, Artifact Registry repositories. `remove-iam-policy-binding` and raw `set-iam-policy` deliberately out of scope everywhere (lockout-risk reasons, see Round 8 note); every other service's `[ ] IAM` row stays unimplemented — see the reference table for the full per-service list of what's done vs. skipped and why.
- [x] Delete operations — implemented across every service where GCP itself supports it (2026-08-28 Round 6, see below). Cloud Logging and Cloud Build have no Delete concept at all and are intentionally excluded, matching their Create/Update treatment; KMS key rings genuinely cannot be deleted in GCP. Type-to-confirm (typing the resource name) was not built — see the note in Round 6 below for the actual safety pattern used and a call-out that type-to-confirm would be a good hardening follow-up.
- [x] Update operations — implemented for 20 of ~26 services with an Update row (2026-08-28 agent pass, see Round 5 below). Remaining `[ ]` Update rows are deliberately skipped with an inline reason in the reference table: Cloud Functions (needs a real source bundle, same blocker as Create), Dataflow (`update-options` needs per-transform key/value overrides), Cloud DNS (record-level changes need the transaction add/remove flow), Load Balancing (every meaningful update is a cross-resource reference edit), Cloud Logging (no Update concept in this app), Cloud Build (builds are immutable).
- [x] Lifecycle operations — the simplest, most common single-call actions implemented for 10 services (2026-08-28 Round 7, see below): GCE VM reset/suspend/resume (on top of Round 3's start/stop), Cloud Tasks pause/resume/purge, Cloud Scheduler pause/resume/run, Redis failover, Dataflow cancel/drain, Dataproc start/stop, IAM Service Account disable/enable, Secret Manager version enable/disable/destroy, Pub/Sub detach-subscription, Cloud Functions call (Gen1 only). Remaining `[ ]` Lifecycle rows are deliberately skipped with an inline reason in the reference table — mostly multi-step orchestration (GKE upgrade, Cloud SQL failover/promote/clone/PITR/switchover, Firestore clone/restore, Cloud Run jobs/deploy), operations needing a sub-resource this app doesn't model (MIG per-instance ops, KMS key versions, Load Balancing get-health/invalidate-cdn-cache, Filestore revert/replica ops, Disks async-replication), or narrow/rare/unstable-API operations (Spanner change-quorum, Cloud Monitoring policy migrate, GCE perform-maintenance).
- [x] Data-plane operations — implemented for 6 services with the simplest/safest wins (2026-08-28 Round 9, see below): GCS object delete+download, Secret Manager add-version, Pub/Sub publish+pull-without-ack, KMS get-public-key, Bigtable table list, Cloud SQL execute-sql (read-only SELECT/SHOW/EXPLAIN/DESCRIBE only, enforced client-side). Every mutating op (GCS delete, Pub/Sub publish, Secret Manager add-version) goes through the existing confirm-dialog pattern; read-only ops (GCS download, Pub/Sub pull, KMS public-key, Bigtable tables, Cloud SQL query) skip it. Spanner execute-sql, Firestore export/import/bulk-delete, Redis export/import/get-auth-string, Dataproc jobs, Cloud Tasks task-level ops, Parameter Manager versions, Cloud DNS record-set transactions, Cloud Logging sinks/metrics/buckets/views, Cloud Build log streaming, and Artifact Registry docker tags were all considered and explicitly skipped — see the reference table for the per-service reasons (mostly: needs a scrolling/streaming UI surface, a multi-step batch editor, or a sub-resource this app doesn't model).

### Follow-ups from Round 2/3
- [ ] Live-terminal verification of Round 3 mutating actions against a real project (IAM role-binding fetch, Cloud SQL restart, Cloud Build retry/cancel, Artifact Registry delete image) — only verified via build/vet/test, not a live run
- [ ] Investigate blank content pane seen in a live tmux run when navigating into *any* service view (Round 2 note) — reproduced identically on `disks` (untouched that round), so likely a tmux/headless-capture artifact rather than a regression, but never confirmed in a real terminal
- [ ] Extend start/stop/restart-style mutating actions beyond what Round 3 added (Cloud SQL restart, Cloud Build retry/cancel, Artifact Registry delete image) — GKE, Cloud Run, and most other services still have none

## Completed

### TUI bug-fix pass
All items fixed; `go build ./...` and `go vet ./...` pass cleanly.

- [x] **Stale detail-view pointer after background refresh** — selected-item pointer re-resolved by stable identifier (`.Name`/`.ID`) in the new slice after a background refresh reassigns the backing slice. Applied across `gce`, `gke`, `disks`, `cloudrun`, `dataflow`, `cloudbuild`, `iam`, `gcs`, `pubsub`, `dataproc`, `cloudsql`, `bigtable`, `firestore`, `spanner`, `redis`.
- [x] **Shared filter textinput leaking across tabs** — filter cleared (`ExitFilterMode()`) and table cursor reset to 0 on every tab/view transition, in `cloudrun` (Services ↔ Functions), `pubsub` (Topics ↔ Subscriptions), `gcs` (bucket list ↔ object list).
- [x] **No-op confirmation dialogs** — removed dead/misleading `ViewConfirmation` UI in `disks` (`s:Snapshot`), `artifactregistry`, `cloudbuild` rather than wiring up unimplemented mutating calls.
- [x] **No immediate fetch on `Init()`** — `Init()` now fires an immediate cache-aware fetch + spinner alongside the background tick, in `cloudsql`, `bigtable`, `spanner`, `redis`, `firestore`, `bigquery`, `artifactregistry`, `secrets`.
- [x] **One-off bugs** — `gke` k9s fallback message-nesting bug; `net` detail-view tick not re-fetching; `cloudrun` `l:Logs` missing on Functions tab; `bigtable`/`firestore` stale fetches not tagged/discarded; `bigquery` unused `filterInput` field; `logging` background tick refreshing during detail view/off page 1; `overview` error state not clearing on successful refresh and missing resize handling; `cloudbuild` dead `capitalize` helper.
- [x] **Core UI framework (`internal/ui/`)** — resize forwarding not accounting for sidebar width; sidebar toggle not forwarding resize; toast dismiss timers lacking identity stamps; status bar truncation not rune-safe; error component negative-index panic on very small widths; palette fuzzy-match highlighting indexing by byte offset instead of rune position.
- [x] **tmux rendering corruption** — ambiguous-width Unicode service icons replaced with ASCII; a bare `"\n"` gap bug and a double-counted border height bug in the landing page; landing page now adapts to small terminals; added `Ctrl+L` as a manual force-redraw key.

### Enhancement Plan — Round 1 (2026-08-26)
Implemented via 6 parallel agents. Verified with `go build`/`go vet`/`go test`/`golangci-lint`; several runtime-tested against real GCP projects (read-only).

- [x] **Finish incomplete services** — Artifact Registry (`cloud.google.com/go/artifactregistry/apiv1`, per-region fan-out since there's no `-` wildcard), Cloud Build (`cloud.google.com/go/cloudbuild/apiv1/v2`, last 100 builds with real fields).
- [x] **New GCP service coverage** — Cloud Scheduler, Cloud Tasks (both region-discovered via `ListLocations`, no hardcoded region list). Read-only.
- [x] **UX polish** — Disk snapshot creation wired for real (`compute.disks.createSnapshot`); audited `HelpText()` vs the `?` help screen across all services and fixed the one real gap (missing `K`/`[ ]` in the global help overlay); Secret Manager version-value reveal (`v` key, never auto-fetched); new Parameter Manager service.
- [x] **Engineering health** — added `.github/workflows/test.yml` and `.golangci.yml`; removed all deprecated `lipgloss.Style.Copy()` calls; lint issues in `internal/ui/`, `gke/views.go`, `net.go`, `overview/` went to 0; added test coverage for shared UI primitives (`filter_test.go`, `table_test.go`, `home_menu_test.go`).

### Enhancement Plan — Round 2 (2026-08-27)
Scoped to 3 of 4 areas by explicit choice — mutating actions/destructive-op safety stayed excluded. Verified with `go build`/`go vet`/`go test`/`golangci-lint` — 0 lint issues, all tests pass.

- [x] **Remaining new GCP service coverage** — Cloud DNS, Cloud KMS (metadata only, no crypto material fetched), Filestore, Instance Groups/MIGs (new tab in `gce`), Cloud Functions (Gen1+Gen2, distinct from the Cloud Run Functions tab), Load Balancing (Backend Services + Health Checks MVP), Cloud Monitoring (Uptime Checks + Alert Policies, notification channel contact details never rendered). All 30 services registered with zero name/icon collisions.
- [x] **Detail-view & UX consistency pass** — added missing-but-already-fetched fields to detail views in `cloudsql`, `dataflow`, `dataproc`, `gke`, `iam`, `logging`, `pubsub`, `disks`; empty/loading-state spot-check folded into the same pass.
- [x] **Finish engineering health** — all 19 remaining lint issues fixed; `golangci-lint run ./...` reports 0 issues repo-wide. Added `disks_test.go` as a template for full list+detail+filter service test coverage.

### Enhancement Plan — Round 3 (2026-08-27)
Implemented per an approved plan; closes the three items Rounds 1/2 had explicitly deferred. Verified with `go build`/`go vet`/`go test`/`golangci-lint` after each area — 0 lint issues, all tests pass. Not live-tested against real GCP resources.

- [x] **IAM role bindings** — one project-wide `Projects.GetIamPolicy` call (cached 5min), flattened into `PolicyMember`, shown per selected service account. Fetch failures degrade silently.
- [x] **Load Balancing MVP expansion** — grew from 2 to 5 tabs (added URL Maps, Forwarding Rules, SSL Certificates); `[`/`]` now a real cyclic prev/next. SSL private-key material never fetched.
- [x] **Cloud SQL: restart** (`t` key) — added onto the existing start/stop confirm-dialog scaffolding.
- [x] **Cloud Build: retry/cancel** (`t`/`c` keys) — new confirm-dialog state machine, plus `retry`/`cancel` verb styling added to the shared confirmation component.
- [x] **Artifact Registry: delete image** (`i` to drill into images, `d` to delete) — new repository→images drill-down, `DeleteVersion(Force: true)` fire-and-forget.

**Recovery note (2026-08-28)**: all five items above were found missing from the working tree partway through Round 6 (a `git checkout`-style operation during an earlier agent's internal stash handling had silently reverted `cloudsql`, `cloudbuild`, `iam`, `loadbalancing`, and `artifactregistry` past this round, back toward their Round-2 state, while Rounds 4/5 were built on top of that regressed base). The code was intact in a leftover `git stash` entry and was restored via a 3-way merge (`git merge-file`, ancestor `c56d652`, ours = the Round 4/5 working tree, theirs = the stash) across `cloudsql/{api,cloudsql}.go`, `cloudbuild/{api,service}.go`, `iam/{api,iam}.go`, `loadbalancing/{api,loadbalancing,models}.go`, and `artifactregistry/{api,service}.go`. All conflicts were "keep both" interleaved-addition cases (two rounds independently adding code at the same point in a file) plus a few pre-existing duplicate-declaration bugs (an accidentally doubled `actionResultMsg` type in three files, a doubled `HealthCheckCreateOpts`, a doubled `createForm` struct field, a doubled `CreateHealthCheck`/`renderConfirmation` function) that were cleaned up in the process. Re-verified with `go build`/`go vet`/`go test`/`golangci-lint` (0 issues) and confirmed each recovered feature (Cloud SQL restart, Cloud Build retry/cancel, IAM policy-binding fetch, Load Balancing's 5-tab cycling, Artifact Registry image drill-down/delete) is reachable from a keybinding, not dead code.

### Enhancement Plan — Round 4: Create operations (2026-08-27)
Implemented by a single agent pass covering the Create category only (Update/Delete/Lifecycle/IAM/Data-plane deferred to later rounds to avoid concurrent edits to the same service files). New shared `internal/ui/components/form.go` sequential-field input form component, reused across all 24 services below rather than one-off per-service forms. Confirm-dialog-before-submit pattern matching the existing mutating-action UX. Verified with `go build`/`go vet`/`go test`/`golangci-lint` (0 issues) — never live-tested against a real GCP project.

- [x] Create implemented (minimal viable fields, not full API surface) for: VM Instances, Disks, GKE, Cloud Run (services), Cloud SQL, GCS (buckets), BigQuery (datasets), Bigtable (instances), Firestore, Spanner (instances), Redis, Dataflow (run job from template), Dataproc, Pub/Sub (topics + subscriptions), Cloud Tasks, Cloud Scheduler (HTTP jobs), IAM Service Accounts, Secret Manager, Parameter Manager, KMS (key rings + crypto keys), Cloud DNS (zones), VPC Firewalls, Load Balancing (health checks), Filestore, Cloud Monitoring (uptime checks), Cloud Build (submit), Artifact Registry.

**Still open**: MIGs (needs an instance-template reference, deferred), Cloud Functions deploy (needs a real source bundle/GCS upload — a metadata-only create would submit a request guaranteed to fail, so deliberately skipped rather than wiring a broken call), Cloud Run jobs, VPC networks/subnets, Load Balancing's other 4 resource types (backend services/url maps/forwarding rules/ssl certs — real inter-resource dependencies that don't fit a flat form), Cloud Monitoring alert policies, Cloud Scheduler pubsub/app-engine variants, BigQuery tables, KMS key-version operations. Cloud Logging has no Create concept in this app and is intentionally excluded. See the reference table for the full per-service gap list.

### Enhancement Plan — Round 5: Update operations (2026-08-28)
Implemented by a single agent pass covering the Update category only, continuing from a prior attempt that was killed mid-flight by a rate limit (Cloud Run's `UpdateServiceImage` and the orphaned Redis/Cloud Scheduler API methods were already in place from that attempt — this pass finished wiring Redis/Scheduler and did the remaining ~20 services). Reused the `internal/ui/components/form.go` component from Round 4, pre-populated with each resource's current values, following the exact pattern already established by `cloudrun`'s `u` keybinding (Get-then-mutate-then-PATCH/Update, or a labels-only/field-only Patch with an explicit `UpdateMask`). Per this round's scope guidance, only the single most common/simple field was wired per service (a resize, a labels/description patch, a single-field toggle) rather than the full `gcloud update` flag surface; multi-step or cross-resource updates were skipped and documented inline in the reference table below. Verified with `go build`/`go vet`/`go test`/`golangci-lint` after every 2-3 service batch (0 issues throughout) — never live-tested against a real GCP project, per the explicit safety constraint against invoking live Update calls.

- [x] **Finished from the killed prior attempt**: Redis (`UpdateInstanceMemorySize`, `u` key), Cloud Scheduler (`UpdateJobSchedule`, `u` key) — both API methods already existed as dead code; this pass added the keybinding/form/toast/refresh wiring.
- [x] **New Update implementations** (minimal single-field, `u` key, pre-populated form) for: GCE VM Instances (network tags), GCE MIGs (target-size resize), Disks (grow-only resize), GKE (first-node-pool resize), Cloud SQL (machine tier patch), Bigtable (first-cluster node-count resize), Spanner (node count), GCS (default storage class), BigQuery (dataset description), Pub/Sub (subscription ack deadline), Cloud Tasks (max dispatch rate), IAM Service Accounts (display name), Secret Manager (labels), KMS (crypto key rotation schedule), Cloud DNS is skipped (see below) but VPC Firewalls (rule priority), Cloud Monitoring (uptime check interval), Filestore (first file-share capacity resize), Firestore (delete-protection toggle), Artifact Registry (repository description), Parameter Manager (labels).
- [x] **Deliberately skipped, documented inline in the reference table**: Cloud Functions (needs a real source bundle, same blocker as Create), Dataflow (`update-options` needs per-transform key/value overrides that don't fit a flat form), Cloud DNS (record-level changes need the transaction add/remove flow), Load Balancing (every meaningful update is a cross-resource reference edit across its other 4 resource types), Cloud Logging (no Update concept in this app, same as Create), Cloud Build (builds are immutable — no `gcloud builds update`).

### Enhancement Plan — Round 6: Delete operations (2026-08-28)
Implemented by a single agent pass covering the Delete category only, in sequential batches of 3-4 services (checked into TODO.md and build/vet-verified after each batch to survive a mid-session cutoff). Every delete goes through the shared `internal/ui/components/confirmation.go` dialog (`ViewConfirmation`/`pendingAction` state machine, matching the `disks`/`artifactregistry` pattern) — no delete fires without an explicit `y` confirm keypress. `confirmation.go` already had "delete" verb styling (red border, "This action cannot be undone" warning) from a prior round, so no changes were needed there. Never live-tested against a real GCP project, per the explicit safety constraint against invoking live Delete calls — verified only via `go build`/`go vet`/`go test`/`golangci-lint` (0 issues throughout).

**Safety pattern used instead of type-to-confirm**: `confirmation.go` has no built-in support for "type the resource name to confirm", and building it was out of scope for this pass. For the handful of especially destructive deletes — a whole GKE cluster, a Cloud SQL instance, an IAM service account — this pass instead uses a **double confirmation**: the first `y` escalates `pendingAction` to a `-confirm2` state that re-renders the dialog with a "FINAL WARNING" message spelling out exactly what will be destroyed, and only a second `y` fires the actual delete. This is noted here as a real gap: **type-to-confirm (typing the resource name) would be a meaningfully stronger hardening follow-up** for these specific resources.

- [x] **Delete implemented** (`d` key, confirm dialog, sequential batches) for: VM Instances, MIGs, Disks, GKE (double confirm), Cloud Run services, Cloud SQL instances (double confirm), Cloud Functions, GCS buckets (empty-only, client-side `IsBucketEmpty` check before the call), BigQuery datasets (empty-only, API itself refuses non-empty), Bigtable instances, Firestore databases, Spanner instances, Redis instances, Dataflow jobs (archive — sets the `archived` label via a labels-only Update, since Dataflow has no true delete), Dataproc clusters, Pub/Sub topics + subscriptions, Cloud Tasks queues, Cloud Scheduler jobs, IAM Service Accounts (double confirm), Secret Manager secrets, Parameter Manager parameters, KMS crypto keys (keyrings excluded — see below), Cloud DNS zones (empty-only, API itself refuses zones with records beyond the default NS/SOA), VPC Firewall rules, Load Balancing health checks + backend services (both global and regional), Filestore instances, Cloud Monitoring uptime checks + alert policies, Artifact Registry repositories.
- [x] **Corrected two stale table entries found during this pass**: Firestore databases *are* deletable via the Admin API (`projects.databases.delete`) — the table previously said otherwise; and KMS CryptoKeys *can* be deleted via `DeleteCryptoKey`, though only after every CryptoKeyVersion under the key has already been destroyed first (this app doesn't implement key-version operations, so this will fail for keys with active versions — documented in the confirmation dialog itself, not just here). KMS key rings genuinely cannot be deleted in GCP; no keyring-delete was built.
- [x] **Also found and fixed a discrepancy**: this table previously marked Artifact Registry "delete image" as done, but no such implementation (drill-down into images, `DeleteVersion` call, etc.) actually existed anywhere in `internal/services/artifactregistry/` — only repository-level delete was added this round. Image/package/version-level delete remains open for a future pass.
- [x] **Deliberately excluded — no Delete concept in GCP or this app**: Cloud Logging (no way to delete individual log entries), Cloud Build (builds are an immutable history, no `gcloud builds delete`).
- [x] **Missed in the initial batch, added in a follow-up sweep**: Disks (`DeleteDisk`, `d` key) — the Compute API refuses to delete a disk still attached to an instance.

### Enhancement Plan — Round 7: Lifecycle operations (2026-08-28)
Implemented by a single agent pass covering the Lifecycle category only, in sequential batches of 3-4 services (checked into TODO.md and build/vet-verified after each batch to survive a mid-session cutoff, per the same convention as Round 6). Per the task's scope guidance, only the simplest, most common, single-call lifecycle actions were wired per service — multi-step orchestration and operations needing a sub-resource this app doesn't model were left out and documented inline in the reference table below, alongside the existing GCE start/stop, Disks snapshot, Cloud SQL start/stop/restart, and Cloud Build retry/cancel from earlier rounds (verified still present and untouched). Every action goes through the shared `internal/ui/components/confirmation.go` dialog, extended with new verb styling (`cancel`/`drain`/`disable`/`pause`/`detach`/`suspend` grouped with the existing disruptive/orange style; `resume`/`enable`/`failover`/`call`/`run` grouped with the existing safe/blue style; `purge` given its own red/destructive style) rather than reusing a mismatched existing verb. Never invoked live against a real GCP project, per the explicit safety constraint — verified only via `go build`/`go vet`/`go test`/`golangci-lint` (0 issues throughout, all existing tests still pass).

- [x] **GCE VM Instances**: reset/suspend/resume (`R`/`z`/`Z` keys, list and detail views) alongside the existing start/stop. `perform-maintenance` skipped (sole-tenant-node-only, rarely applicable).
- [x] **Cloud Tasks**: pause/resume/purge (`p`/`R`/`x` keys from queue detail view).
- [x] **Cloud Scheduler**: pause/resume/run (`p`/`R`/`x` keys from job detail view).
- [x] **Redis/Memorystore**: failover (`f` key from instance detail view), always using `LIMITED_DATA_LOSS` rather than `FORCE_DATA_LOSS` to avoid an unnecessary data-loss risk. `reschedule-maintenance` skipped (narrow scheduling-window operation).
- [x] **Dataflow**: cancel/drain (`c`/`x` keys from job detail view) via `Jobs.Update` with `requestedState`, alongside the existing archive-based Delete.
- [x] **Dataproc**: start/stop (`s`/`x` keys from cluster detail view). `diagnose` skipped (produces an async diagnostic tarball with no sensible UI to surface the result).
- [x] **IAM Service Accounts**: disable/enable (`E`/`D` keys from account detail view, staying on the detail view after completion rather than dropping back to the list, unlike delete). `undelete` skipped (needs the deleted account's unique ID within a 30-day window, which this app doesn't track since it doesn't list deleted accounts).
- [x] **Secret Manager**: version enable/disable/destroy (`E`/`D`/`X` keys from the version detail view added in an earlier round) — the first version-level mutation in this package; the version list is refreshed in place after each action so the State column updates without leaving the version detail view.
- [x] **Pub/Sub**: detach-subscription (`x` key from subscription detail view).
- [x] **Cloud Functions**: call (`c` key from function detail view opens a one-field JSON-data form, then a confirm dialog before invoking) — Gen1 only. This required adding the `google.golang.org/api/cloudfunctions/v1` client alongside the existing v2 client used for everything else, since v2 has no `call` RPC (Gen2 functions are Cloud Run services invoked over HTTPS, which this app has no trigger-URL/auth flow for); Gen2 calls are rejected client-side with a clear error before any API call is made.
- [x] **Verified untouched from earlier rounds**: GCE start/stop, Disks snapshot, Cloud SQL start/stop/restart, Cloud Build retry/cancel — all still present, reachable, and passing build/vet/test/lint.
- [x] **Explicitly out of scope for this pass, documented inline in the reference table**: GKE upgrade/complete-control-plane-upgrade, Cloud SQL failover/promote-replica/clone/point-in-time-restore/switchover, Cloud Run execute(jobs)/deploy, Firestore clone/restore — all multi-step orchestration named as out of scope in the task brief. MIGs resize/start-stop-instances/rolling-action, KMS key-version operations, Load Balancing get-health/invalidate-cdn-cache, Filestore revert/replica ops, Disks start/stop-async-replication — all need a sub-resource or reference this app's data model doesn't track. Spanner change-quorum, Cloud Monitoring policy migrate, GCE perform-maintenance — narrow/rare operations or unstable (beta-only) API surface, low value for this pass.

### Enhancement Plan — Round 8: IAM policy writes (2026-08-28)

Implemented by a single agent pass covering the IAM category only, in sequential batches of 1-2 services (checked into TODO.md and build/vet-verified after each batch, per the same convention as Rounds 6/7). This is the highest-risk mutating category in the app — a `setIamPolicy` call with an incomplete policy body can silently revoke unrelated permissions and lock someone out of a resource — so this pass scoped itself deliberately narrow and safety-first:

- **Only `add-iam-policy-binding` was implemented, never a raw `set-iam-policy` or `remove-iam-policy-binding`.** Every write is a get-current-policy → merge-one-binding-in → set-the-merged-policy round trip, so no existing binding is ever dropped. For GCS/KMS this uses the client library's own `iam.Policy.Add` (from `cloud.google.com/go/iam`, shared by both the GCS `storage.BucketHandle.IAM()` handle and the KMS `KeyManagementClient.ResourceIAM()` handle); for Pub/Sub, Secret Manager, and Artifact Registry (whose Go clients expose the raw `GetIamPolicy`/`SetIamPolicy` RPCs instead of that wrapper) this pass added a local `mergeIAMBinding` helper per package that appends the member to an existing role's binding or appends a new binding, preserving every other binding and the policy's `etag` untouched.
- **A read step was added everywhere first.** None of these five services had any existing IAM display, so each got a new `ViewIAM` state (`RenderIAMBindings`, a new shared component) showing every current role → members binding before the `a` (Add Binding) key is even reachable — mirroring the "look before you grant" pattern named in the task brief, and closest in spirit to `internal/services/iam`'s existing project-level `GetProjectPolicyBindings` read.
- **Every grant goes through the existing confirm-dialog component**, extended with a new `"grant"` verb style in `internal/ui/components/confirmation.go` (distinct warning-colored styling, not reused from delete/disable) and a new `components.IAMConfirmMessage` helper that spells out the exact role, member, and resource name in the confirmation text before the user can press `y`.
- **New shared component**: `internal/ui/components/iam.go` (`IAMBindingRow`, `RenderIAMBindings`, `NewIAMAddBindingForm`, `IAMConfirmMessage`) — the role+member add-binding form reuses the existing `internal/ui/components/form.go` two-field pattern, matching `gcloud ... add-iam-policy-binding --role=... --member=...`.
- [x] **GCS**: bucket-level (`i` key from bucket detail view → `ViewIAM`; `a` to add). Uses `Bucket.IAM().Policy()`/`SetPolicy()`.
- [x] **Pub/Sub**: topic-level only, not subscriptions (`i` key from topic detail view). Uses `Projects.Topics.GetIamPolicy`/`SetIamPolicy` (REST client).
- [x] **Secret Manager**: secret-level (`i` key from secret detail view). Uses `Projects.Secrets.GetIamPolicy`/`SetIamPolicy` (REST client).
- [x] **KMS**: key-ring-level only, not individual crypto keys (`i` key from the key rings list, applied to the cursor-selected ring). Uses `KeyManagementClient.ResourceIAM(...)` (the non-deprecated replacement for the now-deprecated `KeyRingIAM` helper, caught by `golangci-lint`'s staticcheck).
- [x] **Artifact Registry**: repository-level (`g` key from repository detail view — `i` was already taken by the existing Images drill-down keybinding). Uses `Client.GetIamPolicy`/`SetIamPolicy` (gRPC client); also required switching from the deprecated `google.golang.org/genproto/googleapis/iam/v1` package to its replacement `cloud.google.com/go/iam/apiv1/iampb`, again flagged by staticcheck.
- [ ] **Explicitly out of scope for this pass, per the task's safety guidance**: every other service with a `[ ] IAM` row in the reference table below — VM Instances, MIGs, Disks, Cloud Run, Cloud SQL, Cloud Functions, Bigtable, Spanner, Cloud Tasks, IAM Service Accounts (policy write, not the existing project-level read), KMS crypto-key-level, Cloud DNS zones, VPC subnets, Load Balancing backend-services, Cloud Logging views, Cloud Build connections, GKE (gcloud itself has no cluster-level binding, project-level only) — either need a resource type/client this app doesn't have, or were simply left for a future pass per the "4-5 services, not all of them" scope guidance. `remove-iam-policy-binding` and raw `set-iam-policy` remain unimplemented for all five services above too.
- **Verification**: `go build ./...`, `go vet ./...`, `go test ./...`, and `golangci-lint run ./...` all pass with 0 issues after every batch and again at the end. Per the explicit safety constraint for this category, no IAM write was ever invoked against a real GCP project — this is unverified against live IAM behavior, same as every other mutating category in this app.

### Enhancement Plan — Round 9: Data-plane operations (2026-08-28)

Implemented by parallel agent batches (2-3 services per batch, build/vet/test-verified after each batch), covering the final remaining category — operations on the data *inside* a resource rather than the resource itself. This is the highest-risk category to test since several of these ops genuinely mutate or expose live data (a publish sends a real message, a GCS delete removes real object data), so per the task's explicit safety constraint **no data-plane call was ever invoked against a real GCP project** — verification was `go build`/`go vet`/`go test` only, never a live run. Mutating ops (GCS object delete, Pub/Sub publish, Secret Manager add-version) go through the existing confirm-dialog pattern with the exact target/content shown before confirming; read-only ops (GCS download, Pub/Sub pull-without-ack, KMS get-public-key, Bigtable table list, Cloud SQL query) skip the dialog per the task's scope guidance.

- [x] **GCS**: object delete (`d`, confirm dialog) and object download (`c`, to `~/Downloads/<basename>`, no confirm — non-destructive) from the existing bucket→object list drill-down. `rsync`/`mv`/`sign-url` skipped (batch ops and URL-signing don't fit a single-object pass).
- [x] **Secret Manager**: add new secret version (`n` from secret detail view, one-field form for the plaintext value, confirm dialog, `Projects.Secrets.AddVersion`, version list refreshes in place).
- [x] **Pub/Sub**: publish (`p` from topic detail, one-field message-body form, confirm dialog shows exact text + topic, toast returns the message ID) and pull-without-ack (`P` from subscription detail, immediate `Subscriptions.Pull` with `ReturnImmediately: true`, maxMessages=10, Acknowledge never called — true no-ack, no confirm needed since nothing is mutated). `ack`/`modify-message-ack-deadline`/`seek` skipped — no natural UI for picking a specific pulled message to act on yet.
- [x] **KMS**: get-public-key (`k` from the keys list, asymmetric keys only — symmetric keys get a client-side toast error instead of a doomed call); fetches `cryptoKeyVersions/1` specifically since this app has never listed individual CryptoKeyVersions, documented inline as a "version 1 only" simplification; PEM rendered in a new scrollable view mirroring Secret Manager's existing value-reveal pattern.
- [x] **Bigtable**: table list (`t` from instance detail, `Tables.List` with `SCHEMA_VIEW`, shows table name + column families, read-only). create/delete/describe/restore/undelete tables remain out of scope.
- [x] **Cloud SQL**: execute-sql, read-only only (`e` from list or detail view, form for database name + one SQL statement). Turned out to be feasible via the Admin API alone — `sqladmin/v1beta4`'s `Instances.ExecuteSql` RPC runs a query through the management API itself (using `AutoIamAuthn`, no stored DB password, no direct wire-protocol connection/proxy needed) and returns structured columns/rows. A client-side `ValidateReadOnlySQL` check rejects anything but a single `SELECT`/`WITH`/`SHOW`/`EXPLAIN`/`DESCRIBE` statement and scans the whole body (including inside a CTE) for write keywords before any request is built — there is no code path capable of issuing INSERT/UPDATE/DELETE/DDL, per this task's explicit scoping instruction to prefer read-only-only for execute-sql.
- [ ] **Explicitly considered and skipped, per the task's "skip anything requiring substantial new UI surface" guidance**: Spanner execute-sql (same read-only-only approach would apply but wasn't reached this pass — good next candidate, mirrors the Cloud SQL implementation above); BigQuery insert/show-rows/copy/jobs (`show-rows` needs a table-within-a-table renderer this app doesn't have); Firestore export/import/bulk-delete (needs a GCS-target picker plus a real bulk-delete confirmation flow); Redis export/import/get-auth-string (export/import need a GCS target, though get-auth-string is a good simple future add — a single read-only API call); Dataproc jobs submit/list/kill (job submission needs a much larger form — main-class/jar/args — than this pass's single/two-field pattern); Cloud Tasks task-level ops (this app doesn't list individual tasks within a queue, only queue-level metadata); Parameter Manager versions create/render (same shape as Secret Manager's add-version, a good near-identical future add); Cloud DNS record-sets transaction (needs a multi-step batch add/remove editor); Cloud Logging sinks/metrics/buckets/views (four distinct new sub-resources, each needing its own list/read surface first); Cloud Build log streaming (needs a scrolling log viewer this app has no component for); Artifact Registry docker tags (tag-level add/delete/list under the existing image drill-down — a reasonable future add, just not reached this pass).
- **Verification**: `go build ./...`, `go vet ./...`, and `go test ./...` all pass with 0 issues after every batch. `golangci-lint run ./...` found 3 issues at the final full-repo pass (2 unchecked `Close()` errors in the new GCS download code, 1 staticcheck `WriteString(fmt.Sprintf(...))` simplification in the new Cloud SQL query-result view) — all three fixed directly, and a final `golangci-lint run ./...` reports 0 issues repo-wide. No data-plane call was ever invoked against a real GCP project, per the explicit safety constraint for this category — this is unverified against live behavior, same as every other mutating category in this app.

## Reference: GCP Operations Map — gcloud CLI Parity (2026-08-27)

A full enumeration of the real `gcloud` CLI command surface for every service `tgcp` currently covers, categorized by operation type, with implementation status. Used to scope the "Open" checklist above — what's read (done), what's mutating (mostly not done), and which categories don't exist in `tgcp` at all yet (data-plane ops, most Create/Update/Delete, most IAM policy writes).

**Categories:**
- **Read** — `list`, `describe`, `get-iam-policy`, `show-rows`, `export` (config export, not data)
- **Create** — `create`
- **Update** — `update`, `patch`, `set-*`, `add-labels`/`remove-labels`, `add-tags`/`remove-tags`
- **Delete** — `delete`, `undelete`
- **Lifecycle** — `start`/`stop`/`restart`/`reset`/`resume`/`suspend`/`pause`, `resize`/`scale`, `failover`/`promote`, `upgrade`, `cancel`/`kill`/`drain`, `run` (trigger)
- **IAM** — `add-iam-policy-binding`, `remove-iam-policy-binding`, `set-iam-policy`
- **Data-plane** — operations on the data *inside* the resource, not the resource itself: `cp`/`rsync`/`rm` (GCS objects), `publish`/`pull`/`ack` (Pub/Sub), `execute-sql` (Spanner/Cloud SQL), `import`/`export` (data, not config)

Status legend: ✅ implemented in `tgcp` · 🟡 partially implemented · ⬜ not implemented

### Compute & Containers

- **VM Instances** (`compute instances`)
  - [x] Read — list/describe
  - [x] Create
  - [x] Update — network tags only (`u` key, pre-populated form); set-machine-type (needs stop first), add/remove-labels, add/remove-metadata out of scope
  - [x] Delete — `d` key from list or detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — start/stop/reset/suspend/resume (`R`/`z`/`Z` keys, confirm dialog, 2026-08-28 Round 7)
    - [ ] perform-maintenance — sole-tenant-node-only operation, rarely applicable, skipped
  - [ ] IAM
  - Data-plane: —
- **MIGs** (`compute instance-groups managed`)
  - [x] Read — list (tab in `gce`)
  - [ ] Create
  - [x] Update — target size resize only (`u` key from list view, pre-populated form); set-autoscaling, update-instances (rolling replace/restart) out of scope
  - [x] Delete — `d` key from list view, confirm dialog (2026-08-28 Round 6)
  - [ ] Lifecycle — resize (already covered by the Update-pass target-size resize above), start/stop-instances, rolling-action replace/restart — all three need a specific-instance-within-the-group selector this app's MIG model doesn't have (only aggregate group state is fetched), skipped in the 2026-08-28 Round 7 Lifecycle pass
  - [ ] IAM
  - Data-plane: —
- **Disks** (`compute disks`)
  - [x] Read — list/describe
  - [x] Create
  - [x] Update — resize (grow only) via `u` key, pre-populated form; move/update-kms-key out of scope
  - [x] Delete — `d` key from list or detail view, confirm dialog (API refuses if still attached to an instance) (2026-08-28 Round 6)
  - [x] Lifecycle — snapshot
    - [ ] start/stop-async-replication — requires configuring/tracking a secondary disk in another region, which this app's disk model doesn't represent; skipped in the 2026-08-28 Round 7 Lifecycle pass as out of the "simplest single-call" scope
  - [ ] IAM
  - Data-plane: —
- **GKE** (`container clusters`/`node-pools`)
  - [x] Read — list/describe
  - [x] Create
  - [x] Update — node pool resize (`u` key from detail view, first pool only); cluster/pool version `upgrade` and `complete-control-plane-upgrade` out of scope
  - [x] Delete — `d` key from detail view, double confirmation (irreversible, destroys every node pool/workload) (2026-08-28 Round 6)
  - [ ] Lifecycle — upgrade, complete-control-plane-upgrade — explicitly named as out of scope in the task brief for this pass (multi-step orchestration with real availability risk to a running cluster); skipped
  - [ ] IAM (no `add-iam-policy-binding` at cluster level in gcloud either — project-level only)
  - Data-plane: —
- **Cloud Run** (`run services`/`jobs`)
  - [x] Read — list/describe (Services + Functions tabs)
  - [x] Create — services only, not jobs
  - [x] Update — container image only (`u` key, pre-populated form; completed in a prior pass); update-traffic and full service replace out of scope
  - [x] Delete — services only (not jobs, which aren't covered by this service at all); `d` key from list or detail view, confirm dialog (2026-08-28 Round 6)
  - [ ] Lifecycle — execute (jobs), deploy — Cloud Run Jobs aren't covered by this service at all (only Services), and `deploy` needs a full service-replace form; both explicitly named as out of scope for this pass, skipped
  - [ ] IAM — add/remove/set/get-iam-policy
  - [ ] Data-plane — proxy, logs read
- **Cloud SQL** (`sql instances`)
  - [x] Read — list/describe
  - [x] Create
  - [x] Update — machine tier only (`u` key, pre-populated form) via Settings-only Patch; full `patch` surface (storage, flags, backups) out of scope
  - [x] Delete — `d` key from list or detail view, double confirmation (destroys all databases on the instance) (2026-08-28 Round 6)
  - [x] Lifecycle — start/stop/restart
    - [ ] failover/promote-replica/clone/point-in-time-restore/switchover — all five explicitly named as out of scope for this pass (each requires multi-step orchestration: failover needs an HA-configured instance and real downtime risk, promote-replica/clone/PITR/switchover all involve a second instance or backup/timestamp reference this app doesn't model); skipped
  - [ ] IAM
  - [x] Data-plane — execute-sql, read-only (2026-08-28). `e` key from list or detail view opens a form for a database name + a single SQL statement, then calls the Admin API's `instances.executeSql` RPC (`sqladmin/v1beta4` `InstancesService.ExecuteSql`, backed by `AutoIamAuthn` so no DB password is collected) and renders the returned columns/rows in a table. Client-side `ValidateReadOnlySQL` (in `api.go`) rejects anything but a single `SELECT`/`WITH`/`SHOW`/`EXPLAIN`/`DESCRIBE` statement and scans for write keywords (including inside a CTE) before the request is ever sent — there is no code path that can issue INSERT/UPDATE/DELETE/DDL. `sql databases`/`sql users`/`sql backups` still not covered at all (read or otherwise).
- **Cloud Functions** (`functions`)
  - [x] Read — list/describe (Gen1+Gen2)
  - [ ] Create — deploy
  - [ ] Update — skipped: `functions deploy` (used for update too) needs a real source bundle/GCS upload, same blocker as Create
  - [x] Delete — `d` key from list or detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — call (`c` key from detail view opens a JSON-data form, then confirm dialog, 2026-08-28 Round 7). Gen1 only — the v2 API this package otherwise uses has no `call` RPC, so this adds the v1 API purely for `Projects.Locations.Functions.Call`; Gen2 functions are rejected client-side with a clear error (they're backed by Cloud Run and invoked over HTTPS instead, which this app has no trigger-URL/auth flow for).
  - [ ] IAM — add/remove/set/get-iam-policy, add-invoker-policy-binding
  - Data-plane: —

### Data & Storage

- **GCS** (`storage buckets`, object ops)
  - [x] Read — list/describe buckets, list/describe objects
  - [x] Create — buckets only
  - [x] Update — default storage class only (`u` key from detail view, pre-populated form); lifecycle rules, CORS, versioning, retention, relocate out of scope
  - [x] Delete — buckets only, and only if empty (client-side `IsBucketEmpty` check before the call — this app has no object-level delete to empty a bucket first); `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - Lifecycle: —
  - [x] IAM — add-iam-policy-binding only (`i` key from bucket detail view shows current bindings via `Bucket.IAM().Policy()`; `a` opens a role+member form; confirm dialog spells out role/member/bucket before granting via `Policy.Add`+`SetPolicy`, which merges into the existing policy rather than replacing it) (2026-08-28 Round 8). remove-iam-policy-binding/set-iam-policy (raw) out of scope, per this pass's safety-first scoping.
  - [x] Data-plane — object delete and download only (2026-08-28 Round 9). From the object list, `d` deletes the selected object via the existing confirm dialog (`bucket.Object(name).Delete`); `c` downloads it to `~/Downloads/<basename>` (falls back to `.`), non-destructive so no confirm dialog. `rsync`, `mv`, `sign-url` skipped — batch/multi-object rsync and a URL-signing flow don't fit this pass's single-object scope.
- **BigQuery** (`alpha bq datasets`/`tables`)
  - [x] Read — list (own client, not via gcloud's alpha-only surface)
  - [x] Create — datasets only, not tables
  - [x] Update — dataset description only (`u` key from dataset list, pre-populated form); labels, access, default table expiration, table-level update out of scope
  - [x] Delete — datasets only, and only if empty (API itself refuses non-empty datasets, matching `bq rm -d` without `-f`); `d` key from dataset list, confirm dialog (2026-08-28 Round 6)
  - Lifecycle: —
  - IAM: —
  - [ ] Data-plane — insert, show-rows, copy, jobs (query execution)
- **Bigtable** (`bigtable instances`/`clusters`/`tables`)
  - [x] Read — list instances+clusters
  - [x] Create — instances only
  - [x] Update — cluster node-count resize only (`u` key from detail view, first cluster only); instance `upgrade`, autoscaling config, multi-cluster selection out of scope
  - [x] Delete — instance only (destroys every cluster/table in it); `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - Lifecycle: —
  - [ ] IAM — add/remove/set/get-iam-policy
  - [x] Data-plane — table list only (2026-08-28 Round 9). `t` key from instance detail view lists the instance's tables with their column-family names (`Tables.List` with `SCHEMA_VIEW`); read-only, `esc`/`q` back to detail. create/delete/describe/restore/undelete tables remain out of scope.
- **Firestore** (`firestore databases`)
  - [x] Read — list databases, namespaces/kinds (Datastore mode)
  - [x] Create
  - [x] Update — delete protection toggle only (`u` key from detail view, pre-populated form); concurrency mode, PITR out of scope
  - [x] Delete — Firestore databases *are* deletable via the Admin API (`projects.databases.delete`), contrary to the original note in this table; the API itself refuses if delete protection is enabled (toggle via `u`); `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [ ] Lifecycle — clone, restore — both explicitly named as out of scope for this pass (each needs a source backup/database reference and a new-database target this app doesn't have a picker for); skipped
  - IAM: —
  - [ ] Data-plane — export/import/bulk-delete; `indexes`, `backups`, `backups schedules` not covered
- **Spanner** (`spanner instances`/`databases`)
  - [x] Read — list instances+databases
  - [x] Create — instances only
  - [x] Update — node count only (`u` key from detail view, pre-populated form); DDL update, move, change-quorum out of scope
  - [x] Delete — instance only (destroys every database in it); `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [ ] Lifecycle — change-quorum — dual-region/witness-region quorum reconfiguration; a narrow, high-risk, rarely-used operation with no simple single-field form, skipped in the 2026-08-28 Round 7 pass
  - [ ] IAM — add/remove/set/get-iam-policy
  - [ ] Data-plane — execute-sql; `databases roles`/`sessions`/`splits` not covered
- **Redis/Memorystore** (`redis instances`)
  - [x] Read — list/describe
  - [x] Create
  - [x] Update — memory size resize only (`u` key, pre-populated form); version upgrade/reschedule-maintenance out of scope
  - [x] Delete — `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — failover (`f` key, confirm dialog, always `LIMITED_DATA_LOSS`, 2026-08-28 Round 7)
    - [ ] reschedule-maintenance — narrow scheduling-window operation, low value, skipped
  - IAM: —
  - [ ] Data-plane — export/import, get-auth-string
- **Dataflow** (`dataflow jobs`)
  - [x] Read — list/describe
  - [x] Create — run job from a template
  - [ ] Update — skipped: `update-options` requires per-transform key/value overrides that don't fit a flat form
  - [x] Delete — archive; Dataflow has no true delete, so this sets the `archived` label via a labels-only Update, matching `gcloud dataflow jobs archive` (the API rejects archiving a still-running job); `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — cancel/drain (`c`/`x` keys, confirm dialog, `Jobs.Update` with `requestedState`, 2026-08-28 Round 7)
  - IAM: —
  - Data-plane: —
- **Dataproc** (`dataproc clusters`/`jobs`)
  - [x] Read — list/describe clusters
  - [x] Create
  - [x] Update — primary worker-group node count only (`u` key from detail view, pre-populated form); secondary/preemptible workers, autoscaling policies, graceful decommission out of scope
  - [x] Delete — `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — start/stop (`s`/`x` keys, confirm dialog, 2026-08-28 Round 7)
    - [ ] diagnose — produces an async diagnostic tarball with no sensible UI to surface the result, skipped
  - [ ] IAM — get/set-iam-policy
  - [ ] Data-plane — export/import config; `jobs` (submit/list/kill for spark/hadoop/hive/pig/etc.) not covered at all

### Messaging & Scheduling

- **Pub/Sub** (`pubsub topics`/`subscriptions`)
  - [x] Read — list topics+subscriptions
  - [x] Create — topics + subscriptions
  - [x] Update — subscription ack-deadline only (`u` key from subscription detail view, pre-populated form); push config, retention, dead-letter policy, topic update out of scope
  - [x] Delete — topics and subscriptions, `d` key from either detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — detach-subscription (`x` key from subscription detail view, confirm dialog, 2026-08-28 Round 7)
  - [x] IAM — add-iam-policy-binding, topics only (`i` key from topic detail view shows current bindings via `Projects.Topics.GetIamPolicy`; `a` opens a role+member form; confirm dialog spells out role/member/topic before granting via a get-merge-set that appends to the fetched policy, preserving its `etag`) (2026-08-28 Round 8). Subscriptions and remove-iam-policy-binding/set-iam-policy (raw) out of scope, per this pass's safety-first scoping.
  - [x] Data-plane — publish and pull-without-ack only (2026-08-28 Round 9). `p` from topic detail opens a one-field message-body form, then a confirm dialog showing the exact message text and target topic, then `Projects.Topics.Publish` (REST); toast shows the resulting message ID. `P` from subscription detail does an immediate `Projects.Subscriptions.Pull` (maxMessages=10, `ReturnImmediately: true`), rendering each pulled message's ID/publish time/attributes/data — no confirm needed since it's read-only, and Acknowledge is never called (true no-ack). `ack`, `modify-message-ack-deadline`, `seek` skipped — no natural UI surface for picking specific pulled messages to ack/seek in this pass.
- **Cloud Tasks** (`tasks queues`)
  - [x] Read — list queues (no task-level list)
  - [x] Create
  - [x] Update — max dispatches/sec only (`u` key from detail view, pre-populated form); max concurrent dispatches, retry config, app-engine routing out of scope
  - [x] Delete — `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — pause/resume/purge (`p`/`R`/`x` keys, confirm dialog, 2026-08-28 Round 7)
  - [ ] IAM — add/remove/set/get-iam-policy
  - [ ] Data-plane — task-level create-http-task/create-app-engine-task/run/delete/list/describe
- **Cloud Scheduler** (`scheduler jobs`)
  - [x] Read — list/describe
  - [x] Create — HTTP jobs only
    - [ ] pubsub/app-engine job variants
  - [x] Update — cron schedule only (`u` key, pre-populated form); target/type changes out of scope
  - [x] Delete — `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — pause/resume/run (`p`/`R`/`x` keys, confirm dialog, 2026-08-28 Round 7)
  - IAM: —
  - Data-plane: —

### Security & Identity

- **IAM Service Accounts** (`iam service-accounts`)
  - [x] Read — list/describe
  - [x] Create
  - [x] Update — display name only (`u` key from detail view, pre-populated form); description, disable/enable/undelete, `keys` subgroup out of scope
  - [x] Delete — `d` key from detail view, double confirmation (immediately breaks any workload authenticating as this identity; no `undelete` exposed in this app) (2026-08-28 Round 6)
  - [x] Lifecycle — disable/enable (`E`/`D` keys, confirm dialog, 2026-08-28 Round 7)
    - [ ] undelete — needs the deleted account's unique ID within a 30-day window, which this app doesn't track since it doesn't list deleted accounts; skipped
  - [x] IAM — project-level role read
    - [ ] add/remove/set-iam-policy (write), `keys` subgroup
  - Data-plane: —
- **Secret Manager** (`secrets`)
  - [x] Read — list secrets+versions, value reveal
  - [x] Create
  - [x] Update — labels only (`u` key from detail view, pre-populated form); replication set/update out of scope
  - [x] Delete — deletes all versions; `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [x] Lifecycle — versions enable/disable/destroy (`E`/`D`/`X` keys from version detail view, confirm dialog, 2026-08-28 Round 7)
  - [x] IAM — add-iam-policy-binding only (`i` key from secret detail view shows current bindings via `Projects.Secrets.GetIamPolicy`; `a` opens a role+member form; confirm dialog spells out role/member/secret before granting via a get-merge-set that appends to the fetched policy) (2026-08-28 Round 8). remove-iam-policy-binding/set-iam-policy (raw) out of scope, per this pass's safety-first scoping.
  - [x] Data-plane — versions add only (2026-08-28 Round 9). `n` (New Version) from the secret detail view opens a single-field form for the plaintext value, then a confirm dialog ("Add new version to secret <name>?"), then `Projects.Secrets.AddVersion`; version list refreshes in place with a toast naming the new version number.
- **Parameter Manager** (`parametermanager parameters`)
  - [x] Read — list, value shown directly
  - [x] Create
  - [x] Update — labels only (`u` key from detail view, pre-populated form); format is immutable after creation
  - [x] Delete — deletes all versions; `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - Lifecycle: —
  - IAM: —
  - [ ] Data-plane — versions create/render
- **KMS** (`kms keyrings`/`keys`/`keys versions`)
  - [x] Read — list keyrings+keys (metadata only)
  - [x] Create — key rings + crypto keys
  - [x] Update — rotation schedule only (`u` key from keys list, pre-populated form); other `update` fields and key-version operations out of scope
  - [x] Delete — keys only (key rings genuinely cannot be deleted in GCP; no keyring-delete built); `d` key from keys list, confirm dialog with a hard warning that the API itself requires every CryptoKeyVersion under the key to have already been destroyed first (this app doesn't implement key-version operations, so this will fail for keys with active versions) (2026-08-28 Round 6)
  - [ ] Lifecycle — set-primary-version, enable/disable/destroy/restore (key versions) — this app has never fetched or listed individual CryptoKeyVersions (only key-level metadata, per the Update-pass note above), so there's no version to select an action against; explicitly named as out of scope for this pass and skipped
  - [x] IAM — add-iam-policy-binding, key rings only (`i` key from the key rings list shows current bindings for the cursor-selected ring via `KeyManagementClient.ResourceIAM(...).Policy()`; `a` opens a role+member form; confirm dialog spells out role/member/key-ring before granting via `Policy.Add`+`SetPolicy`, which merges rather than replaces) (2026-08-28 Round 8). Crypto-key-level IAM and remove-iam-policy-binding/set-iam-policy (raw) out of scope, per this pass's safety-first scoping.
  - [x] Data-plane — get-public-key only (2026-08-28 Round 9). `k` (Public Key) from the keys list, asymmetric keys only (client-side check on `Purpose`; symmetric keys get a toast error instead of a doomed API call); fetches `cryptoKeyVersions/1` specifically since this app doesn't list individual CryptoKeyVersions — documented as a "version 1 only" simplification in code — and displays the returned PEM in a new scrollable text view mirroring Secret Manager's value-reveal pattern. `import/export-trusted-key-wrapped` skipped — wrapped-key import/export needs a key-wrapping ceremony (RSA-OAEP wrap against the target key's own wrapping public key) with no simple form-based UI fit.
- **Cloud DNS** (`dns managed-zones`/`record-sets`)
  - [x] Read — list zones, record sets
  - [x] Create — zones only
  - [ ] Update — skipped: zone-level `update` fields are narrow (mostly DNSSEC config) and record-level changes need the `record-sets transaction` add/remove flow, which doesn't fit a flat form
  - [x] Delete — only empty zones (API itself refuses a zone with records beyond the default NS/SOA, matching this app's no-record-delete scope); `d` key from zones list, confirm dialog (2026-08-28 Round 6)
  - Lifecycle: —
  - [ ] IAM — get/set-iam-policy (zones)
  - [ ] Data-plane — record-sets transaction (add/remove records), export/import

### Networking

- **VPC Networks/Subnets/Firewalls** (`compute networks`/`subnets`/`firewall-rules`)
  - [x] Read — list subnets+firewalls per network
  - [x] Create — firewall rules only, not networks/subnets
  - [x] Update — firewall rule priority only (`u` key from Firewalls tab, pre-populated form); network/subnet update, expand-ip-range out of scope
  - [x] Delete — firewall rules only, not networks/subnets; `d` key from Firewalls tab, confirm dialog (2026-08-28 Round 6)
  - Lifecycle: —
  - [ ] IAM — add/remove/set/get-iam-policy (subnets)
  - Data-plane: —
- **Load Balancing** (`compute backend-services`/`health-checks`/`url-maps`/`forwarding-rules`/`ssl-certificates`)
  - [x] Read — all 5 resource types
  - [x] Create — health checks only
  - [ ] Update — skipped: every meaningful update (add/remove-backend, set-default-service) is an inter-resource reference edit across the other 4 resource types, same reason Create was scoped down to health checks only
  - [x] Delete — health checks and backend services only (the 2 resource types this app covers at all); handles both global and regional resources; `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [ ] Lifecycle — get-health, invalidate-cdn-cache — `get-health` needs a specific backend/instance-group reference within a backend service that this app's model doesn't track, and `invalidate-cdn-cache` needs a URL map plus a host/path pattern (a URL map create/read isn't covered by this app at all); both explicitly named as out of scope for this pass and skipped
  - [ ] IAM — add/remove/set/get-iam-policy (backend-services only)
  - Data-plane: —
- **Filestore** (`filestore instances`)
  - [x] Read — list/describe
  - [x] Create
  - [x] Update — first file-share capacity resize only (`u` key from detail view, pre-populated form); multi-share instances, revert, promote/pause/resume-replica out of scope
  - [x] Delete — `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [ ] Lifecycle — revert, promote/pause/resume-replica — `revert` needs a snapshot reference (Filestore snapshots aren't covered by this app, per the Data-plane gap below) and the replica operations only apply to multi-region replicated instances, a configuration this app's Filestore model doesn't distinguish; all explicitly named as out of scope for this pass and skipped
  - IAM: —
  - [ ] Data-plane — `snapshots` subgroup not covered

### Observability

- **Cloud Monitoring** (`monitoring uptime`/`policies`)
  - [x] Read — uptime checks + alert policies
  - [x] Create — uptime checks only, not alert policies
  - [x] Update — uptime check interval only (`u` key from detail view, pre-populated form); HTTP/path/host, content matchers, alert-policy update out of scope
  - [x] Delete — both uptime checks and alert policies; `d` key from detail view, confirm dialog (2026-08-28 Round 6)
  - [ ] Lifecycle — migrate (policies) — a beta-only bulk-migration RPC (`gcloud alpha/beta monitoring policies migrate`) for moving alert policies between monitoring scopes; low value for a single-project browsing tool and not a stable API surface, skipped in the 2026-08-28 Round 7 pass
  - IAM: —
  - [ ] Data-plane — `dashboards`, `snoozes` not covered; `channels`/`alerts`/`metrics-scopes` are beta/alpha-only in gcloud, not covered
- **Cloud Logging** (`logging`)
  - [x] Read — log entries read + filter
  - [ ] Create — no Create concept in this app (see Round 4 note); intentionally excluded
  - [ ] Update — no Update concept in this app for the same reason; intentionally excluded
  - [x] Delete — no Delete concept: `gcloud logging` has no way to delete individual log entries (only entire log buckets, which this app doesn't manage); intentionally excluded, same reasoning as Create/Update (2026-08-28 Round 6 audit)
  - Lifecycle: —
  - [ ] IAM — add/remove/set/get-iam-policy (views)
  - [ ] Data-plane — `sinks`, `metrics`, `buckets`, `views` not covered at all; `write` intentionally excluded (write-path, not a browsing feature)

### Developer / CI

- **Cloud Build** (`builds`)
  - [x] Read — list/describe builds
  - [x] Create — submit
  - [ ] Update — skipped: builds are immutable once submitted, there's no `gcloud builds update`; `triggers` (which are updatable) aren't covered by this service at all
  - [x] Delete — no Delete concept: there is no `gcloud builds delete` (builds are an immutable history), same reasoning as Update; intentionally excluded (2026-08-28 Round 6 audit)
  - [x] Lifecycle — retry/cancel
  - [ ] IAM — add/set/get-iam-policy (`connections`)
  - [ ] Data-plane — log (stream); `triggers`, `connections`, `repositories`, `worker-pools` not covered at all
- **Artifact Registry** (`artifacts repositories`/`docker images`/`packages`/`versions`)
  - [x] Read — list repos + images
  - [x] Create
  - [x] Update — repository description only (`u` key from detail view, pre-populated form); set-cleanup-policies out of scope
  - [x] Delete — delete repo (`d` key from detail view, confirm dialog, 2026-08-28 Round 6). Note: this pass could not find an existing "delete image" implementation in the codebase despite this table previously marking it done — re-audited `internal/services/artifactregistry/` and found no image drill-down or `DeleteVersion` call anywhere; only repository-level delete was added this round. Image/package/version-level delete remains open.
  - [ ] Lifecycle — scan (vulnerability), list-vulnerabilities
  - [x] IAM — add-iam-policy-binding, repositories only (`g` key from repository detail view — `i` was already taken by the Images drill-down — shows current bindings via `Client.GetIamPolicy`; `a` opens a role+member form; confirm dialog spells out role/member/repository before granting via a get-merge-set that appends to the fetched policy) (2026-08-28 Round 8). remove-iam-policy-binding/set-iam-policy (raw) out of scope, per this pass's safety-first scoping.
  - [ ] Data-plane — `docker tags` add/delete/list; `packages`/non-Docker `versions` not covered

**Reading this table for planning**: every service is Read-complete for the resource types it covers. The next-highest-value gaps by volume are IAM policy *writes* and Delete. Create and most Lifecycle/Update operations are the largest remaining category but also the highest-risk to add (they require input forms, not just a confirm dialog) — per this project's convention of keeping destructive/complex mutations deliberately scoped, these should be discussed before further work.
