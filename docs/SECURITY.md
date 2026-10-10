# Security model and deployment notes

This is pre-production v0.2 software. Automated checks do not replace independent security review before internet exposure.

- Bind loopback by default. Use HTTPS at a trusted reverse proxy for remote use
- Token files are created with mode 0600 in a 0700 data directory. Keep the whole directory private. Never place it under `web/`
- FOLIO_TOKEN is the administrator secret. Optional proposal/draft/read tokens must be distinct from each other and the admin token, and 32+ characters; issue and rotate them yourself. Tokens are not in capability results, exports, URLs or application logs
- The CLI refuses non-loopback plaintext URLs and cross-host redirects to protect bearer credentials
- Administrative browser calls use Authorization headers, not cookies. Origin mismatches are rejected; no permissive CORS is enabled
- Frontend token storage is session-scoped; do not use Studio on an untrusted machine
- Raw Markdown HTML is disabled and rendered HTML is sanitized. CSP disallows external script, frames, plugins and cross-origin connections
- Uploaded SVG/HTML are rejected; uploaded images have a 5 MiB cap, fixed content type, nosniff, hash-derived URLs, and immutable caching. Names cannot contain path separators. Public asset URLs are not an access-control mechanism
- Drafts/revisions and private metadata never appear in public reading, feed, search, sitemap, or SSR surfaces
- `posts.check` requires the same private-read authority as `posts.get`; it is unavailable to anonymous readers. It reads only the authorized in-memory catalog, never fetches reference URLs, probes filesystem paths or executes HTML. Findings expose the supplied reference and classification, not target IDs, titles or bodies.
- Proposal-only tokens can read private authoring baselines and submit/cancel their own suggestions, but cannot write drafts, approve, publish, schedule, migrate, upload media or administer. Shared role tokens share an identity; they do not isolate separate agents
- Proposal approval is owner-only and revision-guarded; it writes a private draft, never a live snapshot. Display proposal differences as untrusted plain text
- Scheduling requires explicit confirmation of one current revision and a future timezone-qualified timestamp. Restart catch-up may activate overdue approved snapshots. Start recovery with `--pause-schedules`, verify `system.info.scheduler.paused`, inspect restored jobs, and resume only by deliberately restarting without the flag
- Markdown migration is admin-only. The plan hash checks integrity, not origin or authorization. Review exact content, replacement targets, revisions, hash and destination before confirmation. Source dates/draft flags cannot trigger publishing. A partial batch retains already committed private drafts
- File restoration is logical JSON and never extracts ZIP or supplied paths. Restore refuses non-empty instances and verifies image hashes and content types
- Backup checksums detect corruption; they are not signatures or proof of author identity. Restore only backups from sources you trust
- Trashing is recoverable; there is no permanent purge operation in this version
- User-chosen base_url is validated, and browser HTML metadata is escaped
- Secrets, credentials and user data should not enter source archives, screenshots, test artifacts or bug reports

For production: terminate TLS, set the canonical base URL, configure authenticated backup storage, restrict network reachability of administrative clients, rotate demo/local tokens, disable demo content, and review dependency updates. Media confidentiality, comprehensive rate limiting, OAuth, audit retention policy, multi-user roles, large-scale search and denial-of-service hardening remain outside this release's promise.

- `posts.relations` uses the existing private `read` scope: owner, draft, read and proposal tokens already authorized to read drafts can see related article IDs, titles and snapshot metadata. Authorization precedes post lookup. No anonymous operation, public graph endpoint or permission is added; the existing public live-only backlinks response remains unchanged. Only saved snapshots are parsed; no outbound URL, filesystem probe, script or provider runs. Relationship evidence and article titles are escaped before Studio display.

- `posts.compare` has the same existing private-read scope as `posts.get`: owner/draft/read/proposal tokens can read full historical draft content. Public APIs gain no historical data. Source and destination text is escaped, not rendered; Markdown/images/URLs execute no network or script during comparison. `restorable` is advisory and does not expand permissions. `posts.restore` retains its existing **draft** scope (owner and draft tokens), creates a private revision, and leaves live content/pinned schedules unchanged. Studio requires saved input plus an exact current-to-destination review before restoring. Uncertain restore receipts block subsequent editor saves, proposals and publication until an identical retry and latest-head confirmation resolve them. That receipt is memory-only; refreshing or closing the tab requires reviewing server history, with ordinary CAS protecting subsequent writes.
