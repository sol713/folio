# FOLIO CLI / 命令行

## 语言 / Language

FOLIO 默认使用简体中文（zh-CN）显示帮助和错误说明。设置 FOLIO_LANG=en
可切换为英文；单次调用也可在命令前传入 --lang en 或 --lang zh-CN：

```sh
folio --help
FOLIO_LANG=en folio --help
folio --lang en call posts.list --json '{}'
folio --lang=zh-CN healthcheck
```

优先级为命令行 --lang、FOLIO_LANG、默认 zh-CN。显式 --lang 仅接受 zh-CN
或 en，无效值会输出稳定的 invalid_locale 错误并以非零状态退出。未设置或
无法识别的 FOLIO_LANG 会回退到 zh-CN；环境变量也支持 zh、zh-Hans、en-US
和 en-GB 等别名。请将 --lang 放在子命令前，并且只指定一次。

仅人类可读的帮助和 error.message 会翻译。JSON 键、操作名、错误码、状态值、
业务成功值、用户正文和 MCP 结构定义保持原样，脚本应检查 error.code。
system.info.ai.reason 这种仅供人阅读的说明也会本地化。
HTTP 操作请求和健康检查会发送所选语言的 Accept-Language 标头。

Help and human-readable error messages default to Simplified Chinese. Select
English with FOLIO_LANG=en or a leading --lang en. A flag overrides the
environment; an unsupported environment value falls back to Chinese, while an
invalid explicit flag fails with code invalid_locale. Both --lang en and
--lang=en are supported. Language changes never rename machine keys, operation
names, status values, error codes, content or schemas. The human-only
`system.info.ai.reason` explanation is also localized. Check error.code in
scripts rather than matching translated text.

The CLI is a client of the running FOLIO daemon. It never opens SQLite or writes
media directly. The browser admin, HTTP API, CLI and MCP use the same operation
registry, validation, scoped tokens, concurrency guards and audit trail.

## Connect

Initialize once with `folio init`, then start the daemon with `folio serve`.
The initializer writes the owner token to the private `data/token` file; inject
it through your secure environment without printing or committing it. In a
separate shell, configure the client:

```sh
export FOLIO_URL=http://127.0.0.1:8080  # default
# Set FOLIO_TOKEN from your secure environment or secret manager.
# Do not paste real tokens into checked-in files, issue reports or shell scripts.
folio capabilities
folio call system.info
```

`FOLIO_TOKEN` is a bearer token configured on that daemon. Use the least-privileged
token sufficient for your workflow. Capabilities discovery is public and does
not disclose the token. All other operations require authentication.

Remote URLs must use HTTPS. For an SSH tunnel or a published Docker host port,
use a loopback HTTP URL (`127.0.0.1`, `[::1]` or `localhost`). URLs containing
credentials, query strings or fragments are rejected. Redirects are not followed.

## Commands and output

```text
folio [--lang zh-CN|en] COMMAND ...
folio serve --data ./data [--pause-schedules]
folio capabilities
folio healthcheck [--url http://127.0.0.1:8080/healthz]
folio call OP
folio call OP --json '{"key":"value"}'
folio call OP --file input.json
folio call OP --file -
folio mcp
folio --help
```

- `capabilities` calls `system.capabilities` and returns the live registry
- `healthcheck` makes an unauthenticated GET to `/healthz`, requires HTTP 200 and
  JSON `status:"ok"`, and fails after 3 seconds. It never sends `FOLIO_TOKEN`. The
  default uses `FOLIO_URL` (or the default daemon URL); `--url` sets the exact health
  endpoint. This works inside a scratch container without curl or a shell
- Omitting the input uses `{}`; `--file -` reads one JSON object from stdin
- Choose one input source. Malformed JSON, arrays, `null`, empty input and multiple
  JSON values are rejected before reaching the daemon
- Each CLI operation writes one compact JSON envelope plus newline to stdout
- Success is `{"ok":true,"data":...}` and exits zero
- API and local errors are `{"ok":false,"error":...}` and exit nonzero
- Daemon errors retain their detailed structured JSON; transport/auth failures
  use actionable adapter errors such as `connection_failed` and `authentication_failed`
- Help goes to stderr. MCP reserves stdout exclusively for protocol messages

Adapters time out requests after 60 seconds and accept at most 64 MiB of input
or response JSON. They never retry mutations automatically. A lost connection or
timeout does not prove a mutation failed: read state before deciding to retry.

## Canonical schemas

Always use discovery as the source of truth:

```sh
folio capabilities | jq '.data.operations[] | {name, scope, read_only, input_schema}'
```

The v0.2 registry contains 36 operations:

- `posts.list`, `posts.get`, `posts.create`, `posts.update`, `posts.preview`, `posts.check`, `posts.relations`, `posts.compare`
- `posts.publish`, `posts.unpublish`, `posts.restore`, `posts.delete`, `posts.recover`
- `settings.get`, `settings.update`
- `media.list`, `media.upload`
- `audit.list`, `backup.export`, `backup.restore`
- `system.info`, `system.capabilities`
- `proposals.create`, `proposals.list`, `proposals.get`, `proposals.approve`, `proposals.reject`, `proposals.cancel`
- `schedules.create`, `schedules.list`, `schedules.get`, `schedules.cancel`, `schedules.reschedule`
- `migration.plan`, `migration.apply`, `migration.export`

`posts.preview` and `posts.check` are read-only. `posts.check` requires an explicit current saved `revision`; see [content-check semantics](CONTENT-CHECK.md). `posts.relations` shares its private-read scope and current saved revision guard; see [article links](ARTICLE-RELATIONS.md). `posts.compare` compares two exact saved snapshots against the current saved revision; [revision review](REVISION-REVIEW.md) documents all eight fields, limits and safe restore. Post changes stay private until an explicit publish.
Post mutation concurrency uses `expected_revision`, except for creation. Media
upload and empty-instance backup restoration do not need a revision. Settings
updates use the settings revision. Authorization is enforced by the daemon,
regardless of the chosen interface.

