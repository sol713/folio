# FOLIO

**A considered home for your words. A dependable workspace for your agents.**

FOLIO 是一个从零实现的 Go 个人创作与发布工作台：安静、有辨识度的读者界面，真正可用的 Markdown Studio，以及共享同一套业务逻辑的 HTTP API、JSON CLI 和官方 MCP SDK 工具。

当前为 **v0.2 预生产版本**：完整的简体中文／英文界面、可审阅的 Markdown 批量迁移、不可变智能体提案与所有者审批，以及固定审阅版本的持久化定时发布。迁移和批准提案都只写私有草稿；立即发布或定时发布仍需单独确认。

This is functional **v0.2 pre-production software**, not a claim of feature parity with Halo or production readiness. It adds zh-CN/en presentation, portable Markdown migration, owner-reviewed immutable proposals, and durable revision-pinned publishing schedules. Content ownership, explicit publishing and recoverable changes remain the foundation.

## Start locally

Requires Go **1.25+** to build. The resulting binary needs no Go, Node, database server, or external account.

```sh
go build -trimpath -buildvcs=false -o folio ./cmd/folio
./folio init --data ./data --demo
./folio serve --data ./data
```

Open `http://127.0.0.1:8080` for the journal and `/studio` for authoring. Read `data/token` locally and enter it into Studio. The token stays in that tab's sessionStorage, never in a URL. `--demo` is optional and adds six clearly fictional essays; omit it for an empty blog. Do not ship your data directory or token with source code.

For a provided Linux binary: `chmod +x folio-linux-amd64`, then replace `./folio` above with `./folio-linux-amd64`.

## Agent first, author in control

```sh
export FOLIO_URL=http://127.0.0.1:8080
export FOLIO_TOKEN="$(cat data/token)"
./folio capabilities
./folio call posts.create --json '{"title":"My first idea","slug":"my-first-idea","markdown":"A small beginning.","tags":["idea"],"idempotency_key":"first-idea-001"}'
# Use the ID and exact revision from the response:
./folio call posts.preview --json '{"id":"RETURNED_ID","revision":1}'
./folio call posts.publish --json '{"id":"RETURNED_ID","expected_revision":1,"confirm":true}'
./folio mcp
```

`folio mcp` is a real stdio Model Context Protocol server implemented with the official Go SDK. It exposes discovered operation schemas as individual tools. It connects to the running daemon, so CLI, browser and MCP never implement conflicting write paths. See [CLI](docs/CLI.md), [MCP](docs/MCP.md), and [agent playbook](docs/AGENT_PLAYBOOK.md).

For least-privilege agents, configure distinct random 32+ character `FOLIO_PROPOSAL_TOKEN`, `FOLIO_DRAFT_TOKEN` and/or `FOLIO_READ_TOKEN` on the daemon, and inject the selected credential as the client's `FOLIO_TOKEN`.

- Proposal-only agents can read private authoring baselines and submit, inspect or cancel their own proposals; they cannot edit drafts, approve proposals, upload images, migrate content or publish
- Draft agents can edit private drafts and submit proposals, but cannot approve, publish/schedule, trash/recover, change settings, inspect audit history, migrate or export/restore backups
- Read tokens can inspect authoring content but cannot write. They are not public-reader credentials
- The owner/admin reviews and approves proposals into private drafts, then separately confirms immediate or scheduled publication. One configured token per role means agents sharing a token share an identity, not isolated accounts

See [review and scheduling workflows](docs/WORKFLOWS.md) and [migration](docs/MIGRATION.md).

## What works

| Area | v0.2 |
|---|---|
| Reading | Responsive editorial homepage and articles; topic/tag views; search including short CJK queries; RSS; sitemap; canonical/OG tags; server-rendered public fallback |
| Authoring | Markdown drafts, private preview, immutable content revisions, revision restore, explicit publish/unpublish, trash/recovery |
| Safety | Live snapshot isolated from unsaved/unpublished revisions, compare-and-swap updates, retry keys, role scopes, audit trail |
| Assets | Sniffed PNG/JPEG/GIF/WebP upload, 5 MiB limit, content-addressed SQLite BLOB storage; SVG/HTML rejected |
| Operations | Single binary, init, healthcheck, version, JSON capabilities; consistent backup/restore; Python standard-library recovery drills and secure Docker/Compose generator |
| Agent interfaces | Shared typed service, discoverable JSON schemas, structured errors, JSON CLI, official MCP stdio tools |
| Writing experience | Idea capture, keyboard command palette, Markdown toolbar and list continuation, editor-buffer .md import/export, focus mode, unsaved-change warnings, revision-aware publication review |
| Language | zh-CN default and English switch across reader/Studio, dialogs, accessible labels and human CLI/API errors; authored content and machine schemas remain unchanged |
| Migration | Reviewed frozen plan/hash and target instance; YAML/TOML frontmatter subset; create/skip/explicit draft replacement; per-item retry reports; portable Markdown plus provenance manifest |
| Review | Immutable agent proposals, plain-text changes, owner approve/reject, stale-base protection; approval only creates/updates private drafts |
| Scheduling | Explicit future publication of a pinned snapshot, cancellation/rescheduling, restart catch-up, audit and backup persistence; paused restore review |

