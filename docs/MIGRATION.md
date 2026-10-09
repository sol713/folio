# Markdown 迁移 / Portable Markdown migration

FOLIO v0.2 已将 Markdown 迁移接入真实的 HTTP API、CLI、MCP 和 Studio。
先预检、审阅冻结计划与目标实例，再明确确认；所有成功项都只写入私有草稿。
源文件里的 `draft:false`、`date` 或 `publishDate` 不会发布文章或创建定时计划。

The integrated operations are `migration.plan`, `migration.apply` and
`migration.export`, all owner/admin-only. Studio provides the same workflow at
`/studio/migration`; CLI uses `folio call`; MCP exposes `migration_plan`,
`migration_apply` and `migration_export`. They use `internal/core/migration.go`
and the standard-library `internal/migration` package, not a separate database
writer or a simulated import. Discover the current schemas before automating.

## What is portable

- UTF-8 Markdown with optional YAML `---` or TOML `+++` frontmatter
- Title, ASCII slug, excerpt (`description`/`summary`/`excerpt`), tags, one category,
  an existing local `/media/` cover reference and a featured flag
- Private source provenance: relative source path, normalized source `date` and
  `publish_date`, original draft flag, unsupported flat metadata, warnings and
  source-content hash
- Plain Markdown without frontmatter: a title is inferred from the first H1
  outside code fences or the filename; missing slugs use a safe filename-based
  fallback, including deterministic hashes for names without ASCII characters

This is a deliberate frontmatter subset, not full YAML/TOML, a Hugo site
converter or an Obsidian renderer. Nested maps/tables, aliases/anchors and
complex arrays are unsupported. Unknown flat fields are retained as source
literals with warnings. JSON frontmatter is treated as Markdown, not parsed
metadata. BOM is accepted; CRLF/CR become LF. Source date-only or timezone-free
values use UTC, never the operator's inferred local timezone. For exact grammar
and underlying package limits see the [package reference](../internal/migration/README.md).

A source date is provenance, not FOLIO's `created_at` or `published_at`.
Importing an old article does not backdate its creation, make it public or
schedule it. Publishing it later records the actual activation time. Source
metadata stays out of anonymous post responses and remains available in full
backups and portable export manifests.

Ordinary links, relative image references, `[[wiki links]]` and `![[embeds]]`
remain text. Image references generate warnings; no attachment is copied,
uploaded, rewritten or fetched. A cover reference does not prove that the asset
exists. Review links and separately migrate permitted media as needed.

## Plan and review without writes

The product API accepts **1–200 files and at most 8 MiB of source text** per
plan. Each Markdown body is limited to 1 MiB, plus the parser's bounded
frontmatter allowance. Paths are safe relative provenance labels, not server
filesystem paths. Neither API nor MCP scans a directory on your computer.
Studio uploads selected `.md`/`.markdown` file contents with these same limits.

Configure `FOLIO_URL` and the owner `FOLIO_TOKEN` securely. This example uses
optional `jq` to read one local file and write private JSON request/response files:

```sh
umask 077
jq -n --rawfile body ./essay.md \
  '{files:[{path:"essay.md",content:$body}],conflict:"error"}' > migration-input.json
folio call migration.plan --file migration-input.json > migration-plan-envelope.json
jq '.data | {target_instance_id,can_apply,summary,validation_issues,plan}' migration-plan-envelope.json
```

The response contains `plan`, `target_instance_id`, `can_apply`, `summary` and
`validation_issues`. Read both `plan.diagnostics` and `validation_issues`, plus
each document's `warnings`; `can_apply:true` is required before proceeding.
Parsing errors, duplicate source paths/slugs, invalid metadata and reserved
live slugs must be resolved. Source/instance diagnostics are frozen into the
plan checksum; applying an exact rejected (`can_apply:false`) preview fails
before writes. Do not discard errors to make a partial selection
look like the original reviewed batch.

Choose the conflict policy before planning:

