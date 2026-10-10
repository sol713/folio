# Architecture

## One service, three adapters

```
Studio browser ── HTTP JSON ──┐
JSON CLI ─────── HTTP JSON ───┼── core.Store.Call ── SQLite transaction
MCP SDK stdio ── HTTP JSON ───┘
Public reader ── published snapshots only
```

The daemon owns application writes. The CLI and MCP server never independently edit database files. `internal/core/capabilities.go` is the operation/schema registry. Core operations validate fields, content size, slug uniqueness, revision preconditions, and explicit publishing intent. The HTTP adapter enforces bearer roles and same-origin browser requests. The MCP adapter uses the official modelcontextprotocol/go-sdk, discovers operations and exposes each as a tool.

## Content model

A `Record` contains a mutable draft pointer, immutable content revision snapshots, optional published snapshot, a trash flag and old slug metadata. Draft edits copy all fields into a new revision. The live snapshot includes title, URL slug, excerpt, tags, category and HTML as well as Markdown. Editing metadata cannot leak it into the public index, feeds or article pages. Restoring a content revision creates a new draft; recovering trash also returns to draft and never republishes.

Content revision is the compare-and-swap guard. Publishing does not create a new content revision; it activates the reviewed revision. Administrative state changes separately advance the instance revision. Retry keys are operation- and actor-scoped and fingerprint exact JSON bytes: reuse identical payloads when retrying. A changed payload with the same key fails instead of silently duplicating writes.

## Persistence

- SQLite WAL, busy timeout, transactions and immediate transaction locks
- Migration 1: initial snapshot catalog
- Migration 2: per-post `posts(id,document)` rows, content-addressed `media(id,metadata,bytes)` BLOB rows, small instance/settings/audit/retry catalog in `state`
- Immutable revisions currently live inside each post document; editing one post rewrites that post's history, not the media library
- Process cache refreshes when the persisted instance revision changes; public queries cannot observe a partially committed write
- State schema 2 adds proposal and schedule catalogs; private migration provenance is stored alongside them. Opening a schema 1 instance upgrades it transactionally; old writers must not reopen schema 2
- A separate persisted local instance identity binds reviewed migration application to its destination and is not copied by logical backup restore
- Logical export is taken from one transaction; it includes drafts, published snapshots, revisions, media, proposals, schedules and import provenance, and excludes credentials and retry cache
- Logical backup version 2/state schema 2 is current; version 1/schema 1 remains accepted after original-checksum validation and explicit in-memory upgrade
- Restore validates version, top-level checksum, post content and asset hashes, and requires an empty target. It does not extract user-supplied filesystem paths

This deliberately bounded personal-site release is intended for a personal site. The 64 MiB logical snapshot cap avoids unbounded memory/backup work. Future scale work should normalize immutable revisions, paginate the authoring catalog, move large blobs to an explicit object-storage adapter, and add indexed search. Never silently replace the Go service with a client-only mock for a hosting platform.

## Reviewed workflows and migration

Proposal candidates and their base snapshots are immutable. Owner approval checks the proposal revision and the original post base revision, then creates a private draft revision in the same transaction as the review decision. It cannot publish. Actor-scoped ownership checks complement HTTP role checks; the shipped daemon has one token/identity per role.

Schedules persist the exact reviewed `Post` snapshot, post revision, requested UTC time and a separate schedule revision. The daemon scans at startup and every second. A due transition atomically updates the live snapshot, publication metadata, job outcome and audit trail; later draft content remains private. Manual publish, unpublish or trash cancels active schedules. Slug conflicts become blocked rather than exposing another revision. `serve --pause-schedules` disables automatic scans for recovery review; it does not cancel schedules or revoke manual publishing authority. See [WORKFLOWS.md](WORKFLOWS.md).

`internal/migration` is a pure standard-library parser/planner/exporter. `internal/core/migration.go` integrates its three admin operations into the same registry used by HTTP, CLI, MCP and Studio. Planning is read-only and emits a deterministic content hash, frozen entries and an explicit target instance ID. Confirmed application validates the plan hash/target, then calls ordinary `posts.create` or `posts.update` operations with fixed per-item retry keys and revision guards. Each item is atomic; a batch is not. The private provenance write commits with its draft. No file paths are opened on the server and no migration operation publishes. See [MIGRATION.md](MIGRATION.md).

## Presentation and stable interfaces

Reader/Studio presentation defaults to zh-CN with an English switch. Locale changes update existing interface nodes rather than replacing editor forms; user text and machine/code regions are excluded. The browser stores only the locale preference in localStorage/cookie, while its bearer token remains session-scoped. API human errors use the requested locale; operation names, machine codes, JSON keys and schemas remain stable. CLI language selection is shared with daemon help, and MCP discovery remains canonical English.

## Public rendering

Go embeds HTML/CSS/JavaScript. Anonymous APIs only return published snapshots. Server-rendered semantic article/index content is included in initial HTML for crawlers/no-JavaScript access and then replaced by the richer browser interface. RSS/sitemap are generated from the same published list. Search scans only public Markdown/title/tags, including one- and two-character Chinese queries. Raw Markdown HTML is omitted by Goldmark and rendered output is sanitized with Bluemonday before storage and backup restore.

## Read-only content checks

`posts.check` parses the current saved Markdown with the same Goldmark parser as rendering, and analyzes link/image destinations against a single authorized catalog snapshot. It returns draft and instance revisions, stable finding codes, bounded evidence and source locations. It does not fetch URLs, probe paths, alter the catalog, write audit/retry records or change the publication protocol. Studio invalidates pending or completed results on input, save, route, editor, token or instance changes. See [CONTENT-CHECK.md](CONTENT-CHECK.md).

## Extension points

The next substantial upgrades should preserve operation parity: content graph and exact Markdown-link backlinks, per-agent identities and finer-grained approval policies, richer editor integration, revision-row storage, scoped tokens with expiration, and verified remote MCP transport. A provider integration must expose configuration/cost/status honestly; an absent provider must not masquerade as a working AI capability.

`posts.relations` reuses the shared Goldmark destination walker, canonical URL classification and ephemeral catalog index with `posts.check`. Each call holds the existing read transaction and computes one-hop outgoing and incoming groups separately for current saved and published snapshots. No persisted graph or cache is introduced. Query snapshots are scanned first; explicit budgets cap incoming catalog work, groups and positions. Ambiguous alias owners remain unresolved. Studio links use article IDs and open current drafts, with request/session guards and saved-buffer invalidation. See [ARTICLE-RELATIONS.md](ARTICLE-RELATIONS.md).
