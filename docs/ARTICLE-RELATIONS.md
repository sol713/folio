# Article links / 文章引用

在私有 Studio 中手动读取已保存文章的一层引用：分别查看当前草稿和线上版本的引用、被草稿和被线上版本引用。每条关系显示来源版本、目标是否公开、重复次数及前三处源码位置。点击来源或目标按文章 ID 打开其**当前已保存草稿**；位置属于标明的来源版本，不会把线上旧版本的位置套用到新草稿。未保存输入不会发送给查询；保存、编辑、导航、锁定及取消后，迟到结果不会改动编辑器。界面支持中英文及移动端。

This author-only view shows one-hop outgoing and incoming Markdown article links, separately for current saved drafts and published snapshots. Each group records the actual source revision, target availability, occurrence count and first three source positions. Article links open the **current saved draft by ID**, including after slug changes. Positions belong to the indicated source revision. Reading or following links never saves or publishes content. Unsaved input is excluded; editing invalidates the report and disables its article links.

```sh
folio --lang en call posts.relations --json '{"id":"POST_ID","revision":3}'
```

HTTP: `POST /api/op/posts.relations` with an authorized bearer token and the same payload. MCP: `posts_relations`, `readOnlyHint:true`. Registry: 35 operations. The schema requires the current saved `id` and positive integer `revision`, rejects unknown properties and accepts no arbitrary Markdown, URL or path. Stale revisions return `conflict` (409); missing/deleted records return `not_found` after authorization. Owner, draft, read and proposal tokens retain their existing private-read authority. Anonymous readers receive no graph or draft metadata. Chinese validation errors follow the same locale rules as existing operations.

The response includes `saved_draft`, nullable `published`, `incoming_saved_drafts`, `incoming_published`, instance/draft/live revisions, counts, `limitations` and `truncated`. Source and target metadata distinguish `saved_draft` / `published`, snapshot revision and current draft/live revision; status is `draft`, `published` or `changed`. Outgoing availability is `public`, `private`, `future_self`, `missing` or `ambiguous`. Unresolved edges have no target ID. Groups deduplicate resolved article IDs within an availability; missing/ambiguous paths deduplicate by slug. Locations retain the literal rendered reference plus route provenance, so current and old-slug links can share a group without losing redirect evidence. Self links and cycles are reported once per hop; there is no recursive expansion.

Markdown parsing, destination escaping and canonical route classification are shared with `posts.check`. A current public slug wins over old aliases; an alias with several owners is marked ambiguous instead of selecting an article. Current draft-only slugs resolve privately; fragment-only links refer to the actual source snapshot. Goldmark links, reference definitions and URL autolinks are parsed; image destinations, raw HTML, code, external URLs and unsupported paths do not form article edges. External URLs are never fetched and anchors are never verified. Historical versions are excluded. This is a bounded reference view, not a complete knowledge graph.

Each call recomputes from one authorized catalog snapshot without a persistent index or cache. Slug edits, publication changes and deletion are reflected by the next call. Results can become old if another writer changes the catalog; the catalog revision identifies the checked point in time. Limits: 1,000 source snapshots, 8 MiB Markdown, 20,000 references overall, 2,000 per source, 100 groups per list, three positions per group and 512 UTF-8 bytes plus ellipsis per reference. Requested saved and live versions are scanned first. Counts retain checked totals when displayed groups are capped; `truncated` and omitted-snapshot counts explicitly signal incomplete incoming results. Block locations may be approximate, matching Content check.

查询只读取当前已保存草稿和线上快照，不读取历史版本、外部网址、图片、HTML、代码或任意文件路径，不验证锚点，不连接 AI 服务。每次重新计算；其他作者修改后可能过期。扫描与返回都有数量限制，超过范围会明确标注结果不完整，不能据空列表断言整个站点没有任何引用。原有发布审批、草稿恢复和公开文章接口保留。

## Verification

`internal/core/relations_test.go` covers snapshot separation, source positions, duplicate counts, route aliases, ambiguous owners, immediate rename/unpublish/delete changes, read-only repeats and all source/byte/reference/edge caps. `internal/server/relations_test.go` covers private-read roles, authorization before existence lookup, origin enforcement, localized validation and no private data on public surfaces.

`scripts/article_relations_e2e.py` uses a real daemon, CLI, MCP, browser and reference listener. API-only checks run on both CI Go versions; browser checks run alongside all publication/recovery/content regressions. Delayed browser responses retain actual server data. A navigation regression reproduces a prior late `posts.get` overwriting a newer editor, then proves the new navigation-session guard preserves dirty state, caret and the subsequent save destination. Use `--navigation-only` to isolate it. Full acceptance and source lineage are recorded in [ARTICLE-RELATIONS-VERIFICATION.json](ARTICLE-RELATIONS-VERIFICATION.json).
