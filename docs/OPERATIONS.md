# Operations and recovery

FOLIO includes a Python 3.11+ standard-library operations module under `ops/`. It uses the public Folio CLI contract rather than depending on SQLite table structure. Read [security](SECURITY.md) before exposing a service.

## Native service

Build from this repository with Go 1.25 or newer, then initialize and run as the intended non-root service user:

```sh
mkdir -p dist
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio
./dist/folio init --data /absolute/private/folio-data
./dist/folio serve --data /absolute/private/folio-data --addr 127.0.0.1:8080
```

The parent of the chosen data path must exist and be accessible to the service user. Initialization creates a private 0700 data directory and a 0600 administrator token file. Keep data outside source-control and web roots. Do not run a second writer on that directory. SQLite stores media BLOBs in `folio.db`; active WAL/SHM files belong with the database.

In another terminal:

```sh
python3 ops/ops.py doctor --data /absolute/private/folio-data
python3 ops/ops.py probe --url http://127.0.0.1:8080
python3 ops/ops.py probe --url http://127.0.0.1:8080 --ready
```

`doctor` reports tool availability; exit 0 does not imply Docker is ready. Check `container_runtime_ready`. `/readyz` currently aliases the lightweight health check, not a deep database readiness test.

## Container deployment

The checked-in image recipe is `ops/deploy/Dockerfile`; its build context is `ops/`. It packages only a verified static Linux binary and an explicitly supplied, hash-verified CA bundle into `scratch`. It does not download binaries or base images. Preserve the CA bundle's publisher/source and applicable license separately with release provenance.

1. Build a Linux binary for the target architecture, even if building on macOS:

   ```sh
   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -o dist/folio-linux-amd64 ./cmd/folio
   ```

2. From the repository root, stage trusted artifacts. `ops/dist` must not already exist. Supply actual SHA256 values from trusted build/release records; transport checksums alone do not establish publisher identity:

   ```sh
   python3 ops/ops.py prepare-image \
     --binary dist/folio-linux-amd64 --sha256 REPLACE_WITH_BINARY_SHA256 \
     --ca-bundle /trusted/ca-certificates.crt --ca-sha256 REPLACE_WITH_CA_SHA256 \
     --arch amd64 --output ops/dist
   docker build --platform linux/amd64 -f ops/deploy/Dockerfile -t folio:local ops
   docker image inspect --format '{{.Id}}' folio:local
   ```

3. Initialize data using the native Folio binary under the same non-root numeric UID that will run the service. `render` defaults to the current UID/GID and verifies ownership. Do not initialize as root then use a different runtime user. The image's standalone default is UID 65532, but generated Compose overrides it with the verified owner. Ownership is never changed automatically:

   ```sh
   python3 ops/ops.py render --data /absolute/private/folio-data \
     --image sha256:REPLACE_WITH_ACTUAL_IMAGE_ID --output /absolute/private/compose.json
   docker compose -f /absolute/private/compose.json config --quiet
   docker compose -f /absolute/private/compose.json up -d --no-build --pull never
   python3 ops/ops.py probe --url http://127.0.0.1:8080 --ready
   ```

Compose binds only loopback, runs non-root, drops all capabilities, disallows privilege escalation, makes the image filesystem read-only, limits resources and logs, and gives SIGTERM 15 seconds. Data is the only persistent writable mount. Generated JSON is a Compose file; an additional YAML template is unnecessary. Only one generated `folio` project per Docker daemon is supported. See the [deployment details](../ops/docs/deployment.md).

For remote access, use a trusted HTTPS reverse proxy, set the canonical `base_url`, and separately review exposure, authentication, storage, upgrades and TLS. These examples do not deploy an internet service or configure a proxy. Do not disable TLS verification. The CLI refuses non-loopback plaintext URLs when sending bearer tokens.

## Online backup and recovery drill

Create a private backup parent directory, then:

```sh
mkdir -m 700 /absolute/private/backups
python3 ops/ops.py logical-export --binary dist/folio \
  --url http://127.0.0.1:8080 --token-file /absolute/private/folio-data/token \
  --output /absolute/private/backups/snapshot.json
python3 ops/ops.py logical-drill --binary dist/folio \
  --url http://127.0.0.1:8080 --token-file /absolute/private/folio-data/token \
  --workdir /absolute/private/new-recovery-drill
```

