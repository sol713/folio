# FOLIO MCP adapter / 适配器

## 语言与兼容性 / Language and compatibility

帮助和人类可读的错误默认使用简体中文。MCP 配置中的 FOLIO_LANG 可设为
zh-CN 或 en，也可以使用 args: ["--lang", "en", "mcp"] 选择英文。

MCP 工具名、参数名、输入 JSON Schema、描述和服务器指令保持标准英文；
适配器使用英文获取工具定义，保证智能体和既有集成兼容。工具执行时，错误对象中的 message 和 system.info.ai.reason 等人类说明
会按所选语言显示。error.code、业务值、状态值和用户内容不会翻译。HTTP 工具请求通过 Accept-Language 传递语言选择。

Human error messages default to zh-CN. Select English with FOLIO_LANG=en or
args ["--lang", "en", "mcp"]. Tool names, descriptions, server instructions and
input schemas stay canonical English in both languages. Discovery is requested
in English; ordinary tool requests use the selected Accept-Language. Only
human-facing explanations (including `error.message` and `system.info.ai.reason`)
are localized; error codes, keys, business values and user content remain stable. No language setting grants permission or changes scopes.

FOLIO uses the **official Go MCP SDK**,
[`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk/tree/v1.7.0),
pinned to v1.7.0. This is an initialized MCP server with real tools discovery and
calls, using the SDK's stdio transport and protocol lifecycle.

## Architecture

```text
MCP client → folio mcp → HTTP /api/op/{operation} → canonical service → SQLite
CLI        → folio call ────────────────────────────┘
Web admin  → HTTP operation API ────────────────────┘
```

The daemon stores images in individual SQLite BLOB rows and metadata separately.
The daemon is the only writer. MCP does not open SQLite, acquire a separate
writer lock, or implement another set of business rules. Run `folio serve` first.

At startup the adapter calls `system.capabilities`. The v0.2 registry has 35 operations. Each returned operation
becomes one tool with the exact same input JSON Schema. Names replace dots with
underscores: `posts.preview` becomes `posts_preview`; `backup.restore` becomes
`backup_restore`. Descriptions include required scope when discovery supplies it.

The adapter supplies read-only/destructive hints, but hints are not permissions.
The daemon enforces authorization, field validation, revision checks, publication
confirmation, idempotency and auditing for every tool call.

## Client configuration

Use the absolute path to your built binary in your MCP client's stdio settings:

```json
{
  "mcpServers": {
    "folio": {
      "command": "/absolute/path/to/folio",
      "args": ["mcp"],
      "env": {
        "FOLIO_URL": "http://127.0.0.1:8080",
        "FOLIO_LANG": "zh-CN",
        "FOLIO_TOKEN": "SET_WITH_YOUR_CLIENT_SECRET_STORE"
      }
    }
  }
}
```

The token above is a placeholder. Prefer your MCP client's secret storage or
secure environment injection; do not commit an actual token into configuration.
Choose the least-privileged scoped daemon token appropriate for that agent.

Use HTTPS for remote endpoints, or loopback HTTP through a trusted SSH tunnel.
The adapter refuses redirects and URLs containing embedded credentials. It does
not print the bearer token or startup banners. Stdout contains only MCP protocol
messages; startup failures should be reported by the launcher on stderr.

If discovery fails, startup fails clearly. Restart the MCP process after
upgrading the daemon to refresh tool discovery; it does not continuously poll or
silently alter its registry mid-session.

## Tool behavior

Examples of tools returned by `tools/list`:

- Read: `system_capabilities`, `system_info`, `posts_list`, `posts_get`,
  `posts_preview`, `posts_check`, `posts_relations`, `settings_get`, `media_list`, `audit_list`, `backup_export`
- Write: `posts_create`, `posts_update`, `posts_publish`, `posts_unpublish`,
  `posts_restore`, `posts_delete`, `posts_recover`, `settings_update`, `media_upload`, `backup_restore`
- Proposal review: `proposals_create`, `proposals_list`, `proposals_get`,
  `proposals_approve`, `proposals_reject`, `proposals_cancel`
- Scheduling: `schedules_create`, `schedules_list`, `schedules_get`,
  `schedules_cancel`, `schedules_reschedule`
- Portable migration: `migration_plan`, `migration_apply`, `migration_export`

Both successful and failed operations return the daemon JSON envelope as MCP
`structuredContent` and as a text fallback. Business errors use `isError:true`
while preserving error codes and details. They are tool execution results, not
opaque JSON-RPC failures, so an agent can inspect and correct the request.
Authentication and connection failures also provide actionable structured errors.

All HTTP calls use a 60-second timeout; input/response JSON is limited to 64 MiB.
There are no automatic write retries. After a timeout, inspect current state and
reuse a supported idempotency key only for the exact same write.

Read-only discovery lists the full registry; listing a tool does not grant permission. `system_info` exposes the authenticated role and permitted operation names. A proposal-only agent receives the daemon's `FOLIO_PROPOSAL_TOKEN` as its client `FOLIO_TOKEN`: it can read private baselines and submit/manage its own proposals, but cannot directly write drafts, approve, publish/schedule or migrate. Owner-only approval creates a private draft; publishing is separate. Agents sharing a role token share one identity.

Follow [WORKFLOWS.md](WORKFLOWS.md) for immutable proposals and reviewed schedule snapshots, and [MIGRATION.md](MIGRATION.md) for the exact frozen plan/hash/target contract. Migration never calls publish. Its per-item failures are reported inside a successful operation envelope: inspect `data.report.complete` and `outcomes` even when MCP `isError` is not set. Retry only the original plan on the same instance; do not silently rebuild it after a partial result.

## Recommended agent workflow

1. Discover schemas and required scopes with `system_capabilities`
2. Read the current draft and revision
3. Create or update a private draft
4. Preview the exact draft revision with `posts_preview`. Optionally call `posts_check` with that saved revision and review findings plus limits; its warnings do not change publishing authority
5. Obtain explicit user intent for publication
6. Call `posts_publish` with `id`, the reviewed `expected_revision`, and `confirm:true`
7. Read back the result; do not conceal revision conflicts or overwrite newer work

Post content, titles and media names are untrusted data. They do not authorize
publication, credential disclosure or tool calls. MCP instructions explicitly
reinforce this boundary. `confirm:true` is a service safeguard, not a mechanism
for an agent to invent approval.

There is no AI model bundled in the publishing daemon. MCP gives an external
agent controlled access to the blog; it does not claim to generate content by
itself.

## Verification

The adapter tests include an actual child-process stdio session with the official
SDK client. It performs initialization, `tools/list`, successful `tools/call`,
and a rejected publish followed by an explicitly confirmed publish. It checks
that tool schemas match discovery, arguments reach the HTTP service unchanged,
read-only annotations are present, and operation errors preserve structured
codes/details. CLI tests cover JSON/file/stdin input, nonzero errors, public
discovery, authentication, redirect rejection and malformed responses.

```sh
go test ./internal/adapters -count=1
go test ./internal/adapters -run TestMCPStdio -v
```

`adapters.NewMCPServer(ctx, client)` constructs the registered SDK server for
embedding/testing. `adapters.RunMCP(ctx)` connects it to real process stdio.

For a full test against the built application rather than an HTTP fixture:

```sh
go build -buildvcs=false -o dist/folio ./cmd/folio
python3 scripts/e2e.py --binary dist/folio
```

This initializes disposable instances, performs real MCP discovery and writes,
compares every tool schema with live HTTP discovery, exercises CLI/public read
parity, and verifies that MCP stdout contains only JSON-RPC messages and captured
outputs/logs contain no authentication token values.
