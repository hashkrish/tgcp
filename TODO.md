# TODO

## Notes
- No mutating call (Create/Update/Delete/Lifecycle/IAM/data-plane) has ever been tested against a live GCP project — verified only via `go build`/`go vet`/`go test`/`golangci-lint`.
- IAM only supports `add-iam-policy-binding`, and only for 5 services (GCS buckets, Pub/Sub topics, Secret Manager secrets, KMS key rings, Artifact Registry repositories). `remove-iam-policy-binding` and raw `set-iam-policy` are unimplemented everywhere (lockout-risk).
- Delete uses double-confirmation for the most destructive resources (GKE cluster, Cloud SQL instance, IAM service account) rather than type-to-confirm; type-to-confirm would be a good hardening follow-up.

## Open

### UI (landing page / palette / Cloud Run revisions)
- [x] Grid-mode service picker: no mouse support, no vertical scrolling (deliberate v1 scope limits) — **Partial**: mouse click-to-select now works in grid mode (best-effort hit-testing, mirroring the flat list's approach). Vertical scrolling stays **declined**: the grid is capped at ~7 categories / 4 columns (2 screen-rows-of-columns), which comfortably fits any real terminal height today; building scroll-viewport clipping for a case that doesn't currently arise would be speculative complexity the code's own comments already call out as unnecessary at this scale.
- [x] Command palette: recency list is session-only, not persisted
- [x] Cloud Run: no arbitrary N-way traffic splits (only promote-to-100%)
- [x] Cloud Run: no tag removal
- [x] Cloud Run: no revision deletion
- [x] Cloud Run: revision detail missing resource limits/concurrency/timeout
- [x] Cloud Run: revision detail missing min-max-scale/env-secrets-volumes — **Partial**: min/max-scale, env var names, and volume count are shown; secret *values* are deliberately never surfaced (env var names only, not values, since some are Secret Manager references).
- [x] Cloud Run: revision detail missing VPC connector/service account/ImageDigest/Conditions
- [x] Cloud Run: `ListRevisions` has no pagination and no caching/TTL
- [x] Cloud Run: no revision-scoped log filter
- [x] Live-terminal verification still needed for older mutating actions (IAM fetch, Cloud SQL restart, Cloud Build retry/cancel, Artifact Registry delete image) and everything added since — **Declined**: this environment has no live GCP project/credentials to test against. All work continues to be verified via `go build`/`go vet`/`go test`/`golangci-lint` only, per the project Notes.
- [x] Blank content pane seen once in a live tmux run navigating into a service view — reproduced on an untouched service too, likely a tmux/headless-capture artifact, never confirmed live — **Declined**: already investigated; not reproducible outside that one tmux capture and not tied to any specific service, so there's nothing actionable to fix without a repro.

### Compute & Containers
- [x] VM Instances: no perform-maintenance — `PerformMaintenanceCmd` added.
- [x] VM Instances: no IAM — add-iam-policy-binding only (`GetInstanceIAMPolicy`/`AddInstanceIAMBinding`, verified real via `go doc compute/v1 InstancesService`); remove/set declined (lockout risk).
- [x] MIGs: no Create — `CreateGroupCmd` added.
- [x] MIGs: no start/stop-instances — `StartInstancesInGroupCmd`/`StopInstancesInGroupCmd` added.
- [x] MIGs: no rolling-action replace/restart — `RollingActionReplaceCmd`/`RollingActionRestartCmd` added.
- [x] MIGs: no IAM — **Declined**: verified via `go doc compute/v1 InstanceGroupManagersService` — no `GetIamPolicy`/`SetIamPolicy` exist; Compute Engine has no resource-level IAM for instance group managers, only project-level roles.
- [x] Disks: no start/stop-async-replication — `StartAsyncReplicationCmd`/`StopAsyncReplicationCmd` added.
- [x] Disks: no IAM — add-iam-policy-binding only (verified real via `go doc compute/v1 DisksService`); remove/set declined.
- [x] GKE: no cluster/node-pool upgrade — `UpgradeMaster`/`UpgradeNodePool` added.
- [x] GKE: no IAM (gcloud itself has none at cluster level) — confirmed via `go doc container/v1 ProjectsLocationsClustersService`: no IAM methods exist. Not a real gap, no action needed.
- [x] Cloud Run: Jobs not covered at all (no create/delete/execute/deploy) — **Declined**: Jobs are a fully separate Cloud Run resource type (their own `gcloud run jobs` command group). Wiring them into `internal/services/cloudrun/run.go` would mean converting the file's existing two-way `if activeTab == TabServices {...} else {...}` conditionals (Services/Functions) into three-way switches across ~20+ call sites in an already 1500+ line file — a much larger, riskier structural change than a typical minimal-viable addition, with no natural third-tab slot to reuse. Deferred as a follow-up refactor rather than rushed into a working file.
- [x] Cloud Run: no IAM — add-only `GetServiceIAMPolicy`/`AddServiceIAMBinding` (`i` in service detail view); remove/set declined.
- [x] Cloud Run: no data-plane (proxy, logs) — **Partial**: logs already covered (service- and now revision-scoped log filters). `proxy` (`gcloud run services proxy`, a local authenticated reverse-proxy server) **declined**: this app has no local-server/networking infrastructure anywhere else, and spawning one would be a new category of feature, not a minimal-viable data-plane action.
- [x] Cloud Run: Update flow was image-only — `UpdateServiceImage` replaced with `UpdateServiceSpec`/`ServiceUpdateOpts` (CPU/memory limits, concurrency, timeout, min/max scale, VPC connector, service account, plain-value env vars — each field optional/leave-unchanged), still via the same Get→mutate→ReplaceService pattern; deploying any change creates a new revision. The Update form is seeded from the service's latest-ready revision detail when already fetched (via the Revisions view), otherwise starts blank. Traffic-split-on-deploy, canary percentages, and multi-container/sidecar editing remain out of scope.
- [x] Cloud SQL: no failover/promote-replica/clone/PITR/switchover — failover/promote-replica already existed; `CloneInstance` (also serves PITR via `CloneContext.PointInTime` — there's no separate PITR-restore RPC) and `SwitchoverInstance` added (`C`/`S` in instance detail).
- [x] Cloud SQL: no IAM — **Declined**: verified via `go doc sqladmin/v1beta4` — no `GetIamPolicy`/`SetIamPolicy` anywhere in the Cloud SQL Admin API; instance access is governed by project-level IAM roles only.
- [x] Cloud SQL: `databases`/`users`/`backups` not covered — full databases/users create+delete, backups list.
- [x] Cloud Functions: no deploy (Create/Update) — needs a real source bundle — **Declined** (pre-existing documented decision in `cloudfunctions/api.go`'s package comment): a real deploy needs a GCS-staged source zip or git reference; there's no placeholder source that produces a working function, so a Create form here would submit a request guaranteed to fail the build.
- [x] Cloud Functions: no IAM — add-only `GetFunctionIAMPolicy`/`AddFunctionIAMBinding` (`i` in function detail view, verified real via `go doc cloudfunctions/v2`); remove/set declined.
- [x] Cloud Functions: Gen2 `call` not supported (Gen1 only) — **Declined** (pre-existing documented decision): Gen2 functions are backed by Cloud Run and have no `call` RPC; `CallFunction` already rejects Gen2 client-side with a clear message pointing at the Cloud Run HTTPS trigger instead.

### Data & Storage
- [x] GCS: no lifecycle rules/CORS/versioning/retention/relocate — `UpdateBucketSettings` covers versioning/retention/CORS/lifecycle-delete-age in one call. Bucket "relocate" (a distinct long-running Storage Control API operation) and full multi-rule lifecycle/CORS configs **declined** as out of scope for this minimal Update flow (documented in code).
- [x] GCS: no remove/set-iam-policy (raw) — **Declined** (lockout-risk repo policy); add-iam-policy-binding already existed.
- [x] GCS: no rsync/mv/sign-url — `MoveObject` (mv, via copy+delete) already existed; `SignURL` added, signing via the IAM Credentials API's `SignBlob` RPC (no private-key-file dependency under ADC). Bulk/recursive `rsync` **declined** as out of scope for this minimal data-plane flow (documented in code).
- [x] BigQuery: tables not covered (create/update/delete) — **Partial**: create/delete added (schema-driven create). Update **declined**: BigQuery table schema changes (add/relax columns) are a distinct, more involved migration-style operation than this codebase's single-field-patch Update convention supports safely.
- [x] BigQuery: no data-plane (insert/show-rows/copy/jobs) — **Partial**: `RunQuery` covers show-rows (and insert, via DML, since it accepts arbitrary SQL). `copy` (table-to-table) and `jobs` (list/cancel BQ jobs) **declined**: distinct feature surfaces from the query runner already added, deferred as follow-ups.
- [x] Bigtable: no instance upgrade/autoscaling/multi-cluster select — instance "upgrade" is already covered by `UpdateInstanceType` (DEVELOPMENT→PRODUCTION is exactly what `gcloud bigtable instances upgrade` does); autoscaling already covered by `SetClusterAutoscaling`. Multi-cluster routing-policy select (an app-profile concept) **declined**: app profiles aren't modeled anywhere else in this package, so there's no existing resource for a routing policy to attach to.
- [x] Bigtable: no IAM — add-iam-policy-binding already existed (`AddInstanceIAMBinding`).
- [x] Bigtable: table create/delete/describe/restore/undelete not covered — create/delete already existed; `DescribeTable`, `RestoreTable` (from a backup resource name), and `UndeleteTable` added (`u`/`R` in the Tables view). Describe is also already implicit in the Tables list, which fetches `SCHEMA_VIEW` and shows column families per row.
- [x] Firestore: no clone/restore — `CloneDatabase` (point-in-time snapshot) and `RestoreDatabase` (from a backup resource name) added (`C`/`R` in database detail view).
- [x] Firestore: no IAM — **Declined**: verified via `go doc firestore/v1` — no `GetIamPolicy`/`SetIamPolicy` anywhere in the Firestore Admin API.
- [x] Firestore: no export/import/bulk-delete — `ExportDocuments`/`ImportDocuments`/`BulkDeleteDocuments` added.
- [x] Firestore: indexes/backups/backup-schedules not covered — **Declined**: a large amount of additional CRUD surface (composite indexes, backups, backup-schedules) that would roughly double this package's size; export/import/bulk-delete/clone/restore already cover the core data-protection operations, so this is deferred as a follow-up rather than rushed.
- [x] Spanner: no change-quorum — **Declined** (pre-existing documented decision alongside instance `move`): a complex, rarely-used operation with nested quorum-type configuration (single-region/dual-region), not a good fit for a simple form.
- [x] Spanner: no IAM — add-iam-policy-binding already existed (`AddInstanceIAMBinding`).
- [x] Spanner: no execute-sql — `ExecuteQuery` added (`e` in instance detail), using the `cloud.google.com/go/spanner` data-plane client directly (Spanner has no admin-API `executeSql` REST trick like Cloud SQL); read-only (SELECT/WITH only), 200-row cap.
- [x] Spanner: `databases roles`/`sessions`/`splits` not covered — **Declined**: advanced/rarely-used admin features (fine-grained-access role management, live session inspection, manual split-point hints) with low interactive value relative to the effort of a full CRUD surface for each; execute-sql covers the actual data-plane need.
- [x] Redis: no reschedule-maintenance — `RescheduleMaintenance` already existed.
- [x] Redis: no IAM — **Declined**: verified via `go doc redis/v1 ProjectsLocationsInstancesService` — no `GetIamPolicy`/`SetIamPolicy`; Memorystore for Redis has no resource-level IAM.
- [x] Redis: no export/import/get-auth-string — `ExportInstance`/`ImportInstance`/`GetAuthString` already existed.
- [x] Dataflow: no `update-options` — `UpdateJobOptions` added (`o` in job detail), matching `gcloud dataflow jobs update-options --min-num-workers/--max-num-workers` via `RuntimeUpdatableParams`.
- [x] Dataflow: no IAM — **Declined**: verified via `go doc dataflow/v1b3` — no IAM policy methods anywhere in the package.
- [x] Dataproc: no diagnose — `DiagnoseCluster` added (`g` in cluster detail), fire-and-forget like every other lifecycle action in this package (the real API returns a long-running operation with an eventual diagnostic-tarball URL; polling that to completion is a different kind of feature than this app's mutating-call pattern anywhere else).
- [x] Dataproc: no IAM — add-only `GetClusterIAMPolicy`/`AddClusterIAMBinding` (`i` in cluster detail); remove/set declined.
- [x] Dataproc: `jobs` submit/list/kill not covered — minimal Spark-job submit, list, and kill (cancel) added (`J` opens Jobs, `n` submit, `k` kill); other job types (Hadoop/Hive/Pig/PySpark) declined as out of scope for this minimal submit flow.

### Messaging & Scheduling
- [x] Pub/Sub: subscription-level IAM not covered (topics only) — add-only `GetSubscriptionIAMPolicy`/`AddSubscriptionIAMBinding`, mirroring the topic pattern.
- [x] Pub/Sub: no ack/modify-ack-deadline/seek — `Ack`/`ModifyAckDeadline`/`SeekToTime` added, wired into the pulled-messages view and subscription detail view.
- [x] Cloud Tasks: no IAM — add-only `GetQueueIAMPolicy`/`AddQueueIAMBinding`.
- [x] Cloud Tasks: task-level ops not covered (create/run/delete/list/describe) — HTTP-target task create, list, run-now, delete; describe via list→detail view. App Engine-target task creation declined as out of scope, matching this codebase's existing minimal-viable pattern.
- [x] Cloud Scheduler: no pubsub/app-engine job variants — `CreatePubSubJob`/`CreateAppEngineJob` added alongside the existing HTTP create.
- [x] Cloud Scheduler: no target/type update — `UpdateJobHTTPTarget`/`UpdateJobPubSubTarget` added. App Engine target update declined: Scheduler's own `UpdateJob` doesn't support type conversion either, matching upstream `gcloud` limits.

### Security & Identity
- [x] IAM Service Accounts: no undelete — `UndeleteServiceAccount` added (`U` in list view).
- [x] IAM Service Accounts: no add/remove/set-iam-policy (write) — add-only `GetServiceAccountIAMPolicy`/`AddServiceAccountIAMBinding` (`i` in detail view); remove/set declined (lockout risk).
- [x] IAM Service Accounts: `keys` subgroup not covered — list/create/delete (`K` opens Keys, `n` create, `d` delete).
- [x] Secret Manager: no replication set/update — **Declined**: replication policy (automatic vs. user-managed with specific KMS keys per region) is a create-time-shaped, multi-field structural choice, not a good fit for this codebase's single-field-patch Update convention (see `UpdateSecretLabels`'s existing labels-only scope).
- [x] Secret Manager: no remove/set-iam-policy (raw) — **Declined** (lockout-risk repo policy); add-iam-policy-binding already existed.
- [x] Parameter Manager: no versions create/render — `CreateVersion`/`RenderVersion` added.
- [x] KMS: no key-version lifecycle ops (set-primary/enable/disable/destroy/restore) — all added (`p`/`e`/`x`/`d`/`R` in the Versions view).
- [x] KMS: no crypto-key-level IAM — add-only `GetCryptoKeyIAMPolicy`/`AddCryptoKeyIAMBinding` (`i` in Keys view); remove/set declined.
- [x] KMS: no remove/set-iam-policy (raw) — **Declined** (lockout-risk repo policy); add already existed at both key-ring and crypto-key level.
- [x] KMS: no import/export-trusted-key-wrapped — **Declined**: this is a multi-step interactive key-wrapping ceremony (generate a wrapping key, wrap the external key material with OpenSSL/a tool outside this app, then submit the wrapped blob) rather than a single form submission; doesn't fit this app's one-shot mutating-action pattern.
- [x] Cloud DNS: no zone/record update — zone description update (`u` in zones list) and record-set TTL/data update (`u` in records view) added.
- [x] Cloud DNS: no IAM — add-only `GetZoneIAMPolicy`/`AddZoneIAMBinding` (`i` in zones list, verified real via `go doc dns/v1 ManagedZonesService`); remove/set declined (lockout risk).
- [x] Cloud DNS: no record-sets transaction/export/import — **Declined**: the transaction API (start/add/remove/execute as a batch) and BIND-zone-file export/import are a different interaction model (multi-step staged edits, file I/O) than this codebase's direct record CRUD (list/update; create/delete were never in scope here and remain a separate follow-up).

### Networking
- [x] VPC: networks/subnets not covered at all (firewall rules only) — Create/Delete for networks (list view) and subnets (network detail, Subnets tab).
- [x] VPC: no subnet IAM — add-iam-policy-binding only (`g` in Subnets tab); remove/set declined (lockout risk).
- [x] Load Balancing: no update (every one is a cross-resource edit) — **Scoped**: single-field `UpdateBackendServiceTimeout` patch (`u`), matching the repo's existing single-field-Update convention; true cross-resource editing (URL maps/proxies/forwarding rules together) is out of scope, documented in code.
- [x] Load Balancing: no get-health/invalidate-cdn-cache — `h` (backend health) and `i` (CDN cache invalidate on URL Maps).
- [x] Load Balancing: no IAM — add-iam-policy-binding only (`g`, global + regional backend services); remove/set declined.
- [x] Load Balancing: only 2 of 5 resource types support create/delete — **Partial**: Delete added for all 5 types (URL Maps, Forwarding Rules, SSL Certificates joined Backend Services/Health Checks). Create for the remaining 3 **declined**: URL maps need a default-service reference, forwarding rules need a target proxy, SSL certs need cert/key material or managed-domain config — none of that prerequisite plumbing exists yet as a manageable resource in this tool, so there's no honest minimal-viable create form to build.
- [x] Filestore: no revert/promote/pause/resume-replica — **Partial**: revert (`v`, snapshot ID form) and promote-replica (`p`) implemented. Pause/resume-replica **declined**: the vendored `cloud.google.com/go/filestore/apiv1` client has no such methods in this SDK version.
- [x] Filestore: no IAM — **Declined**: no `GetIamPolicy`/`SetIamPolicy` on the vendored Filestore client; not a resource-level-IAM-enabled type in this SDK.
- [x] Filestore: `snapshots` subgroup not covered — full Create/List/Delete (`s` opens Snapshots, `c` create, `x` delete).

### Observability
- [x] Cloud Monitoring: no alert-policy create — single-condition metric-threshold policy, matching the simplest `gcloud alpha monitoring policies create` shape.
- [x] Cloud Monitoring: no policy migrate — **Declined**: no stable public API RPC for this; it's an alpha-only gcloud-side schema conversion with ambiguous semantics, not a simple one-shot call.
- [x] Cloud Monitoring: no IAM — **Declined**: verified via `go doc`/grep across the entire `cloud.google.com/go/monitoring` client library (AlertPolicy/UptimeCheck/Dashboards/Snooze clients) — none expose `GetIamPolicy`/`SetIamPolicy`. Monitoring resources have no resource-level IAM in GCP; access is project-scoped only.
- [x] Cloud Monitoring: dashboards/snoozes not covered — **Partial**: dashboards (list, delete) and snoozes (list, create) implemented. Dashboard create declined: a dashboard's layout is arbitrary nested JSON (grid/mosaic + widgets), a poor fit for this codebase's simple-form Create pattern (documented in code; use Console or `--config-from-file` instead). Snooze delete not implemented since the real API has none (snoozes only expire or update).
- [x] Cloud Logging: no IAM (views) — add-only (`g`); verified `ProjectsLocationsBucketsViewsService` supports `GetIamPolicy`/`SetIamPolicy`. Remove/set declined (lockout risk).
- [x] Cloud Logging: `sinks`/`metrics`/`buckets`/`views` not covered at all — full list/create/delete for sinks, log-based metrics, and log buckets, added as a new "Resources" mode (`R`); views list/create/delete scoped to the project's default bucket (`_Default`/`global`) to avoid a full bucket-picker UI.

### Developer / CI
- [x] Cloud Build: no IAM (connections) — add-only `AddConnectionIAMBinding` on the 2nd-gen RepositoryManagerClient; remove/set declined (lockout risk).
- [x] Cloud Build: no log streaming — **Scoped**: `l` on a build's detail view routes to the shared Cloud Logging view filtered by build ID; this is a one-shot fetch-and-display, not a live tail (Cloud Build v1 has no streaming-log API).
- [x] Cloud Build: `triggers`/`connections`/`repositories`/`worker-pools` not covered — **Partial**: triggers (list/create-minimal/detail view/edit/enable-disable/run/delete) and worker pools (list/create-minimal/delete) done. Connections/repositories: list/delete + connection IAM done; **create declined** for both — creating a connection requires completing an interactive GitHub/GitLab/Bitbucket App OAuth install flow in a browser, which this TUI has no way to drive.
- [x] Cloud Build: trigger sub-view tables (`triggersTable`/`workerPoolsTable`/`connectionsTable`/`cbRepositoriesTable`) weren't resized on `tea.WindowSizeMsg`, unlike the top-level Builds table — fixed by calling `HandleWindowSizeDefault` on all four, matching the pattern already used elsewhere (e.g. `dns`).
- [x] Cloud Build: Builds list table had column overflow/misalignment on long statuses (e.g. `STATUS_UNKNOWN`, `INTERNAL_ERROR`) — `updateTable` was putting the fully lipgloss-styled `components.RenderStatus` badge (ANSI color + background padding) directly into a fixed-width table cell; the underlying `bubbles/table` truncates cells with `go-runewidth`, which isn't ANSI-escape-aware and corrupts/truncates mid-escape-sequence, bleeding styling into later columns whenever a value exceeds its column width. Every other service in this repo avoids this by using plain text (optionally a bare emoji, no lipgloss styling) in table cells and reserving `RenderStatus` for detail-card rows, which never truncate — `service.go`'s Builds table now does the same (plain `item.Status` text), and the Status column was widened 12→14 so the two longest real enum values no longer truncate at all. Also noted in passing: Cloud Build's raw status strings (`SUCCESS`/`FAILURE`/`WORKING`/`QUEUED`/etc.) don't match `components.CategorizeStatus`'s state-name vocabulary (tuned for other resource types' RUNNING/STOPPED/etc.), so the detail view's `RenderStatus` badge has always shown as neutral/unknown-colored for builds regardless of actual outcome — a separate, pre-existing cosmetic gap, not touched here.
- [x] Artifact Registry: no image/package/version-level delete — package-level (`DeletePackage`) and generic version-level (`DeleteVersion`) delete added alongside the existing image-level delete.
- [x] Artifact Registry: no vulnerability scan/list — `GetVulnerabilitySummary` via Container Analysis, shown as fixable/total-by-severity (`v` on an image).
- [x] Artifact Registry: no remove/set-iam-policy (raw) — **Declined** (lockout-risk repo policy); add-iam-policy-binding already existed.
- [x] Artifact Registry: no docker tags ops — `ListTags`/`DeleteTag` (`T` removes a single tag, preserving the version and other tags).
- [x] Artifact Registry: packages/non-docker versions not covered — Packages → Versions browse views added.
