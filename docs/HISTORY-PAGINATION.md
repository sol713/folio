# Saved history pages / 已保存历史分页

## English

`posts.history` is the 37th operation. It reads private saved-revision metadata
through the same HTTP, CLI and MCP registry, with the existing `read` scope.
Owner, draft, read and proposal roles can use it; anonymous readers cannot.
It never writes content, audit, retry receipts or publication state.

```json
{"id":"ARTICLE_ID","revision":24,"limit":8}
```

`id` and positive **current saved draft** `revision` are required. `limit` is an
integer from 1 to 50, default 8. The optional positive `before_revision` cursor
is exclusive: only stored revisions with smaller numbers qualify. It must not
exceed the current head. Rows sort by revision number descending, independently
of timestamps or the original order of a sparse imported history.

The response has `id`, `revision`, `published_revision`, `instance_id`,
`instance_revision`, `total`, `limit`, `before_revision`, `has_more`,
`next_before_revision`, and `revisions`. Each row has exactly `revision`,
`created_at`, `updated_at`, `current`, and `published`. No historical Markdown,
HTML, title, slug, excerpt, tags, category, cover or migration provenance is sent.
Only valid RFC3339 dates of at most 64 bytes are exposed; invalid/oversized legacy
dates become empty metadata values without changing any stored snapshot.

When `has_more` is true, reuse the same current `revision` and use
`next_before_revision` for the next request. When false, that cursor is zero and
must not be sent as a positive cursor. Omit the cursor to request the first page.
An empty tail is a successful page with no rows. `total` counts retained history
rows, not the largest revision number. A current/live snapshot missing from
sparse history is not synthesized into the page; comparison still supports it.

New content revisions, including restore, make the old anchor fail with
`conflict` / HTTP 409 instead of silently mixing pages. Clients must obtain a
new saved head and restart pagination. Invalid types, nulls, bounds, cursors and
unknown fields yield `validation` / HTTP 400. Missing private posts yield 404
only after authorization. Anonymous requests get the same 401 regardless of ID;
the existing cross-origin policy remains in force. Publication can change the
live flags without creating a new content revision; flags describe each read.

```sh
folio --lang en call posts.history --json '{"id":"ARTICLE_ID","revision":24,"limit":8}'
folio --lang en call posts.history --json '{"id":"ARTICLE_ID","revision":24,"limit":8,"before_revision":17}'
folio --lang en call posts.get --json '{"id":"ARTICLE_ID","include_revisions":false}'
```

HTTP uses `POST /api/op/posts.history`; MCP uses `posts_history` with the same
payload and read-only annotation. Selected full content remains available via
`posts.preview` and `posts.compare`. The optional boolean `include_revisions`
on `posts.get` and `posts.preview` defaults to **true** for old-client compatibility.
False preserves `post`, rendered `html` and `live`, returns `revisions: []`, and
adds `revision_count`. It does not alter the exact selected version or privacy.

Studio uses light reads and eight metadata rows per page. Compare selectors use
the current page plus current, live and selected versions. Older/Newer requests
update only the history region; typing, selections and unsaved input stay intact.
The comparison window also pages metadata while retaining both selected versions,
so two old snapshots on different pages remain directly comparable. Only one
page request runs at a time. Cancel invalidates its result and offers Try again;
the prior page/cursors remain available. Cancelling a post-save automatic refresh
also leaves an explicit retry to retrieve the new head’s first page. An article,
route, editor, token, instance or saved-head change invalidates late successes
and errors. Local save/restore resets pages and reads the new head. A 409 leaves
the editor and its original CAS base intact and offers an explicit reopen;
unsaved changes still require the existing discard confirmation. No unseen
external edit becomes the base of a subsequent Save. Compare/restore keep their
original dirty guards, current-head CAS and exact retry receipts.

A real 404 marks history unavailable and disables old history actions while
retaining the editor and original CAS base. Export unsaved edits before an explicit
reopen; the existing discard confirmation still applies. Delete then recover
creates a new saved revision, so an old anchor returns 409 until reopened.
A successful read completed before deletion can still display that older snapshot;
there is no push invalidation, and the next read detects the deletion. First create
commits its article route before starting metadata refresh. Save/restore/publish completion
refreshes sidebar controls when the write lock ends, including explicit errors;
it does not rebuild editable fields.

An already-open comparison also disables a new Restore after a history 409/404
and explains that saved history changed or the story is unavailable. Its reviewed
content, editor fields and selection remain intact; Markdown export and explicit
reopen still work. An unconfirmed restore keeps its exact retry available so a
lost receipt can be resolved without creating another restore.

