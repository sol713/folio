# Frontend regression checks

Run from the repository root:

```sh
node --check web/app.js
node --check web/i18n.js
node web/tests/revision-history.test.cjs
node web/tests/i18n.test.cjs
node web/tests/workflows.test.cjs
node web/tests/migration.test.cjs
```

These are source-level DOM-contract checks. They do not claim actual browser or visual coverage.

The i18n check verifies dictionary and placeholder parity, zh-CN default, locale persistence, live labels/ARIA/date/count/textarea-placeholder changes, all primary public and Studio view text (including migration input, review, reports and locked retry states), native confirmation/error/command literal coverage, dynamic dialog templates, real capability descriptions (including migration), known migration parser/plan diagnostics and nested error explanations, user-content collision boundaries, and CJK-aware reading counts. It switches locale during an actual `saveDraft` call with a pending promise and verifies the buffer, caret, selection, scroll, editor object, and pending request key remain intact. It also checks `Accept-Language` on the request.

The migration check covers explicit confirmation, byte-identical frozen payload retries after lost/malformed/partial responses, selected source/state preservation across locale changes, content escaping, locked reset confirmation, and the 200-file/8 MiB/extension/UTF-8 boundaries. Requests and rendering are mocked; this does not provide native FileList or backend integration coverage.

The revision test covers first-save history, later revisions, lost-response create retries with identical payload/key, newer text after an uncertain create, and preservation of in-flight edits/focus.

Locale behavior is presentation-only. `FolioI18n.setLocale()` never calls application render, save, publish, or network APIs. Translatable interface nodes receive inspectable `data-i18n` keys. User content and machine-readable regions are explicitly excluded with `data-no-i18n`, `.prose`, code, and schema boundaries. Textarea contents remain excluded while interface placeholders, titles, and ARIA labels translate in place; explicit opt-outs still exclude attributes. Unknown diagnostic details fall back verbatim while their known wrapper text translates.

## Native downloads and navigation

`node web/tests/navigation.test.cjs` exercises the actual document click listener, export action dispatcher and SPA navigation. It verifies frozen-plan, import-report, portable-bundle, editor-Markdown and backup downloads remain browser-native, with the current URL, dirty editor state and history unchanged. Native targets, download attributes, non-HTTP schemes, modifiers, mouse buttons and prior preventDefault are respected; ordinary HTTP/HTTPS and keyboard navigation still use the SPA. An isolated negative control using the original listener fails on the reported frozen-plan download regression. Actual file creation still requires browser verification.
