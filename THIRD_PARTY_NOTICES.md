# Third-party notices

Runtime dependencies are pinned in go.mod/go.sum. Their original source/license terms apply:

- modelcontextprotocol/go-sdk — MIT — https://github.com/modelcontextprotocol/go-sdk
- modernc.org/sqlite and translated SQLite bindings — BSD-3-Clause and upstream SQLite public domain — https://pkg.go.dev/modernc.org/sqlite
- yuin/goldmark — MIT — https://github.com/yuin/goldmark
- microcosm-cc/bluemonday — BSD-3-Clause — https://github.com/microcosm-cc/bluemonday

Transitive dependency licenses are present in each fetched Go module. `go list -m all` reports the exact build graph. For redistributed binary compliance, license files are included in `third_party/licenses/` by the release packaging step.

The original FOLIO frontend is informed by public interaction research, not copied component implementations. See docs/INSPIRATION.md for reviewed sources, exact file links and licenses. No upstream theme images, fonts or logos are bundled.
