# FOLIO: source-reviewed interaction references

Research date: 2026-10-03. This document records GitHub source that was actually
read while choosing FOLIO's interaction patterns. It is a design/provenance
record, not a claim that every proposed feature has shipped or passed testing.

FOLIO's implementation direction is an original warm-paper, serif-led editorial
interface with a restrained orange accent, served as embedded frontend assets
by Go. The product journey is **capture → develop → review → publish**. A small
number of coherent knowledge-work features can grow around that journey without
turning the first release into a general workspace suite.

No upstream source code or visual assets were copied by this research task.
The recommendations below primarily adapt interaction ideas. Where a dependency
or code fragment is adopted later, pin its version, preserve its complete
required copyright/license notices, and record it in the distribution's
third-party notices. A link here is not a substitute for those notices.

## 1. An editorial reading canvas: Ghost Casper

- Repository: https://github.com/TryGhost/Casper
- Reviewed template: https://github.com/TryGhost/Casper/blob/main/post.hbs
- Reviewed styles: https://github.com/TryGhost/Casper/blob/main/assets/css/screen.css
- License: MIT, https://github.com/TryGhost/Casper/blob/main/LICENSE

The article template establishes a clear title/excerpt/byline/cover/content
sequence. Its content grid separates a narrow reading column from wider images
and full-width media. Code blocks and tables scroll inside their own bounds.
The end of the article offers a small set of further reads.

**FOLIO adaptation:** use a roughly 65–75-character prose measure, generous
vertical rhythm, optional wide cover art, a quiet author/date/reading-time row,
and a restrained continuation section. Apply FOLIO's own typography, palette,
spacing and component code. Do not import Ghost's membership/signup controls or
its full theme build pipeline.

## 2. A dependable command palette: cmdk

- Repository: https://github.com/dip/cmdk
- Reviewed implementation: https://github.com/dip/cmdk/blob/main/cmdk/src/index.tsx
- License: MIT, https://github.com/dip/cmdk/blob/main/LICENSE.md

The implementation maintains stable command identity, updates selection after
filtering, excludes disabled choices, scrolls selected items into view, and
connects combobox/listbox/option semantics through active-descendant IDs. Its
keyboard handler explicitly avoids executing commands during IME composition.

**FOLIO adaptation:** a native dialog and a small vanilla-JavaScript command
registry. Include search, new draft, quick capture, media, settings, theme and
keyboard-help actions only when they genuinely work. Preserve the previously
focused element; support Escape, arrows, Enter, Home and End; distinguish empty,
loading and error states. Ignore command execution while `isComposing` or legacy
IME key code 229 is active. Do not add React/Radix solely to use this component.

Publishing from the palette should open the same review flow as the editor.
It must not silently publish the current draft merely because Enter was pressed.

## 3. Search and lightweight knowledge links: Quartz

- Repository: https://github.com/jackyzha0/quartz
- Reviewed search: https://github.com/jackyzha0/quartz/blob/v4/quartz/components/scripts/search.inline.ts
- Reviewed backlinks: https://github.com/jackyzha0/quartz/blob/v4/quartz/components/Backlinks.tsx
- License: MIT, https://github.com/jackyzha0/quartz/blob/v4/LICENSE.txt

The reviewed v4 search includes keyboard opening/closing, trigger-focus return,
title-first result ordering, snippets, tag filtering and an optional selected
result preview. The backlinks component is deliberately small: match incoming
links, show their titles, and optionally hide the section when it is empty.
Quartz's current default branch was v5 at review time; these are explicit v4
references, not a claim that all v5 internals are identical.

**FOLIO adaptation:** put reading search and author actions in one coherent
keyboard surface, with clearly labeled groups. If backlinks are added, derive
them from existing standard same-site Markdown links and display a short
“Linked from” list. A force-directed graph, custom wiki syntax and transclusion
are unnecessary for the initial benefit.

Security constraint: public search snippets and backlinks must be computed from
published revisions only. A draft linking to a public article must not expose its
title or existence to anonymous readers. Do not copy an upstream string-to-HTML
rendering path without separately validating FOLIO's escaping requirements.

## 4. Markdown editing and review: CodeMirror

- Markdown source: https://github.com/codemirror/lang-markdown/blob/main/src/commands.ts
- Markdown license: MIT, https://github.com/codemirror/lang-markdown/blob/main/LICENSE
- Merge source: https://github.com/codemirror/merge/blob/main/src/unified.ts
- Merge license: MIT, https://github.com/codemirror/merge/blob/main/LICENSE

The Markdown commands use syntax context to continue list/quote markup, reset
task items, and stop continuation when an empty item is submitted. The unified
merge implementation highlights insertions/deletions, can collapse unchanged
regions, and provides per-chunk accept/reject controls.

**First-release adaptation:** an honest Markdown textarea with formatting
shortcuts, focus mode, preview, explicit save state and conflict feedback is
preferable to a partially implemented rich-text editor. Keep Markdown as the
source of truth. A saved-vs-draft comparison can initially use escaped before/
after panels and changed-field summaries.