Exports include drafts, revisions, media, proposals, pinned publishing schedules and private import provenance, and exclude credentials. Keep them private. The drill creates a fresh isolated instance with an independent token, starts v0.2 with `--pause-schedules` and verifies `system.info.scheduler.paused`, restores, verifies exact content and expected audit/revision changes, then shuts it down. Restore advances the instance revision and adds an audit entry, so complete source/restored state hashes intentionally differ. Go remains authoritative for checksum validation; Python does not reimplement its serializer. The drill retains private recovery artifacts for inspection; use a new directory each time.

To restore independently, initialize an empty target directory, run `folio serve --data /new/data --addr 127.0.0.1:8081 --pause-schedules`, verify `system.info.scheduler.paused=true`, and run `logical-restore` against that URL using the target's token. Existing content is never overwritten. See the [backup and recovery guide](../ops/docs/backup-recovery.md). Logical files are limited to 64 MiB in this release.

## Portable Markdown migration

Use Studio `/studio/migration`, `folio call migration.plan/apply/export`, or the corresponding MCP tools for portable content moves. These operations are integrated with the same authenticated core; they are distinct from database schema upgrades and disaster recovery. Review the frozen plan/hash and target identity before importing private drafts, retain the per-item report for partial retries, and keep full backups for history/media/workflows. See [MIGRATION.md](MIGRATION.md).

## v0.2 compatibility and migration

The current logical format is `folio-backup` version 2 with state schema 2. Version 1/schema 1 backups remain supported. Restore and drill results explicitly report `source_schema`, `schema`, and `schema_upgraded`. The strict comparator allows only the declared schema 1→2 upgrade with newly empty workflow maps; it does not ignore proposal, schedule, content, settings or media changes. Core Go code remains authoritative for checksum validation and migration.

Offline archive format stays `folio-offline` version 1. A database upgraded to schema 2 must not be reopened with a v0.1 writer; old binaries reject that schema. To roll back, restore the pre-upgrade offline snapshot with the old binary. Restored schedules retain their pinned snapshots and times. A normal v0.2 daemon scans at startup and roughly every second, so overdue restored plans can publish immediately. Start manual recovery targets with `--pause-schedules` before restore, then inspect the pending plans. Isolated drills automatically pause and verify the scheduler; they never silently resume it. After review, the operator can explicitly stop the paused service and restart normally. Legacy v0.1 drills omit the unsupported flag.

Human CLI text supports Chinese and English via `FOLIO_LANG` or global `--lang zh-CN|en`; machine JSON remains stable. Startup URL detection is language-neutral.

## Offline backup and rollback

Stop the native daemon with SIGTERM and wait for it to exit before using `--stopped`. This flag is an operator assertion, not a process detector:

```sh
python3 ops/ops.py offline-backup --stopped --data /absolute/private/folio-data \
  --output /absolute/private/backups/offline.tar.gz
python3 ops/ops.py verify-offline --backup /absolute/private/backups/offline.tar.gz
python3 ops/ops.py offline-restore --backup /absolute/private/backups/offline.tar.gz \
  --target /absolute/private/new-restored-data
```

Offline archives include the token, database and any WAL/SHM. They are sensitive, mode 0600; restore creates a new 0700 directory and never overwrites an existing instance. Do not make a database-only copy of an active WAL database. For generated Compose deployments, use `offline-backup --compose /path/compose.json` instead of `--stopped`; it stops and verifies the container before copying, then restarts it. The operator must still prevent other writers.

[Upgrade and rollback](../ops/docs/upgrade-rollback.md) use already-present immutable images, a pre-upgrade offline backup, health checks and an operation journal. Failed-version data is preserved. Rollback restores pre-upgrade data; later writes are preserved only in the separate failed-data directory and do not appear in the rolled-back instance. Never claim otherwise. No backup retention deletion, credential creation or remote uploads occur automatically.

## Verified scope

```sh
python3 ops/scripts/check.py
python3 ops/tests/integration.py --binary dist/folio
# Optional: include a trusted old executable for real migration coverage
python3 ops/tests/integration.py --binary dist/folio --legacy-binary /trusted/folio-v0.1
```

The Linux integration suite exercises the actual static binary, content/media recovery and negative cases using only disposable local data. [Integration provenance and results](../ops/docs/integration.md) distinguish those checks from mocked Docker tests. Docker was unavailable in this validation environment; container build/run and live Docker upgrade/rollback remain unverified. Complete them on the target host before deployment.
