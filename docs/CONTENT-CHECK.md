# 发布前内容检查 / Publication content check

`posts.check` 是可选的只读操作：分析当前已保存草稿的 Markdown、封面媒体及摘要。检查不会保存输入、发布文章、访问引用网址或运行 HTML；警告不阻止合法发布。UI、API、CLI、MCP 共享此操作。

`posts.check` is an optional read-only analysis of current saved Markdown, cover media and summary. It never saves input, publishes, fetches referenced URLs or executes HTML. Warnings never gate a legal publish. Studio, API, CLI and MCP use the same operation.

```sh
folio call posts.get --json '{"id":"POST_ID"}'
folio call posts.check --json '{"id":"POST_ID","revision":3}'
```

HTTP: `POST /api/op/posts.check` with a bearer token and the same JSON. MCP: `posts_check`, `readOnlyHint:true`. The schema requires `id` and positive integer `revision`; unknown properties are rejected. The revision must equal the current saved draft: a stale or future value fails with `conflict` (HTTP 409). Missing or deleted records return `not_found` only after authentication. Invalid inputs return `validation` (HTTP 400). No arbitrary Markdown, origin, filesystem path, or target URL can be submitted separately.

Permission scope is `read`. Admin, draft, read and proposal tokens already have access to private `posts.get` baselines and may check. Anonymous/invalid tokens receive the same authentication failure for known and unknown IDs. Classification of a private target does not expose its ID, title or body. Read and proposal tokens cannot publish, and this operation adds no write authority.

## 稳定结果 / Stable output

The response includes `id`, `revision`, `instance_id`, `instance_revision` (catalog snapshot), `canonical_url`, `findings`, `counts`, `limitations` and `truncated`. Only human finding `message` is localized by the API request / CLI / MCP language. Names, JSON keys, machine codes, evidence and schema remain stable. Findings have `code`, `severity`, `field`, `location_precision`, optional one-based `line`, bounded `reference`, and `reason`.

| Code | Severity | 依据 / Evidence |
| --- | --- | --- |
| `article_missing` | warning | 无匹配的公开文章、旧短名跳转或当前草稿 / No matching public route, redirect or current draft |
| `article_private` | warning | 仅有私密草稿网址 / Draft URL exists without a public route or redirect |
| `media_missing` | warning | 本实例媒体目录中无对象 / Media key absent from this instance’s catalog |
| `excerpt_missing` | info | 已保存摘要为空或仅空白 / Empty or whitespace-only saved summary |
| `reference_unchecked` | info | 路径、编码、解析或大小超出支持范围；不代表失效 / Unsupported path, encoding, parsing or size; not a missing-target claim |

Counts include `warning`, `info`, `references`, `article_urls_checked`, `media_urls_checked`, `external_urls_not_checked`, `other_urls_not_checked`, `anchors_not_checked`, and `self_urls_assumed_after_publish` when nonzero. An absent count is zero. Totals may exceed the displayed findings when truncated.

## 规则与边界 / Rules and limits

- Only current saved content is analyzed. There are no historical or unsaved-buffer checks. The catalog is a point-in-time snapshot: publication, deletion and media changes elsewhere can invalidate reference availability later. Recheck before your final review.
- Destinations are extracted from Goldmark links/images, including reference-style links and URL autolinks, using the renderer’s destination decoding. Markdown code and raw HTML are excluded. Image alt text does not create nested links.
- Article matching uses the configured canonical HTTP(S) origin and `/posts/slug`. Live slugs and live old-slug redirects match the existing routes. Draft-only URLs warn. Deleted records do not match. The source draft’s own future slug and fragment/query-only self URLs are assumed available after this draft is published; this assumption does not authorize publication or bypass slug conflict checks.
- Relative paths resolve against `base_url + /posts/<draft-slug>` without a trailing slash. Root-relative, ordinary `../`, same-origin absolute and protocol-relative URLs are supported. Origin matching uses scheme, case-insensitive hostname and effective port. Other origins are counted as external without fetching. Internationalized hostname aliases are not normalized.
- Percent-encoded ordinary slug characters are decoded once, matching Go URL paths. Encoded slashes/backslashes, encoded dot segments, noncanonical slugs and trailing slashes remain unchecked. The SSR route and SPA differ on trailing-slash normalization, so the check makes no missing-target claim for such URLs.
- Query parameters do not affect target identity. Fragment/anchor target existence is **not** checked; checking the article path does not validate its fragment. Relative links outside `/posts/slug` or `/media/key`, opaque/custom schemes and userinfo URLs are counted as other unchecked URLs.
- Media references only look up keys in this instance’s catalog. No filesystem path is opened and no URL is fetched. Both Markdown image/link destinations under `/media/` and the saved cover are checked. Links that are image destinations under `/posts/` are outside this media check’s scope.
- At most 2,000 references are analyzed and 100 findings returned. Displayed reference evidence is shortened at 512 UTF-8 bytes plus an ellipsis. Oversized/ambiguous URLs are reminders, not missing-target assertions. Markdown locations identify a source line, or the beginning of the containing block when the parser has no exact inline position; they do not promise a character offset.

这些限制会在双语界面完整显示。无警告仅表示检查范围内未发现问题，不意味着所有链接有效或内容已经可以自动发布。

These limits are visible in both interface languages. No warnings means no warning was found in the checked scope; it does not guarantee every reference or authorize automatic publication.

## Studio behavior

手动点击“检查已保存草稿”，查看版本、级别、依据及边界。定位按钮只选中源码行、摘要或封面字段，不改动内容。不包含未保存输入，也不会为了检查自动保存。

Use “Check saved draft” to inspect revision, severity, evidence and limits. Location buttons select a source line, summary or cover field without modifying content. Unsaved input is never included or implicitly saved.

Any input event (including reverting the text), a save attempt, navigation, editor replacement, token/instance changes or a newer check invalidates an in-flight result. Completed results become outdated and location buttons are disabled after edits. Late responses cannot bind to another editor or replace a newer report. Live language switching redraws the check panel while preserving input, caret and pending work. The publication review shows the report or its unchecked/outdated status and explicit advisory scope; the existing exact-revision publish payload is unchanged.

## Verification

Go tests cover route/encoding classification, source locations, reference/finding bounds, current-revision validation, read-only state equality, permissions, localization, origin policy and public non-disclosure. `scripts/content_check_e2e.py` starts a disposable real daemon and verifies API/CLI/MCP parity, schema and annotations, actual routes, role restrictions, strict inputs and an unchanged logical backup. A reachable loopback listener records zero reference requests.

With `--chromium`, the same script verifies manual Studio checks, field/line focus, no implicit writes, both languages and 320/390px layout, all role interfaces, thirteen delayed-response invalidation cases, a newer-check-wins race, interrupted-read retry and exact-revision publication despite warnings. `scripts/content_check_busy_e2e.py` additionally covers immediate check availability after a successful publish, retry after a held check completes during a failed publish, and successful/failed old-editor callbacks leaving a new pending check and caret untouched. Request cleanup is bound to its editor/render/token/instance/route/sequence, separately from result eligibility during a write lock. Publication completion refreshes only the original review context or the new editor rendered by that publication. Existing publication and encrypted-recovery regression scripts remain CI gates. Reports and screenshots contain fictional content and stay outside the source tree.