This is response pagination. SQLite still stores and loads full revision arrays
inside each post record, under the existing 64 MiB logical-instance limit. It
does not normalize revision storage, paginate the whole author catalog, or bound
every legacy field in an individually selected full snapshot. Those costs remain.
No publishing workflow, provider, dependency, permission or public route is added.

Reproduce with a built binary:

```sh
python3 scripts/history_pagination_e2e.py --binary dist/folio --report /tmp/folio-history-api.json
python3 scripts/history_pagination_e2e.py --binary dist/folio --chromium /path/to/chromium --report /tmp/folio-history-browser.json
python3 scripts/history_lifecycle_e2e.py --binary dist/folio --chromium /path/to/chromium --report /tmp/folio-history-lifecycle.json
```

## 中文

新增第 37 个操作 `posts.history`，复用 HTTP/CLI/MCP 的私有 `read` 权限，只读，
不保存、恢复、发布或写审计。Owner、draft、read、proposal 可读取；匿名不可读取。
必填文章 ID 和正数的**当前已保存草稿版本** `revision`；页大小 `limit` 默认 8，
合法范围 1–50。可选正数 `before_revision` 为不含自身的修订号游标，不得超过
当前草稿版本。按修订号倒序，时间戳相同、历史稀疏或导入顺序变化均不影响顺序。

响应提供当前/线上版本、实例身份和版本、保留行数 `total`、分页参数、
`has_more`、`next_before_revision` 和元数据行。每行仅含修订号、创建/保存时间、
是否当前、是否线上，不传历史正文、HTML、其他可编辑内容或迁移来源。旧备份的
无效或超过 64 字节的时间只在元数据中显示为空，原快照及旧式读取保持不变。

有下一页时保持同一个 `revision`，使用返回的 `next_before_revision`；结束游标为
0，不得当作正数发送。第一页省略游标；空末页正常返回。`total` 是真实保留行数，
不是最大修订号。历史中缺失的当前/线上快照不伪造为历史行，仍可通过原比较读取。

新保存或恢复生成新内容版本后，旧分页锚点返回 409；重新取得当前版本后从首页
开始。非法类型、null、边界、游标和未知字段返回 400。身份校验先于文章查找，
匿名请求无论 ID 是否存在均为 401；跨源保护保持。发布不生成内容版本，线上
标志反映每次实际读取时的状态。HTTP 为 `/api/op/posts.history`，MCP 名为
`posts_history`，CLI 示例见上。

`posts.get` / `posts.preview` 新增可选布尔值 `include_revisions`，默认 true，
保留旧客户端的全量格式。false 只省略历史数组（返回空数组及 `revision_count`），
当前/选定文章、已渲染 HTML 和线上快照不变。完整选定内容继续由 preview/compare
读取。Studio 使用轻量读取和服务端每页 8 行；比较选择器含本页、当前、线上和
已选版本。翻页只更新历史侧栏，不重建编辑字段；取消、文章/路由/会话/实例/
已保存版本变化使旧响应失效。新保存/恢复重置分页。409 保留输入及原始 CAS 基线，
明确提示重新打开；未保存内容仍需既有的放弃确认，不自动把外部改动设成保存基线。
比较窗口也可翻阅元数据，并保留来源/目标两个已选版本，可直接比较跨页的两个旧版本。
取消后保留原页面及游标，提供明确重试；保存后的自动刷新即使取消也能重新加载首页。
保存/恢复/发布结束（包括明确失败）会解除侧栏按钮的禁用，不重建编辑字段。首次创建先
切换为文章路由，再启动历史刷新；比较/恢复的脏稿保护、CAS 和精确幂等回执保持。

真实 404 会把历史标记为不可用、禁用旧历史操作，但保留输入及原 CAS 基线；导出
未保存输入后再明确重新打开，放弃确认仍有效。删除再回收产生新版本，旧锚点返回
409。删除前已经成功完成的读取仍可能显示旧快照；没有推送失效通知，下次读取
才发现删除。

已经打开的比较窗口在历史读取返回 409/404 后也会禁用新的恢复操作，并明确提示
版本已变化或文章不可用；比较内容、编辑字段和选区保持，仍可导出 Markdown 后
明确重新打开。尚未确认的恢复请求仍可按原请求精确重试，避免回执丢失造成重复恢复。

本轮分页限制的是**响应传输**。SQLite 仍在单条文章记录内保存、加载完整历史，
64 MiB 逻辑实例上限不变；没有进行修订表迁移、作者目录分页或给所有旧版单快照
字段新增上限。没有新增发布流程、依赖、外部提供商、权限或公开历史接口。
