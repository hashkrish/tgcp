# TODO

## Notes
- No mutating call (Create/Update/Delete/Lifecycle/IAM/data-plane) has ever been tested against a live GCP project — verified only via `go build`/`go vet`/`go test`/`golangci-lint`.
- IAM only supports `add-iam-policy-binding`, and only for 5 services (GCS buckets, Pub/Sub topics, Secret Manager secrets, KMS key rings, Artifact Registry repositories). `remove-iam-policy-binding` and raw `set-iam-policy` are unimplemented everywhere (lockout-risk).
- Delete uses double-confirmation for the most destructive resources (GKE cluster, Cloud SQL instance, IAM service account) rather than type-to-confirm; type-to-confirm would be a good hardening follow-up.

## Open

### UI (landing page / palette / Cloud Run revisions)
- [ ] Grid-mode service picker: no mouse support, no vertical scrolling (deliberate v1 scope limits)
- [x] Command palette: recency list is session-only, not persisted
- [x] Cloud Run: no arbitrary N-way traffic splits (only promote-to-100%)
- [x] Cloud Run: no tag removal
- [x] Cloud Run: no revision deletion
- [ ] Cloud Run: revision detail missing resource limits/concurrency/timeout
- [ ] Cloud Run: revision detail missing min-max-scale/env-secrets-volumes
- [ ] Cloud Run: revision detail missing VPC connector/service account/ImageDigest/Conditions
- [ ] Cloud Run: `ListRevisions` has no pagination and no caching/TTL
- [ ] Cloud Run: no revision-scoped log filter
- [ ] Live-terminal verification still needed for older mutating actions (IAM fetch, Cloud SQL restart, Cloud Build retry/cancel, Artifact Registry delete image) and everything added since
- [ ] Blank content pane seen once in a live tmux run navigating into a service view — reproduced on an untouched service too, likely a tmux/headless-capture artifact, never confirmed live

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
- [ ] VPC: networks/subnets not covered at all (firewall rules only)
- [ ] VPC: no subnet IAM
- [ ] Load Balancing: no update (every one is a cross-resource edit)
- [ ] Load Balancing: no get-health/invalidate-cdn-cache
- [ ] Load Balancing: no IAM
- [ ] Load Balancing: only 2 of 5 resource types support create/delete
- [ ] Filestore: no revert/promote/pause/resume-replica
- [ ] Filestore: no IAM
- [ ] Filestore: `snapshots` subgroup not covered

### Observability
- [ ] Cloud Monitoring: no alert-policy create
- [ ] Cloud Monitoring: no policy migrate
- [ ] Cloud Monitoring: no IAM
- [ ] Cloud Monitoring: dashboards/snoozes not covered
- [ ] Cloud Logging: no IAM (views)
- [ ] Cloud Logging: `sinks`/`metrics`/`buckets`/`views` not covered at all

### Developer / CI
- [ ] Cloud Build: no IAM (connections)
- [ ] Cloud Build: no log streaming
- [ ] Cloud Build: `triggers`/`connections`/`repositories`/`worker-pools` not covered
- [ ] Artifact Registry: no image/package/version-level delete
- [ ] Artifact Registry: no vulnerability scan/list
- [ ] Artifact Registry: no remove/set-iam-policy (raw)
- [ ] Artifact Registry: no docker tags ops
- [ ] Artifact Registry: packages/non-docker versions not covered
