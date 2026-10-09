# References and license boundaries

Inspected 2026-10-03. These sources informed format conventions and the preflight/plan/apply workflow. All implementation and fixture prose in this module are original; no upstream code, tests, logos, documentation passages, or binaries were copied or executed.

- Hugo official frontmatter documentation: https://gohugo.io/content-management/front-matter/ — YAML/TOML format conventions, distinct date/publishDate, draft, title, slug, description, tags. This module implements the explicitly documented flat subset, not Hugo's full grammar or rendering semantics.
- Hugo license: https://github.com/gohugoio/hugo/blob/master/LICENSE and https://raw.githubusercontent.com/gohugoio/hugo/master/LICENSE — Apache-2.0. No Hugo code is vendored, so this package does not relabel Hugo code under MIT. If future integration copies upstream code, retain its complete license/notice and modified-file notices.
- Obsidian official properties source: https://github.com/obsidianmd/obsidian-help and https://raw.githubusercontent.com/obsidianmd/obsidian-help/master/en/Editing%20and%20formatting/Properties.md — YAML properties and tags/aliases conventions. A standalone license for this help repository was not verified; no Obsidian source/docs are redistributed. Obsidian application code was not obtained or executed.
- Memos export design: https://github.com/usememos/memos/blob/main/docs/design/memos-export-format.md — readable Markdown plus versioned manifest, input checksums, stable retry workflow. https://usememos.com/docs/usage/export-import — preflight and conflict handling.
- Memos license: https://github.com/usememos/memos/blob/main/LICENSE — MIT. No Memos code or schema is vendored. This module does not read/write Memos full backup format and does not claim interoperability with it.
- Local Go 1.25.7 `go doc os.Root` — root-bound directory APIs and symlink/privileged filesystem caveats. https://pkg.go.dev/os#Root is the canonical API reference.

The module carries the Folio project's MIT LICENSE unchanged. It adds no third-party runtime dependency. Upstream licenses remain theirs; references are not a grant of permission to copy unlicensed material.