**Optional reusable dependency:** CodeMirror 6's `@codemirror/state`,
`@codemirror/view`, `@codemirror/commands`, `@codemirror/lang-markdown`, and later
`@codemirror/merge`. They can be bundled at build time and embedded as local
assets, requiring no Node runtime or external CDN in the deployed Go product.
Adopt only after a reproducible asset build and editor integration are tested.

Important: CodeMirror's merge acceptance modifies editor comparison state. It
does not provide server-side approval, revision storage, authorization or publish
semantics. Those remain FOLIO application-service responsibilities.

## 5. Frictionless idea capture: Memos

- Repository: https://github.com/usememos/memos
- Reviewed editor: https://github.com/usememos/memos/blob/main/web/src/components/MemoEditor/index.tsx
- Reviewed data contract: https://github.com/usememos/memos/blob/main/proto/api/v1/memo_service.proto
- License: MIT, https://github.com/usememos/memos/blob/main/LICENSE

The reviewed contract stores Markdown, visibility, tags, attachments and
references. Unspecified visibility defaults to private. The editor distinguishes
new-entry draft recovery from editing, preserves cursor context, and treats
focus mode as a view transition rather than a separate document.

**FOLIO adaptation:** a small Idea Inbox/quick-capture entry point using the
existing draft creation operation, optionally tagged `idea`. Derive a provisional
title from the first non-empty line and let the author expand it in the normal
editor. Keep it private until explicit publication. Avoid introducing separate
notes synchronization, calendars, audio transcription or a second editor stack.

If browser draft recovery is added, label local recovery separately from a
successful server save and scope cached data to the site/user. Authentication
tokens must not be stored with draft content.

## 6. Evaluated palette alternative: Ninja Keys

- Repository and license declaration: https://github.com/ssleptsov/ninja-keys#license
- Reviewed component: https://github.com/ssleptsov/ninja-keys/blob/main/src/ninja-keys.ts
- License: MIT per the repository's license declaration

This is a usable framework-independent web component with nested commands,
hotkeys, custom styling and dark mode, implemented using Lit and hotkeys-js.
The source also includes optional Material Icons font loading.

**Decision:** useful as a reference or fallback, but not necessary for FOLIO's
small first-release action set. If adopted, bundle it locally, disable automatic
icon-font loading, use the product's own icons, and test focus/IME behavior and
dynamic command-list updates. Do not introduce CDN dependence merely for a
command menu.

## 7. Agent publishing benchmark: Halo MCP Server

- Repository and reviewed capability/security guidance: https://github.com/halo-dev/plugin-mcp-server
- License: GPL-3.0, https://github.com/halo-dev/plugin-mcp-server/blob/main/LICENSE

Halo already exposes content and attachment operations through MCP, with
per-key tool availability and recent invocation records. These are a useful
benchmark, not evidence that an agent should receive unrestricted owner access.

**FOLIO adaptation:** an agent's work appears as a recoverable draft revision,
with actor/operation provenance and a clear comparison against the live version.
Publication targets an exact revision. Read/draft/publish privileges remain
separable in the product's authorization design. No Halo source or theme code
is proposed for reuse; FOLIO's services and MCP adapter are independent.

## Small, coherent additions and acceptance checks

These are proposals in priority order, not a shipped-feature checklist:

1. **Change review:** show draft/live metadata and content differences plus
   available actor provenance. Publishing uses the reviewed revision; if a
   newer revision exists, reject the stale action and refresh the comparison.
2. **Idea Inbox:** capture a short private thought, reopen it without losing
   text, develop it in the normal editor, then explicitly publish. Verify it is
   absent from anonymous search, feeds, sitemap and taxonomy counts beforehand.
3. **Backlinks:** show meaningful incoming public references and hide an empty
   section. Verify draft links, deleted posts and unpublished revisions cannot
   leak through it. Defer this if the core publication lifecycle is not passing.

Cross-cutting UI tests: Cmd/Ctrl+K open and toggle, Escape and focus return,
keyboard-only navigation, Chinese IME Enter, empty results, rapid query changes,
repeated open/close, mobile layout, long Markdown/code/table overflow, dark mode,
reduced motion, interrupted save, server error and stale revision conflicts.

The reviewed repositories provide behavioral evidence and permissive component
options. Their presence in this document does not mean they were installed,
bundled, copied, or visually cloned.

## Implementation status at v0.1 delivery

The finished original vanilla frontend includes the keyboard command palette, idea capture into private drafts, revision-aware publication review, and Markdown editing assists/import/export. No reviewed React/Lit/CodeMirror/theme implementation was bundled. A full backlinks/digital-garden interface, dark theme and browser-local draft recovery are not shipped. The actual acceptance results, including Mac desktop/mobile browser checks and the corrected first-save history display, belong to VERIFICATION.md rather than this research proposal list.
