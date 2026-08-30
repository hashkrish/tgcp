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
- [ ] VM Instances: no perform-maintenance
- [ ] VM Instances: no IAM
- [ ] MIGs: no Create
- [ ] MIGs: no start/stop-instances
- [ ] MIGs: no rolling-action replace/restart
- [ ] MIGs: no IAM
- [ ] Disks: no start/stop-async-replication
- [ ] Disks: no IAM
- [ ] GKE: no cluster/node-pool upgrade
- [ ] GKE: no IAM (gcloud itself has none at cluster level)
- [ ] Cloud Run: Jobs not covered at all (no create/delete/execute/deploy)
- [ ] Cloud Run: no IAM
- [ ] Cloud Run: no data-plane (proxy, logs)
- [ ] Cloud SQL: no failover/promote-replica/clone/PITR/switchover
- [ ] Cloud SQL: no IAM
- [ ] Cloud SQL: `databases`/`users`/`backups` not covered
- [ ] Cloud Functions: no deploy (Create/Update) — needs a real source bundle
- [ ] Cloud Functions: no IAM
- [ ] Cloud Functions: Gen2 `call` not supported (Gen1 only)

### Data & Storage
- [ ] GCS: no lifecycle rules/CORS/versioning/retention/relocate
- [ ] GCS: no remove/set-iam-policy (raw)
- [ ] GCS: no rsync/mv/sign-url
- [ ] BigQuery: tables not covered (create/update/delete)
- [ ] BigQuery: no data-plane (insert/show-rows/copy/jobs)
- [ ] Bigtable: no instance upgrade/autoscaling/multi-cluster select
- [ ] Bigtable: no IAM
- [ ] Bigtable: table create/delete/describe/restore/undelete not covered
- [ ] Firestore: no clone/restore
- [ ] Firestore: no IAM
- [ ] Firestore: no export/import/bulk-delete
- [ ] Firestore: indexes/backups/backup-schedules not covered
- [ ] Spanner: no change-quorum
- [ ] Spanner: no IAM
- [ ] Spanner: no execute-sql
- [ ] Spanner: `databases roles`/`sessions`/`splits` not covered
- [ ] Redis: no reschedule-maintenance
- [ ] Redis: no IAM
- [ ] Redis: no export/import/get-auth-string
- [ ] Dataflow: no `update-options`
- [ ] Dataflow: no IAM
- [ ] Dataproc: no diagnose
- [ ] Dataproc: no IAM
- [ ] Dataproc: `jobs` submit/list/kill not covered

### Messaging & Scheduling
- [ ] Pub/Sub: subscription-level IAM not covered (topics only)
- [ ] Pub/Sub: no ack/modify-ack-deadline/seek
- [ ] Cloud Tasks: no IAM
- [ ] Cloud Tasks: task-level ops not covered (create/run/delete/list/describe)
- [ ] Cloud Scheduler: no pubsub/app-engine job variants
- [ ] Cloud Scheduler: no target/type update

### Security & Identity
- [ ] IAM Service Accounts: no undelete
- [ ] IAM Service Accounts: no add/remove/set-iam-policy (write)
- [ ] IAM Service Accounts: `keys` subgroup not covered
- [ ] Secret Manager: no replication set/update
- [ ] Secret Manager: no remove/set-iam-policy (raw)
- [ ] Parameter Manager: no versions create/render
- [ ] KMS: no key-version lifecycle ops (set-primary/enable/disable/destroy/restore)
- [ ] KMS: no crypto-key-level IAM
- [ ] KMS: no remove/set-iam-policy (raw)
- [ ] KMS: no import/export-trusted-key-wrapped
- [ ] Cloud DNS: no zone/record update
- [ ] Cloud DNS: no IAM
- [ ] Cloud DNS: no record-sets transaction/export/import

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
- [x] Cloud Build: `triggers`/`connections`/`repositories`/`worker-pools` not covered — **Partial**: triggers (list/create-minimal/run/delete) and worker pools (list/create-minimal/delete) done. Connections/repositories: list/delete + connection IAM done; **create declined** for both — creating a connection requires completing an interactive GitHub/GitLab/Bitbucket App OAuth install flow in a browser, which this TUI has no way to drive.
- [x] Artifact Registry: no image/package/version-level delete — package-level (`DeletePackage`) and generic version-level (`DeleteVersion`) delete added alongside the existing image-level delete.
- [x] Artifact Registry: no vulnerability scan/list — `GetVulnerabilitySummary` via Container Analysis, shown as fixable/total-by-severity (`v` on an image).
- [x] Artifact Registry: no remove/set-iam-policy (raw) — **Declined** (lockout-risk repo policy); add-iam-policy-binding already existed.
- [x] Artifact Registry: no docker tags ops — `ListTags`/`DeleteTag` (`T` removes a single tag, preserving the version and other tags).
- [x] Artifact Registry: packages/non-docker versions not covered — Packages → Versions browse views added.
