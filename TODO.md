# TODO

## Notes
- No mutating call (Create/Update/Delete/Lifecycle/IAM/data-plane) has ever been tested against a live GCP project — verified only via `go build`/`go vet`/`go test`/`golangci-lint`.
- IAM only supports `add-iam-policy-binding`, and only for 5 services (GCS buckets, Pub/Sub topics, Secret Manager secrets, KMS key rings, Artifact Registry repositories). `remove-iam-policy-binding` and raw `set-iam-policy` are unimplemented everywhere (lockout-risk).
- Delete uses double-confirmation for the most destructive resources (GKE cluster, Cloud SQL instance, IAM service account) rather than type-to-confirm; type-to-confirm would be a good hardening follow-up.

## Open

### UI (landing page / palette / Cloud Run revisions)
- Grid-mode service picker: no mouse support, no vertical scrolling (deliberate v1 scope limits)
- Command palette: recency list is session-only, not persisted
- Cloud Run: no arbitrary N-way traffic splits (only promote-to-100%), no tag removal, no revision deletion
- Cloud Run: revision detail missing resource limits/concurrency/timeout/min-max-scale/env-secrets-volumes/VPC connector/service account/ImageDigest/Conditions
- Cloud Run: `ListRevisions` has no pagination and no caching/TTL; no revision-scoped log filter
- Live-terminal verification still needed for older mutating actions (IAM fetch, Cloud SQL restart, Cloud Build retry/cancel, Artifact Registry delete image) and everything added since
- Blank content pane seen once in a live tmux run navigating into a service view — reproduced on an untouched service too, likely a tmux/headless-capture artifact, never confirmed live

### Compute & Containers
- VM Instances: no perform-maintenance; no IAM
- MIGs: no Create; no start/stop-instances or rolling-action replace/restart; no IAM
- Disks: no start/stop-async-replication; no IAM
- GKE: no cluster/node-pool upgrade; no IAM (gcloud itself has none at cluster level)
- Cloud Run: Jobs not covered at all (no create/delete/execute/deploy); no IAM; no data-plane (proxy, logs)
- Cloud SQL: no failover/promote-replica/clone/PITR/switchover; no IAM; `databases`/`users`/`backups` not covered
- Cloud Functions: no deploy (Create/Update) — needs a real source bundle; no IAM; Gen2 `call` not supported (Gen1 only)

### Data & Storage
- GCS: no lifecycle rules/CORS/versioning/retention/relocate; no remove/set-iam-policy (raw); no rsync/mv/sign-url
- BigQuery: tables not covered (create/update/delete); no data-plane (insert/show-rows/copy/jobs)
- Bigtable: no instance upgrade/autoscaling/multi-cluster select; no IAM; table create/delete/describe/restore/undelete not covered
- Firestore: no clone/restore; no IAM; no export/import/bulk-delete; indexes/backups/backup-schedules not covered
- Spanner: no change-quorum; no IAM; no execute-sql; `databases roles`/`sessions`/`splits` not covered
- Redis: no reschedule-maintenance; no IAM; no export/import/get-auth-string
- Dataflow: no `update-options`; no IAM
- Dataproc: no diagnose; no IAM; `jobs` submit/list/kill not covered

### Messaging & Scheduling
- Pub/Sub: subscription-level IAM not covered (topics only); no ack/modify-ack-deadline/seek
- Cloud Tasks: no IAM; task-level ops not covered (create/run/delete/list/describe)
- Cloud Scheduler: no pubsub/app-engine job variants; no target/type update

### Security & Identity
- IAM Service Accounts: no undelete; no add/remove/set-iam-policy (write); `keys` subgroup not covered
- Secret Manager: no replication set/update; no remove/set-iam-policy (raw)
- Parameter Manager: no versions create/render
- KMS: no key-version lifecycle ops (set-primary/enable/disable/destroy/restore); no crypto-key-level IAM; no remove/set-iam-policy (raw); no import/export-trusted-key-wrapped
- Cloud DNS: no zone/record update; no IAM; no record-sets transaction/export/import

### Networking
- VPC: networks/subnets not covered at all (firewall rules only); no subnet IAM
- Load Balancing: no update (every one is a cross-resource edit); no get-health/invalidate-cdn-cache; no IAM; only 2 of 5 resource types support create/delete
- Filestore: no revert/promote/pause/resume-replica; no IAM; `snapshots` subgroup not covered

### Observability
- Cloud Monitoring: no alert-policy create; no policy migrate; no IAM; dashboards/snoozes not covered
- Cloud Logging: no IAM (views); `sinks`/`metrics`/`buckets`/`views` not covered at all

### Developer / CI
- Cloud Build: no IAM (connections); no log streaming; `triggers`/`connections`/`repositories`/`worker-pools` not covered
- Artifact Registry: no image/package/version-level delete; no vulnerability scan/list; no remove/set-iam-policy (raw); no docker tags ops; packages/non-docker versions not covered
