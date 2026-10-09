# 可审阅提案与定时发布 / Reviewed proposals and scheduled publishing

FOLIO 的代理可以提交提案，但只有站点所有者可以批准。批准只会创建或更新私有草稿，
不会立即发布。定时发布需要单独确认，并固定当时审阅的完整内容版本。之后编辑的
草稿仍然保持私有；重启后只补发已经到期的固定版本。

An agent may suggest a complete content change without receiving draft-writing
or publishing authority. Owner approval applies the exact proposal to a private
draft using compare-and-swap. Publishing, including future publication, requires
separate explicit publisher confirmation. No model/provider calls happen in
these workflows.

## Permissions and ownership

- Configure a distinct `FOLIO_PROPOSAL_TOKEN` on the daemon, then give only that
  credential to a proposing agent. The CLI uses the supplied credential as its
  `FOLIO_TOKEN`; MCP uses the same client and operations
- Proposal credentials may read baseline authoring content and create, inspect,
  list, or cancel their own proposals. They cannot edit drafts, upload assets,
  approve/reject proposals, publish, schedule, alter settings, or restore backups
- The owner/admin can inspect all proposals and explicitly approve or reject them
- Proposal ownership is the authenticated actor, never a client-supplied name
- This release provides one configured proposal role/token, not per-agent
  accounts. Agents sharing that token also share proposal ownership. Do not
  claim isolation between agents using the same credential
- Never put credentials in operation arguments, documents, URLs, or scripts

The HTTP authorization layer is the boundary for scopes. Proposal ownership
checks and owner-only review decisions also run inside the core service. Stable
operation names, status strings and JSON keys do not change with interface
language. Errors have stable machine codes; their messages may be localized.

## Submit a proposal

Read the current draft with `posts.get`. Use its current `revision` as
`base_revision`; stale submissions fail with `conflict`. For a new post, omit
`post_id` and use `base_revision:0` or omit the base revision.

```json
{
  "post_id": "POST_ID",
  "base_revision": 3,
  "candidate": {
    "title": "一次认真的修改 / A considered revision",
    "slug": "considered-revision",
    "excerpt": "A short introduction.",
    "markdown": "中文原文保持不变。\n\nAn agent-proposed addition.\n",
    "tags": ["writing"],
    "category": "Notes",
    "cover": "",
    "featured": false
  },
  "idempotency_key": "proposal-unique-retry-key"
}
```

Call `proposals.create` with this payload. The candidate is a full content
replacement: omitted optional fields become empty/false. It accepts no rendered
HTML, publication status, or metadata that could bypass review. Title, Markdown
and other authored text are preserved exactly, including Chinese/English,
whitespace, and line endings. A missing/blank title or invalid slug is rejected.
An empty Markdown draft may be proposed, but cannot be scheduled or published.

The result contains `proposal` and `review`. The immutable proposal records the
candidate, the exact base content, `base_revision`, authenticated creator,
status, and an independent proposal `revision`. Creation changes no draft,
history or live snapshot. Duplicate slugs may be proposed but must be available
when approval applies the candidate.

`proposals.get` returns deterministic `review.changed_fields` entries with
`field`, `before`, and `after`, plus `review.markdown_diff` plain-text segments
with `kind` equal to `equal`, `remove`, or `add`. The diff shows one changed range
between matching prefix/suffix lines. It is linear-time and lossless, rather
than a smallest-edit diff. Render these strings as text, never as trusted HTML.
The response JSON escapes HTML-sensitive characters. Authored Markdown is
sanitized only when rendering its preview/output, not rewritten by the review.

Use `proposals.list` with optional `status` and `post_id`. Status values are
`pending`, `approved`, `rejected`, and `cancelled`. Proposing actors see only
their own records; the owner sees all of them.

## Review explicitly; preserve concurrent work

The owner inspects `proposals.get`, then calls:

```sh
folio call proposals.approve --json '{"id":"PROPOSAL_ID","expected_revision":1,"confirm":true,"idempotency_key":"approve-unique-key"}'
```

Here `expected_revision` is the proposal revision. The service separately checks
the stored `base_revision` against the current post draft. Approval creates a new
private draft revision only when both match. An intervening owner edit causes
`conflict`; it never overwrites that edit and leaves the proposal pending. Fetch
the latest baseline and submit a new proposal rather than silently rebasing.

Approval of a new-post proposal creates its first private draft. The result
includes `post`, plus the closed proposal's `applied_post_id` and
`applied_revision`. Existing live content is unchanged. Approval does not grant
the proposer any additional permissions.

`proposals.reject` closes a pending proposal without applying it. The proposer
may withdraw their own pending proposal with `proposals.cancel`. Both use the
same `id`, proposal `expected_revision`, `confirm:true`, and optional retry key.
Closed proposals are immutable review records; changing a suggestion means
creating a new proposal. Only the owner may approve/reject; a proposing agent
cannot approve itself by supplying `confirm:true`.

## Schedule the exact reviewed draft

Preview the current draft revision, then call `schedules.create` using an
owner/publisher credential:

```json
{
  "post_id": "POST_ID",
  "expected_revision": 4,
  "publish_at": "2030-01-15T09:00:00+08:00",
  "confirm": true,
  "idempotency_key": "schedule-unique-retry-key"
}
```

`expected_revision` here is the current post revision. The time must be future
RFC3339 with a timezone; it is normalized to UTC. A timestamp without a timezone,
a past time, missing confirmation, stale revision, or empty post is rejected.
There is at most one pending/blocked schedule per post. Creating a schedule
changes neither the draft's content nor public visibility.