All **33 operations** exposed by the Go API have matching CLI and MCP interfaces (19 original operations, six proposal, five schedule and three migration operations). The graphical Studio emphasizes everyday creation; advanced operations remain discoverable even if they have no dedicated button. Process bootstrap (`init`, `serve`, `healthcheck`), installation, Docker lifecycle and offline filesystem recovery remain explicit CLI/Python workflows: the MCP server must already be installed and running before an agent can call it.

## 中文 / English

Use the 中文 / EN switch in the journal, Studio or sign-in screen. The preference persists locally; switching language keeps editor text, selection and pending work intact. The server-rendered shell follows the locale cookie. This changes presentation, never translates article text or changes permissions.

CLI: `folio --lang en --help` or `FOLIO_LANG=en folio --help`; default is `zh-CN`. MCP names, input schemas and descriptions stay canonical English. See [frontend](docs/FRONTEND.md), [CLI](docs/CLI.md) and [MCP](docs/MCP.md) for the exact contract.

## Honest boundaries

- Single-author personal publishing, not a multi-tenant CMS, plugin marketplace, commerce or membership platform
- No built-in language-model provider calls, fake AI generator, or hidden usage charge. Bring your own agent through MCP
- MCP stdio transport is shipped; authenticated remote Streamable HTTP MCP and OAuth are future work
- No browser-local draft recovery. Save drafts to the server before closing the tab; unsaved-change warnings protect navigation. Markdown file export includes the current editor buffer; full JSON backups include settings and revision history
- No comments, email newsletter delivery, analytics tracking or remote URL importing. Scheduled publishing requires a running daemon; overdue approved snapshots catch up after restart
- Markdown migration is a bounded frontmatter subset, not a complete Hugo/Obsidian site converter. It preserves source-date provenance rather than backdating actual publication, does not fetch/copy attachments, and never publishes automatically
- Migration is per-item, not one atomic batch: committed private drafts remain after a later failure. Preserve and retry the exact reviewed plan against the same instance; inspect every outcome
- Proposals and schedules use fixed role tokens, not per-agent accounts, expiring grants or a multi-user approval system
- Search is a bounded in-memory Unicode substring scan, not a large-scale full-text engine
- Current logical dataset cap: **64 MiB** including revision history/base64 backup representation; one Markdown revision up to **1 MiB**; one media image up to **5 MiB**. SQLite writes are per-post/per-asset, but the small-site catalog remains cached in memory. Do not claim multi-gigabyte media-library scalability
- Uploaded media URLs are public immutable assets. Do not upload confidential images, even when attaching them to a private draft
- Audit history retains the most recent 10,000 events; retry records are retained until logical export/restore
- A valid local backup contains private drafts, media, proposals, pinned schedules and import provenance. Protect and encrypt backup storage yourself. Start recovery with `serve --pause-schedules` and verify `system.info.scheduler.paused` before restoring, so overdue schedules cannot activate during review
- Public deployment requires TLS, trusted host configuration, protected backups and operational review. No external server has been deployed by this project

## Development and verification

```sh
go test ./...
go test -race ./...
go vet ./...
go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio
python3 scripts/e2e.py --binary ./dist/folio
python3 scripts/workflows_e2e.py --binary ./dist/folio
node --check web/app.js
node --check web/i18n.js
for test in web/tests/*.test.cjs; do node "$test"; done
```

See [architecture](docs/ARCHITECTURE.md), [security](docs/SECURITY.md), [verification record](docs/VERIFICATION.md), and [GitHub research/provenance](docs/INSPIRATION.md). The verification record preserves the v0.1 coverage history and distinguishes later checks; historical browser screenshots or benchmarks are not automatically v0.2 coverage.

The [operations toolkit](docs/OPERATIONS.md) adds non-root Docker/Compose generation, diagnostics, paused online recovery drills, offline backup validation and upgrade/rollback tooling. Consult its [integration record](ops/docs/integration.md) for executed checks. Docker container build/run and live upgrade/rollback require target-host verification; configuration tests are not runtime verification. No public GitHub release or production deployment is claimed.

## License

FOLIO source is MIT licensed. Upstream dependencies retain their own licenses. Design research informed interaction principles; no upstream theme or component source was copied into the original frontend. See [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md).
