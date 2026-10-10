# Saved revision review / 已保存版本审阅

## English

FOLIO can compare saved article revisions through Studio, HTTP, CLI and
MCP. This closes the gap between a history number and an informed private restore.
`posts.compare` is the 36th operation; it never saves, restores or publishes.

A request supplies **all four** fields:

```json
{"id":"ARTICLE_ID","revision":2,"from_revision":2,"to_revision":1}
```

`revision` is the current saved draft head and must still match. Source and
destination are positive exact saved version numbers, including current/history
and a retained live snapshot. The response includes `id`, current/live numbers,
instance identity/revision, `from`/`to` snapshot kind and current/published flags,
full `content`, `review.changed_fields`, `review.markdown_diff`, and advisory
`restorable`/`restore_reason`. Stable JSON keys are the same in both languages.

The eight editable fields are title, slug, Markdown, excerpt, tags, category,
cover and featured. Server IDs, timestamps, publication state, migration
provenance and scheduler state are not content to copy. The comparison shares
proposal review semantics: metadata has exact before/after values; Markdown has
a linear shared-prefix/suffix changed range, at most four chunks. It is not a
minimal per-line diff. Complete snapshots remain available even when unchanged;
there is no truncation. Each Markdown snapshot retains the existing 1 MiB limit.
Comparison has a conservative 64 MiB JSON response budget covering all eight
fields, JSON escaping, complete snapshots, changed-field values, Markdown chunks
and transport overhead. It counts before building the review or allocating an
escaped response. A legacy backup can contain a very long cover URL because the
existing cover validator has no length bound. Such a backup remains valid and
readable by the existing import/export path; an oversized comparison returns a
controlled `validation` error (HTTP 400), with an English/Chinese budget message.
No snapshot is truncated. The conservative estimate can also reject some actual
responses smaller than 64 MiB. Inspect the original backup offline or select
bounded versions; this change does not add pagination to full article retrieval.
Text is escaped in Studio. Comparison does not render HTML, load images, fetch
URLs, probe files or contact an AI provider.

History pages show eight rows. The selector includes the latest 100 versions,
current, live and the chosen row. Choose an older row through history pages to
include that exact version; CLI/API/MCP can compare arbitrary retained versions.
Current and live labels refer to actual snapshots, not status inferred from a
historical row. A live snapshot missing from sparse imported history is readable
but cannot be restored by the existing restore operation.

Owner, draft, read and proposal tokens already authorized for private reads can
compare full historical content. Read/proposal tokens have no Restore control;
owner/draft tokens retain the existing `posts.restore` **draft** authority. An
advisory `restorable` value does not grant permission or reserve a slug/version.
Anonymous clients cannot use comparison or discover private post existence.
Public reading, SSR, search, RSS and sitemap gain no history data.

To restore in Studio, save or export unfinished edits first. Open Compare or
Restore on a historical row, review the eight fields, use the **current saved
draft as source**, and confirm Restore reviewed draft. A dirty editor, stale
review, current destination, identical content or currently occupied historical
slug prevents that UI confirmation. A restore writes one new private revision
with current revision CAS and an exact retry key. It leaves the published
snapshot unchanged. Previously approved schedules keep their original pinned
revision and time; they can still publish that approved snapshot when due.
Inspect/cancel them separately if that intent has changed.

Typing, including an edit followed by reverting the text, invalidates the review.
A delayed comparison cannot reopen a closed dialog, replace a newer report,
change another editor, or discard input. Successful restore updates the saved status badge in place, clears obsolete
preview HTML and returns to Write without rebuilding editor fields. A held older
preview cannot reopen after the new revision is acknowledged.

A delayed **committed restore** updates
its acknowledged head without replacing newer input/caret; remaining edits stay
dirty and require a separate Save. Closing the restore dialog during that write
preserves the editor and any subsequently opened dialog. Lock, navigation and
Back remain guarded while a write is active.

After a lost or malformed restore acknowledgement, remain in that tab and retry
the **identical request/key**. Other editor saves, proposals, publishing and new
schedules are blocked until resolution. A retry reuses the saved receipt and
reads the latest server draft, so an intervening Agent edit is not confused with
an old acknowledgement. If only that head read fails, retry sends no second
restore. Newer local text is preserved for a later CAS-guarded save.

This restore receipt is **memory-only**, distinct from encrypted local unfinished
copies. Closing/refreshing the tab loses the receipt. Review server history and
current content before attempting another restore; do not infer failure from a
missing acknowledgement. Local recovery may retain unsaved content, not this
restore operation. The underlying API retains its existing restore semantics;
the UI no-op advice is not an API prohibition.

### Disposable reproducible example

On your own loopback test instance, with `FOLIO_URL` and `FOLIO_TOKEN` already
configured, these writes remain private. Requires `jq` to extract the new ID.

```sh
FOLIO_REVIEW_ID=$(folio call posts.create --json '{"title":"Fictional first","slug":"fictional-revision-example","markdown":"# First version","excerpt":"First summary"}' | jq -r '.data.post.id')
folio call posts.update --json "{\"id\":\"$FOLIO_REVIEW_ID\",\"expected_revision\":1,\"title\":\"Fictional second\",\"slug\":\"fictional-revision-example\",\"markdown\":\"# Second version\",\"excerpt\":\"Second summary\"}"
folio --lang en call posts.compare --json "{\"id\":\"$FOLIO_REVIEW_ID\",\"revision\":2,\"from_revision\":2,\"to_revision\":1}"
folio --lang zh-CN call posts.compare --json "{\"id\":\"$FOLIO_REVIEW_ID\",\"revision\":2,\"from_revision\":2,\"to_revision\":1}"
```

