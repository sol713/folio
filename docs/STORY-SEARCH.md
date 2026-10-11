# Saved story search / 已保存文章搜索

## English

Studio can find words in saved titles, summaries and Markdown, with existing
slug/tag search retained. Highlighted plain-source snippets explain each hit.
Open one to select that exact text in the editor. This reads private **current
saved drafts**, including unpublished edits to published stories. It excludes
unsaved editor input, earlier revisions and the separate live snapshot.

The shared 37th operation is `posts.search`; HTTP uses
`POST /api/op/posts.search`, CLI uses `folio call posts.search`, and MCP exposes
`posts_search` with `readOnlyHint: true`. It uses existing private-read authority:
owner, draft, read and proposal tokens. Anonymous access is rejected before any
lookup. A read/proposal token gains no write or publication authority. Public
search, SSR, RSS, sitemap and public APIs continue reading published snapshots.

```sh
folio --lang en call posts.search --json '{"query":"雨声","limit":20}'
folio --lang zh-CN call posts.search --json '{"query":"雨声","status":"changed","limit":50}'
```

Use an existing authorized loopback instance with `FOLIO_URL` and `FOLIO_TOKEN`
configured. These calls do not write, publish, schedule or contact external
services. No database migration, dependency, search service or provider is added.

The strict object schema requires a nonblank `query`, at most 128 Unicode
characters before trimming. Matching trims outer whitespace and uses Unicode
simple lowercasing plus a **literal substring**. One-character Chinese queries
work. No regular expressions, accent folding, normalization, fuzzy matching,
stemming or semantic ranking are implied. `status` is optional: empty, `draft`,
`changed`, `published` or `trash`. Trash is excluded unless explicitly selected.
`limit` defaults to 20 and accepts integers from 1 to 50. Unknown keys and null
values are rejected. Studio requests up to 50 and offers its existing status
filters; a truncated result asks you to refine the phrase or status.

The response contains normalized `query`, `status`, `limit`, `total`,
`truncated`, instance identity/revision, `results`, and machine-readable
`limitations`. Every hit identifies its current content revision, retained
published revision and actual saved status. Matches appear in title, excerpt,
Markdown, slug, tags order, with only the first literal occurrence in each field.
Stories rank by their strongest matching field in that order, then saved update
time descending, then ID ascending. Results retain at most `limit` candidates
while counting all current matching stories; there is no continuation cursor.

Each match has `field`, one-based source `line`, zero-based **Unicode rune**
`start_rune`/exclusive `end_rune`, exact original `text`, a plain `snippet`,
`snippet_start_rune` and truncation flags. A snippet contains at most 40 runes
before, the match (at most 128), and 60 after: 228 total. Tags refer to their
editor representation joined with `", "`. Offsets refer to original text, even
when lowercasing changes UTF-8 byte width, such as `İ`. Clients using JavaScript
must convert rune positions to UTF-16 input offsets. No complete Markdown body,
HTML, earlier revision or live body is returned.

Studio escapes snippets as text and never loads embedded images or scripts.
It verifies query/status/instance and response bounds before displaying hits.
Debounced reads defer Chinese IME composition. Obsolete successes and errors
cannot replace a newer query, revive a cleared search, affect another route or
reappear after logout. Failed reads preserve the query and have an explicit retry.
Language changes preserve query/caret and translate labels, never authored text.

Clicking a match opens the latest saved draft and checks its revision **and exact
source range** before selecting text. A later writer triggers a clear notice
instead of selecting obsolete coordinates. Selection changes no content and
performs no save or publish. Ordinary/new-tab links still open the story; match
coordinates remain in memory, never in URLs. Existing unsaved-navigation
confirmation and Markdown export remain available. Editing clears the location
notice. This is a point-in-time catalog search, not a live reservation: search
again after editing or concurrent changes.

Search scans the existing in-memory small-site catalog within its 64 MiB logical
dataset cap and 1 MiB Markdown revision limit. It does not add an index, paging
through all hits, full historical search or large-library scalability. See
[verification](STORY-SEARCH-VERIFICATION.json) for executed checks and
`scripts/story_search_e2e.py` for disposable real-daemon/CLI/MCP/browser regression.

## 中文

Studio 现在可搜索已保存标题、摘要和 Markdown 正文，并保留网址短名、标签
搜索。结果按命中字段解释排序，用纯文本片段高亮命中；点击片段可在编辑器中
选择对应原文。搜索范围是私有工作台的**当前已保存草稿**，包括已发布文章的
未上线修改，不包含未保存输入、历史版本或独立线上快照。

HTTP、CLI、MCP 共用第 37 个操作 `posts.search`（MCP 名称 `posts_search`）。
沿用所有者、草稿、读取和提案令牌的现有私有读取权限；匿名不能访问，读取或
提案令牌不会因此获得写入或发布权限。公开搜索、阅读页、RSS、sitemap 继续
只读取线上快照。示例命令仅执行读取，不保存、发布、安排发布时间或访问外部
服务；无需数据库迁移、新依赖、搜索服务或付费模型。

`query` 必填，去除首尾空白后不能为空，原输入最多 128 个 Unicode 字符。
采用 Unicode 简单小写转换后的字面子串匹配，支持单个汉字；不支持正则、
模糊匹配、同音词、词干、Unicode 归一化或语义搜索。`status` 可省略，或设为
空字符串、`draft`、`changed`、`published`、`trash`；只有显式请求 `trash` 才包含
回收站。`limit` 默认 20，范围 1 至 50，未知字段和 null 被拒绝。Studio 最多
显示 50 篇，超出时显示总数，并提示缩小词句或状态范围，不提供续页游标。

每篇只返回各字段的首次命中；排序依次优先标题、摘要、正文、网址短名、标签，
再按保存时间倒序、ID 正序。返回当前版本、线上版本号、实例身份、快照版本、
总命中数和截断提示，不返回全文、HTML 或历史/线上正文。每个片段最多 228 个
字符：命中前 40、搜索词最多 128、命中后 60。行号从 1 开始，源位置是从 0
开始的 Unicode rune 区间，末尾不含；不是 UTF-8 字节或 JavaScript UTF-16
位置。大小写转换改变字节宽度时仍对应原文，标签对应以逗号空格连接的编辑框。

片段作为转义文本呈现，不执行脚本或加载其中图片。中文输入法组词期间不发送
中间请求。乱序返回、清空、换页、退出登录后到达的旧成功或错误不会污染当前
页面；失败保留搜索词并提供重试。中英切换保留搜索词和光标，只翻译界面。

点击命中后先打开最新草稿，再核对已保存版本和原文区间；其他写入者已改稿时
提示重新搜索，不选取过时坐标。定位本身不改稿、不保存、不发布；坐标只保留
在内存中，不写入 URL，新标签页链接仍正常打开文章。原有未保存离开确认与
Markdown 导出保持可用。定位后编辑会清除提示。搜索是当前快照，编辑或并发
变化后应重新搜索，不能把结果当作版本预留或写入权限。

现阶段仍在原有 64 MiB 逻辑数据集、单版 Markdown 1 MiB 限制内扫描内存
目录；没有新增索引、全量分页、历史检索或大规模全文引擎。实际执行记录见
[验证记录](STORY-SEARCH-VERIFICATION.json)，可复验脚本是
`scripts/story_search_e2e.py`；测试使用临时数据和真实守护进程、CLI、MCP、浏览器。
