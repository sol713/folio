# Third-party notices

Runtime dependencies are pinned in go.mod/go.sum. Their original source/license terms apply:

- modelcontextprotocol/go-sdk — MIT — https://github.com/modelcontextprotocol/go-sdk
- modernc.org/sqlite and translated SQLite bindings — BSD-3-Clause and upstream SQLite public domain — https://pkg.go.dev/modernc.org/sqlite
- yuin/goldmark — MIT — https://github.com/yuin/goldmark
- microcosm-cc/bluemonday — BSD-3-Clause — https://github.com/microcosm-cc/bluemonday

The checked-in [module inventory](third_party/licenses/modules.json) contains FOLIO plus **43 upstream modules**. All 43 now have their original license files in `third_party/licenses/`, including the previously omitted compute/metadata, x/crypto, x/term and x/text modules. License and accompanying PATENTS/AUTHORS files are retained unchanged. Additional go.sum entries authenticate those four exact existing versions; go.mod and dependency versions are unchanged.

[License coverage](third_party/licenses/coverage.json) records every included file's SHA256 and distinguishes dependencies actually selected by `go list -deps ./cmd/folio` and `go list -deps -test ./...` from modules present only in the wider build graph. The recorded Linux/amd64 Go 1.27.2 build selects **21 upstream runtime modules**. The four newly covered modules are graph-only in this build, rather than code linked into this executable. Selection can differ for another platform, build tag or toolchain; the full inventory is included conservatively in source and binary packages.

The compiled Go runtime and standard library carry the Go project's BSD-style [LICENSE](third_party/licenses/Go/LICENSE) and [PATENTS](third_party/licenses/Go/PATENTS), also retained from the verified official toolchain. The application's MIT license does not replace these upstream terms. Run `python3 scripts/check_licenses.py` to validate checked-in inventory coverage and license file hashes.

The Docker packaging tool requires an operator-supplied, hash-verified CA bundle; no certificate bundle is checked into FOLIO source. An image distributor must retain the notices applicable to the bundle they select. This local acceptance used the host OS bundle and did not publish or redistribute an image.

The original FOLIO frontend is informed by public interaction research, not copied component implementations. See docs/INSPIRATION.md for reviewed sources, exact file links and licenses. No upstream theme images, fonts or logos are bundled.