For HTTP use `POST /api/op/posts.compare` with that same payload and bearer
Authorization. MCP uses `posts_compare` with the same four arguments and
`readOnlyHint: true`. After inspecting the returned snapshots, an authorized
client may explicitly call `posts.restore` with the ID, `revision: 1`,
`expected_revision: 2` and one chosen idempotency key. A comparison grants no
permission to restore or publish. Do not reuse the example slug in existing data.

### Executed regression

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio
python3 scripts/revision_review_e2e.py --binary dist/folio --report /tmp/revision-api.json
python3 scripts/revision_review_e2e.py --binary dist/folio --chromium /usr/bin/chromium --report /tmp/revision-browser.json
```

The script uses disposable fictional data, real HTTP/CLI/MCP and real committed
responses held in Chromium. `--restore-race-only` isolates later typing during a
committed restore: the pre-change binary fails; the new binary preserves text,
selection, dirty state and the subsequent save identity. Transport aborts and
malformed deliveries occur after actual server commit, not fabricated post data.
Some modal/session stress cases dispatch browser input/click events to underlying
controls while a response is held. This exercises programmatic transitions as
well as user keyboard cancellation.

Implementation and tests were AI-assisted. Local execution covers Linux Chromium;
independent human review, other browser engines, public deployment and production
security audit remain unperformed. See the [verification record](REVISION-REVIEW-VERIFICATION.json).

## 中文

版本历史现在可分页查看；比较面板展示两个**已保存**版本的全部八个可编辑字段，
并标明当前草稿和线上版本。比较不会保存、恢复、发布、加载图片或执行 Markdown
中的 HTML，也不会调用 AI 服务。未保存的编辑区不参与比较，必须先保存或导出再恢复。

API / CLI / MCP 统一新增第 36 个操作 `posts.compare`，MCP 名为 `posts_compare`。
须同时提供文章 `id`、当前草稿 `revision`、来源 `from_revision` 和目标
`to_revision`；版本号均为正数。当前版本不匹配会返回冲突，不存在的版本返回未找到。
机器字段、错误码和权限在两种语言下相同，以上示例可复现双语调用。

八个字段为标题、slug、Markdown、摘要、话题、分类、封面、精选标志。Markdown
使用与提案审阅相同的线性前后缀比较，显示一个变化区间，最多四段，**不是最小逐行
差异**。完整快照不会截断，每个 Markdown 仍最多 1 MiB。比较先计算全部八字段、JSON 转义、
快照、差异及包装开销的保守 64 MiB 预算；超限返回 `validation` / HTTP 400 的双语
错误。历史备份的超长 cover 仍按原有规则导入及导出，不改旧数据；可离线审阅原备份
或选择较小版本。估算保守，部分实际小于 64 MiB 的响应也可能被拒绝。
历史每页八行；选择器含
最近 100 个版本及当前、线上、已选版本。更早版本可从历史分页选择，或指定准确版本号。

owner、draft、read、proposal 均沿用已有私有读取权限，可查看历史全文；read / proposal
没有恢复权限，owner / draft 保留原有 draft scope。`restorable` 只是提示，不授予权限。
匿名和公开阅读、搜索、RSS、SEO 页面不会因此获得草稿或历史内容。

恢复前须以**当前已保存草稿为来源**审阅目标，再明确确认。脏编辑区、过期比较、当前
版本、相同内容或被其他文章占用的历史 slug 会阻止界面恢复。恢复仅新建一个私有草稿，
不改线上版本。既有计划仍保留原来的固定版本和时间，到期仍可能发布那份已批准快照；
若发布意图变化，须单独检查或取消计划。

任何新输入（包括改动后改回）会使审阅过期。取消、Esc、遮罩、换文章、锁定或稍后的
比较不会被旧响应改写。恢复已经提交而回执迟到时，后来输入、光标及新打开的对话框
会保留；尚未保存的编辑须单独保存。成功后只更新保存状态徽标、清空旧预览并回到 Write，
不重建编辑字段；旧版本的迟到预览无法重新打开。写入进行中仍阻止锁定、导航和浏览器返回。

恢复回执丢失或损坏时，请留在同一标签页重试原请求及原幂等键。确认前阻止后续保存、
提案、发布及新建计划。重试确认后读取最新服务器草稿，避免把旧回执当作并发 Agent
写入后的最新内容；若只是最新草稿读取失败，再次检查不会重复恢复。

恢复回执只保存在内存，**不属于加密本地未保存副本**。关闭或刷新会丢失回执；再次恢复前
请先查看服务器历史和当前内容，不要把未收到回执等同于没有提交。本地副本可保护输入，
不持久保存恢复操作；后续写入仍由版本 CAS 保护。原始 API 恢复语义未改变。

本次源码与测试由 AI 辅助实现、在 Linux 本地 Chromium 中实跑；独立人工审阅、其他浏览器、
公网部署和生产安全审计尚未完成。具体结果见 [验证记录](REVISION-REVIEW-VERIFICATION.json)。
