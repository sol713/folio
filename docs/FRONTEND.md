# FOLIO frontend

The public journal and Studio are a vanilla HTML/CSS/JavaScript SPA, embedded
into the Go binary. There are no build dependencies, web font requests, CDN
assets or external JavaScript. Rebuild the binary after changing `web/`.

## Routes

- `/`: featured essay, story cards, category filtering
- `/posts/{slug}`: published article, copy-link, related stories
- `/topics`, `/tag/{tag}`: tag collections
- `/search?q=`: published-content substring search, including short CJK queries
- `/about`: author introduction derived from settings
- `/studio`: authenticated drafts and published stories
- `/studio/new`, `/studio/posts/{id}`: Markdown editor and publishing workflow
- `/studio/proposals`, `/studio/proposals/{id}`: immutable proposals and review
- `/studio/schedules`: pinned snapshots, local/UTC time and durable job outcomes
- `/studio/migration`: file preflight, frozen-plan review/application and portable export
- `/studio/media`: validated image upload and copyable public image URLs
- `/studio/settings`: revision-guarded site settings
- `/studio/tools`: CLI/MCP instructions, schemas, full backups and audit activity

## 中文 / English

The complete interface defaults to Simplified Chinese (`zh-CN`); 中文 / EN
switches to English on public pages, the sign-in screen and Studio. Labels,
help, dialogs, confirmations, toasts, accessible names, dates and reading counts
follow the selected language. The preference is persisted in localStorage and a
same-site locale cookie; the cookie also selects the server-rendered shell.
Authored titles, Markdown, excerpts, taxonomy, settings values, IDs, slugs,
URLs, JSON/schema/code regions and source metadata are never translated.

`web/i18n.js` updates interface text/attributes in existing DOM nodes. Switching
language does not rerender or save the editor, clear unsaved text, replace its
selection, or issue a publication/network action. Locale-aware API requests use
`Accept-Language`; machine JSON keys, error codes and operation names are stable.
The language preference can use localStorage, but draft buffers do not.

## Interaction details

- Command palette: Ctrl/Cmd+K; Arrow keys; Enter; Escape; title-prioritized filtering;
  combobox/listbox ARIA; focus restoration; IME-safe keyboard handling
- Writing: Ctrl/Cmd+S, Markdown toolbar, list/task continuation on Enter, Tab
  indentation, focus mode, server-rendered preview, CJK-aware word/read-time estimate
- Editor-buffer import reads a `.md`/`.markdown`/`.txt` file with replacement
  confirmation. Export downloads current Markdown, including unsaved edits,
  without invented frontmatter. This is distinct from the migration workspace
- Draft save never publishes. Explicit publication review identifies the exact
  revision, title, URL and changes from the live snapshot
- Proposal review displays candidate/base field changes and a safe plain-text
  Markdown diff. Approval checks stale revisions and creates only a private draft
- Scheduling requires reviewing the current saved revision and a future local
  time, with UTC shown for verification. Rescheduling reviews and pins the current
  draft anew; later edits cannot silently replace an approved snapshot
- The schedule workspace warns when automatic publication is paused and exposes
  blocked/cancelled/published outcomes. A blocked job needs explicit review
- Migration uploads at most 200 source files/8 MiB, shows warnings, full replacements
  and destination, and retains a frozen plan for partial/lost-response retries.
  Export is a JSON bundle containing Markdown plus a provenance manifest. Nothing
  imported is automatically published. See [MIGRATION.md](MIGRATION.md)
- Permission-aware navigation/actions use authenticated `system.info.permissions`;
  server authorization remains authoritative. Proposal-only agents cannot approve
  themselves or directly edit drafts even if a client is modified
- Revision conflicts retain editor text. Restoring history creates a new private draft
- Navigation/browser exit warn about unsaved changes; save to the server before
  closing. Migration review state is also in memory; download the plan/report
  before leaving if application is incomplete or uncertain
- Quick capture creates a private `idea`-tagged draft through `posts.create`
- Tokens stay in sessionStorage, never URLs, analytics or localStorage. Lock Studio
  clears the session token
- Arbitrary text is HTML-escaped. Article/preview HTML comes only from the backend
  sanitizer; cover images use local `/media/` URLs
- Responsive layout, reduced-motion support, keyboard focus rings, modal focus
  traps, labeled controls and mobile layouts are included

## Verification and scope

Run frontend syntax and deterministic DOM-contract checks from the repository root:

```sh
node --check web/app.js
node --check web/i18n.js
for test in web/tests/*.test.cjs; do node "$test"; done
```

These checks cover locale/key parity and protected authored text, pending-save
editor preservation, first-save revision history, exact lost-response retry
payloads and workflow interactions. They are not real-browser/visual QA.
[VERIFICATION.md](VERIFICATION.md) is the record of executed release gates.

The v0.1 Mac desktop/mobile verification and screenshots remain historical
coverage: long-form mixed-language editing, save/preview/publish, immutable live
snapshots, first-save history, keyboard navigation and mobile layout. They do
not by themselves verify new v0.2 migration/proposal/schedule screens or locale
switching in a real browser. Refer to the release record for later checks and
remaining platform limits; no exhaustive device matrix or WCAG audit is claimed.

No AI model/provider is built into this UI. External agents use real CLI/MCP
operations. Trash recovery is available through the operations but has no dedicated
Studio view. The server preserves full revision history; the editor shows its
latest eight revisions. There is no durable browser-local draft recovery.