## Safe draft → preview → publish

Create a draft (this never publishes it):

```sh
folio call posts.create --json '{
  "title":"A small beginning",
  "slug":"a-small-beginning",
  "markdown":"# A small beginning\n\nOne considered idea at a time.",
  "excerpt":"Notes on starting small.",
  "tags":["notes"],
  "category":"Journal",
  "idempotency_key":"draft-a-small-beginning-001"
}'
```

Use the returned post ID and revision; the following values are illustrative:

```sh
folio call posts.get --json '{"id":"POST_ID"}'
folio call posts.preview --json '{"id":"POST_ID","revision":1}'
# Only after the user approves publishing this exact revision:
folio call posts.publish --json '{"id":"POST_ID","expected_revision":1,"confirm":true}'
```

`expected_revision` is a compare-and-swap guard. A concurrent edit makes stale
writes fail rather than overwriting content. Fetch and review the new draft
before retrying. Publishing, unpublishing and moving a post to trash require
`confirm:true`; do not treat that field as a substitute for the user's intent.

`posts.update` replaces draft fields and requires `id`, `expected_revision`,
`title`, `slug` and `markdown`. Published content remains the previous snapshot
until you publish the reviewed new revision.

Reuse an `idempotency_key` only for an identical retry of a supported write; use
a new key for changed arguments. Read the daemon's response before retrying.

## Proposals, schedules and portable migration

Use the distinct daemon `FOLIO_PROPOSAL_TOKEN` as a proposing client's `FOLIO_TOKEN` when it should suggest changes without editing drafts. It can read baselines and its own proposals; owner approval is a separate operation. All six proposal and five schedule operations are described with payloads and revision semantics in [WORKFLOWS.md](WORKFLOWS.md). There is no separate publisher token in this release; publisher-scoped operations require the owner/admin credential.

All three migration operations are admin-only and work through ordinary `folio call` commands. [MIGRATION.md](MIGRATION.md) shows plan → review → confirmed apply, per-item report handling and portable export. There is no shipped `folio migrate` or `folio-migrate-plan` command. The API accepts source file contents and safe relative provenance paths, not a server directory to scan.

A complete HTTP response to `migration.apply` can contain `report.complete:false` while the CLI envelope is `ok:true` and its process exits zero. Inspect `data.report.complete` and each outcome before declaring success. A lost response does not mean nothing was written. Retain the exact plan, target ID and per-entry retry keys for retry against the same instance.

## Restore a content revision

Restoring history makes a new draft and preserves the live publication:

```sh
folio call posts.restore --json '{"id":"POST_ID","revision":1,"expected_revision":3}'
```

`posts.delete` moves a post to trash and retains its history. List trashed posts
and recover one as a private draft with the current revision:

```sh
folio call posts.list --json '{"status":"trash"}'
folio call posts.recover --json '{"id":"POST_ID","expected_revision":3,"confirm":true}'
```

Recovery never republishes automatically. Preview and explicitly publish if
needed. Ordinary `posts.list` omits trashed posts.

## Media and backups

`media.upload` takes `name` and standard base64 image bytes in `base64`. Prefer a
JSON file over shell arguments for image data. The daemon sniffs PNG, JPEG, GIF
and WebP, rejects SVG, and limits original image bytes to 5 MiB. Image bytes are
stored as individual SQLite BLOB rows, so editing a post does not rewrite
unchanged image BLOBs. The process currently caches state and media in memory.
The API uses base64 for upload and logical backup transport.

Export a logical backup to a private location:

```sh
umask 077
folio call backup.export > backup-envelope.json
# jq is optional; it is used here to build the canonical restore input.
jq '{backup:.data,confirm:true}' backup-envelope.json > restore-input.json
# Start an empty target daemon with: folio serve --data /new/data --addr 127.0.0.1:8081 --pause-schedules
# Point FOLIO_URL/FOLIO_TOKEN at that target and verify system.info.scheduler.paused=true.
folio call backup.restore --file restore-input.json
```

Backups include private drafts, history, media, proposals, pinned schedules and import provenance. Protect them accordingly. Restoration validates the checksum and only works on an empty instance, including empty workflow catalogs. Version 1/schema 1 backups remain accepted; current exports are version 2/schema 2.

A restored overdue schedule can publish on the next normal scheduler scan. Keep the replacement daemon paused while inspecting `schedules.list`; cancel/reschedule intentionally if needed. Restart without `--pause-schedules` only when ready to resume. The flag pauses automatic work for that process, not explicit manual publishing or durable publication intent. Review the destination before setting `confirm:true`.

## Integration contract

`adapters.RunCLI(ctx, args, stdin, stdout, stderr) error` takes arguments excluding
the executable name. Main must exit nonzero when it returns an error; the CLI has
already written the structured operation error, so do not write a second JSON
value to stdout. Route `serve` in main to the daemon. `adapters.RunMCP(ctx)` owns
real process stdin/stdout when routing the `mcp` command directly.

## Real-binary lifecycle verification

The disposable end-to-end harness uses only Python standard-library modules. It
launches actual daemon and MCP subprocesses, creates its own temporary databases
and tokens, and cleans them up. It never points at an existing instance.

```sh
go build -buildvcs=false -o dist/folio ./cmd/folio
python3 scripts/e2e.py --binary dist/folio
```

It checks cross-interface draft/publish/restore behavior, scoped tokens, revision
conflicts, media bytes, logical backups, restart persistence, draining an in-flight
request during SIGTERM, and token-free logs.