The schedule persists its complete immutable `snapshot`, `post_revision`, UTC
`publish_at`, creator, independent schedule `revision`, and status. Inspect it
with `schedules.get`; list/filter with `schedules.list` using optional `post_id`
and `status`. Status values are `pending`, `published`, `blocked`, `cancelled`.
These are publisher operations because scheduled content can be private.

After scheduling, the owner may continue editing. At the due time, only the
stored snapshot becomes live. A newer draft's title, slug, excerpt, Markdown,
tags, category, cover, featured flag, revision, and update timestamp stay
unchanged. Its status becomes `changed` and publication metadata records the
older live revision. If the current draft is still the pinned revision, its
status becomes `published`. The immutable content history is not rewritten.

Public reader/search/feed/sitemap surfaces continue to use only live snapshots;
they cannot see pending schedules or newer private drafts. The stored schedule
is the approval for that specific revision, not permission to publish any newer
content later.

## Cancel, move, or recover a schedule

- `schedules.cancel` requires schedule `id`, its current `expected_revision`,
  `confirm:true`, and optional `idempotency_key`
- `schedules.reschedule` requires the same fields plus a future `publish_at` and
  `expected_post_revision`. It deliberately pins the newly reviewed current
  draft. If newer edits exist, preview those before approving this operation
- Cancellation and rescheduling work only on `pending` or `blocked` schedules
- Any explicit manual `posts.publish`, `posts.unpublish`, or `posts.delete`
  cancels active schedules in the same transaction, so an old job cannot undo
  the owner's newer visibility decision. Recovering trash does not revive it
- If a scheduled snapshot's old slug has since been claimed by a different
  draft/live post, the job becomes `blocked` with `reason:"slug_conflict"` and
  does not publish. Resolve the collision and explicitly reschedule a reviewed
  draft, or cancel it. Blocked jobs do not automatically retry
- Published/cancelled schedules are terminal. Create a new schedule for another
  future publication; a lost response can be retried with the original exact
  arguments and idempotency key

A cancel/reschedule and due worker serialize in one SQLite transaction. Whichever
commits first wins; a stale change receives `conflict`. If publication already
committed, cancellation is not an unpublish action. Use `posts.unpublish` with
its normal explicit confirmation if necessary.

## Restart safety, audit, backups and limits

The daemon invokes `Store.RunDue(ctx, now)` at startup and periodically while
running. No external queue or service is required. Due work remains in SQLite
while the daemon is stopped. On restart it catches up overdue jobs using their
stored snapshots. The actual activation timestamp is `executed_at` and public
`published_at`; `publish_at` retains the requested time. This is not a promise
of second-level publication while the daemon is offline or busy.

Every due activation commits the live snapshot, draft publication metadata,
terminal schedule status, instance revision, and audit events together. SQLite
immediate transactions prevent duplicate activation by concurrent workers or
processes. Failures roll back everything and leave pending work retryable.
There are no outbound deliveries, so this exactly-once claim concerns the local
publication transition only.

Audit entries record decisions and actor identities without article bodies or
tokens. Scheduled activation emits `schedules.publish` and `posts.publish` with
actor `scheduler`; the schedule retains the original approving creator.

Logical backups include proposals and schedules, their immutable snapshots and
terminal outcomes. Restore validates referenced post revisions, snapshot content,
statuses, timestamps, unique active schedules, and normal content limits. HTML
is re-rendered/sanitized from Markdown. Current exports use `folio-backup` version 2/state schema 2; version 1/schema 1 backups are accepted and upgraded after validating their original checksum.

Restoring pending schedules into an ordinary running daemon can make already-due snapshots public on its next scan. Start the empty recovery target with `folio serve --data /new/data --addr 127.0.0.1:8081 --pause-schedules`, verify `system.info.scheduler.paused:true`, then restore and inspect `schedules.list`. The flag disables automatic startup/periodic scans for that process; it neither cancels jobs nor prevents explicit manual publish calls. Keep using the flag on every recovery restart. When ready, deliberately stop and restart without it; overdue snapshots then catch up. The operations recovery drill automatically pauses and verifies its v0.2 target.

Restoring a backup is not a way to cancel publication intent. Restore requires an empty instance, including empty proposal/schedule catalogs. Old v0.1 binaries reject an upgraded schema 2 database; rollback requires the pre-upgrade backup, not reopening newer data with an old writer.

There may be up to 1,000 pending proposals and 1,000 active schedules
(pending + blocked). All historical workflow records and snapshots count toward
the existing 64 MiB logical instance limit. The list operations are deliberately
unpaginated for this bounded personal-site release. No automatic deletion,
retention policy, or workflow-record purge is provided. Export/archive before
expanding beyond these bounds.

## Verification covered by the core tests

- Proposal submission cannot change drafts, history or live content
- Owner review, per-actor read/cancel ownership, stale-base CAS and exact text
- Actor-isolated retry keys, closed decisions and deterministic safe text diffs
- Scheduled publication invisible before its exact due timestamp
- Overdue restart catch-up with a newer draft kept private
- Two SQLite workers activating a job only once
- Atomic cancel/reschedule-versus-worker races and manual visibility intent
- Slug conflicts blocked until explicit rescheduling
- Backup round-trip and rejection of inconsistent/tampered workflow snapshots
- Injected persistence failures roll back both draft approval and due publication

HTTP adapter and real-binary workflow checks assert that the proposal token cannot call
`posts.create`, `posts.update`, `posts.restore`, `media.upload`,
`proposals.approve`, `proposals.reject`, any `schedules.*` operation, publishing,
settings changes, audit/export/restore, or read another actor's proposal. Public
surface tests cover search, feeds, sitemap, redirects and SSR before due
publication and after a newer private edit. Run `python3 scripts/workflows_e2e.py --binary dist/folio`; consult [VERIFICATION.md](VERIFICATION.md) for which final gates have actually been executed.