- `error` (default): an existing draft slug blocks that item and application
- `skip`: retain the existing post and explicitly skip its matching source
- `update`: replace **every editable field** of the matching private draft at
  its pinned `target_id` and `expected_revision`; absent source fields become
  empty/default values. This is not a merge. Its current live snapshot stays
  unchanged

Review the full candidate bodies, source warnings, actions, replacement target
IDs and revisions. Preserve the exact `plan.id` hash and the full plan together
with `target_instance_id`. The hash detects plan changes; it is not a signature,
a permission grant or proof that untrusted source instructions are safe. The
owner's approval must refer to that exact plan and destination. If content,
policy or destination intentionally changes, obtain a fresh plan and review.

## Apply only the reviewed plan

After explicit review/approval, build the request from the saved response;
do not rescan changing files or reconstruct entries immediately before applying:

```sh
jq -e '.ok == true and .data.can_apply == true' migration-plan-envelope.json
# Continue only if the check above succeeds and the exact plan is approved.
jq '.data | {plan,target_instance_id,confirm:true,continue_on_error:false}' \
  migration-plan-envelope.json > migration-apply.json
folio call migration.apply --file migration-apply.json > migration-result.json
jq '.data.report' migration-result.json
jq -e '.ok == true and .data.report.complete == true' migration-result.json
```

The server checks plan/hash integrity and the exact local instance ID before
writing. Logical backup restore does not transfer that local identity. Each
write goes through ordinary authenticated `posts.create` or `posts.update`
validation, revision CAS, audit and durable idempotency. Import provenance
commits in the same transaction as its draft. Nothing is published or scheduled.

### Partial results and retries

The batch is **not atomic**. Each outcome is `applied`, `skipped`, `failed` or
`pending`. By default a failed item stops subsequent writes, leaving them
pending; explicit `continue_on_error:true` attempts later items. Already
committed private drafts remain. There is no compensating deletion or rollback.

A response envelope can be `ok:true` with `report.complete:false`; this also
means the CLI can exit zero or MCP can return without `isError:true` despite an
incomplete import. Always inspect `report.complete` and every outcome. A failed
or lost receipt can mean its write already committed. Preserve the plan and
report before closing Studio; its in-memory review is not durable browser storage.

Retry the identical saved `migration-apply.json` against the same instance and
actor. Its fixed per-entry keys allow previously committed receipts to be
returned without creating duplicate posts or revisions. Do not regenerate keys,
re-plan a partially applied batch, or replay it after a restore that discarded
the retry cache. If a genuine concurrent-edit conflict remains, inspect actual
state and obtain a separately reviewed resolution instead of blindly retrying
or overwriting the newer draft. Ordinary reads/preview establish the outcome;
publication remains a separate confirmed workflow.

## Export portable content

```sh
umask 077
folio call migration.export --json '{"status":"all"}' > markdown-bundle-envelope.json
```

`data` is a `folio-markdown-bundle` version 1 object: `files` contain relative
output `path`, Markdown `content`, `sha256` and `bytes`; `manifest` contains
`folio-markdown-export` version 1 entries with retained source provenance.
Studio downloads this same portable data as a JSON bundle, not a ZIP or a set of
already-extracted filesystem files. The API performs no filesystem writes.

Selection supports at most 200 posts, optionally narrowed by `ids`:

- `all` (default): current non-trashed drafts, including private changes
- `draft`: current non-trashed drafts whose status is not `published`, including
  `changed` drafts
- `published`: exact live snapshots only, never a newer private draft

Every exported Markdown file explicitly says `draft: true`, including live
articles. Original draft status, unknown metadata, source paths and warnings
remain in the manifest. Reimporting only `.md` files cannot recover fields that
exist only in that manifest; keep both. The package has manifest validation
helpers, but the product import endpoint accepts Markdown `files`, not a
manifest-restoration request.

Portable export omits media bytes, revision history, settings, proposals,
schedules and credentials. It is not a disaster-recovery backup. Use
`backup.export` and the [operations guide](OPERATIONS.md) for full recovery;
start restore targets with `--pause-schedules` before importing backups that
may contain overdue publication intent. Protect all migration plans, reports,
exports and backups as private authoring data.
