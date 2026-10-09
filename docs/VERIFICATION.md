# Verification record

## 2026-10-09 cloud refinement

Fresh clone, original baseline, confirmed defects, regression fixes and actual Docker/Chromium runtime acceptance are recorded in [CLOUD-REFINEMENT.md](CLOUD-REFINEMENT.md) and [CLOUD-VERIFICATION.json](CLOUD-VERIFICATION.json). This dated record includes executed container checks; Docker-unavailable statements below describe the earlier release environments.

## v0.2 release verification

Status: final cloud gates and scoped Mac browser acceptance pass. The native-download routing defect found in Mac QA is fixed and all five export types were retested as actual files on disk. No blocking defect remains in the exercised release scope.

- Toolchain: Go 1.27.1, declared Go 1.25 minimum; Linux amd64 static stripped executable, 14,794,912 bytes
- Extracted release source archive passes the complete Go suite and rebuilds a byte-identical final executable in a fresh temporary directory
- Final executable SHA-256: `723b663ca71324fa18d2a95ecbbf95493c827e871d65cae4c986ffef2775ad04`
- Complete Go tests, race detector and vet passed after integrating migration, proposals, schedules and bilingual interfaces
- govulncheck v1.8.0: no vulnerabilities found
- Real-binary base lifecycle: 12 passing groups, including exact HTTP/CLI/MCP schema parity for all 33 operations
- Operations: 66 Python unit tests; 13 real Linux-binary groups, including actual v0.1→v0.2 recovery, overdue pinned schedules paused during restore, and old-v0.1-binary rejection of schema 2 without data loss
- All five Node source-level contract suites pass: 765-key locale/diagnostic coverage, textarea placeholders and dirty/in-flight caret/scroll preservation, revision retries, proposal approval guards, pinned-schedule confirmation, and migration frozen-plan/uncertain-result retries, file boundaries and private-only completion. Native-download regression exercises all five real export actions and the actual document listener; the original-listener negative control fails on frozen-plan download. These do not substitute for browser checks
- Expanded real-binary workflow gate: 11 groups covering Chinese Markdown migration → stale proposal CAS → private-draft approval → scheduled restart → exact pinned publication, invalid-plan/target/scope checks, partial-success retry receipts, and paused recovery
- Pre-fix Mac browser checks passed Chinese/English public and Studio surfaces, persisted locale refresh, mixed-language authored content, private preview, publication review, and dirty-editor switching with caret 6520 and nonzero scrollTop 7090.5 unchanged. Save-then-switch succeeded, but actual server in-flight timing was not observed; pending-request preservation is covered by Node tests. The observed untranslated textarea placeholders were reproduced and fixed in a regression test
- Fresh isolated Mac QA on the matching source passes all three Chinese textarea placeholders and login hero typography; actual file-chooser import with source draft:false creates private revision 1; 390px Chinese proposal diff/approval creates private revision 2 with live=null; UI-confirmed schedule pins revision 2 while later draft revision 3 stays private. Chinese/English modals have no horizontal overflow at 390px
- Mac additionally passes Go 1.25 build/test/race/vet and all four Node suites. Proposal fixture creation used admin access; least-privilege roles are tested by Go and real CLI/MCP gates. Its QA server deliberately used --pause-schedules, so actual wall-clock publication is established by cloud E2E, not claimed as a browser observation
- Mac rejected-plan review passed with can_apply:false and Apply disabled. Mac then found SPA interception of a same-origin blob download; the shared click guard now leaves download anchors and non-HTTP(S) URLs browser-native
- Fresh corrected Mac instance verifies all five actual downloaded files: frozen plan (1,602 bytes), import report (392), portable bundle (1,886), Markdown (124), and logical backup (3,401). Parsed files preserve plan IDs, manifest hashes, source dates/custom metadata, private-draft status and exact Markdown; backup checksum validates through Go canonical JSON and includes one fictional post with no token. Every download leaves the URL unchanged; ordinary migration/editor/tools/writing navigation works. Download event callbacks were unavailable, so disk files and content checks are the evidence
- Docker daemon is unavailable; no container runtime or production deployment is claimed

## Historical v0.1 verification

Build under test: FOLIO 0.1.0, Linux amd64, Go 1.27.1; declared minimum Go 1.25.

## Executed successfully

- `go test ./...`: unit/API tests across core, server, CLI and MCP adapters
- `go test -race ./...`: race detector across complete Go test suite
- `go vet ./...`: static analysis
- `node --check web/app.js`: frontend syntax
- `go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio`: executable with embedded assets
- `python3 scripts/e2e.py --binary dist/folio`: ten real-daemon/CLI/MCP lifecycle groups, isolated temporary directories and ephemeral loopback ports
- `go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...`: **No vulnerabilities found** after upgrading Goldmark to 1.8.6 and x/net to 0.56.0 (2026-10-03 scan)

The real-binary harness uses actual official MCP stdio protocol initialization, all 19 discovered tool schemas, tools/call, structured errors and clean EOF. It verifies draft creation, preview, explicit publication, immutable live content after a draft edit, stale revision rejection, scope enforcement, trash/recovery, historical revision restore, upload bytes, export/restore into a new instance, process restart persistence, and absence of test bearer values in captured stdout/stderr. An in-flight-body SIGTERM test verifies an admitted HTTP request completes successfully before process exit; its negative control correctly failed on the pre-fix binary. It also verifies role-token collisions are rejected and configured HTTPS origins work behind trusted termination without accepting arbitrary origins.

Core/API regression tests include raw HTML and link XSS, published-only search/RSS/sitemap/SSR, upload names/type/size, checksum-valid malformed backups, null records, cross-record revision IDs, duplicate slugs, byte hashes, ordinary and malformed Origin values, strict Content-Type, malformed/oversized JSON, idempotency key mismatches, and concurrent cross-handle CAS. Canonical backups survive whitespace, property-order, Unicode, HTML and slash escaping differences.

## Bounded performance sample

`go test ./internal/core -run '^$' -bench BenchmarkPersonalSite -benchtime=10x -benchmem`

Fixture: 100 posts (90 public), about 4 KiB Markdown each, 10 generated 128×128 PNGs, 7.87 MiB SQLite/WAL/SHM footprint; Linux AMD EPYC environment. Representative averages:

| Operation | Time |
|---|---:|
| Public snapshot | 0.143 ms |
| Private preview | 0.418 ms |
| Revision-guarded draft update | 6.374 ms |
| Full logical export | 13.27 ms |

Draft update allocation: about 0.889 MB/operation. These are microbenchmarks on a small fixture, not HTTP load, tail latency, memory-resident size, long-term revision-growth, concurrency scale, or multi-gigabyte media benchmarks. Copy-on-write targeted record cloning reduced earlier measured draft-update allocation by about 98.9%; immutable history still grows over time.

## Browser verification and remaining checks

Mac Go 1.25.7 builds and the actual local in-app browser have verified desktop mixed Chinese/English long-form editing with code, draft save/preview/publish, Escape/cancel focus restoration, published revision 1 staying live while revision 2 remains private, publication of revision 2, 390px mobile article without page-level horizontal overflow, mobile navigation, and Chinese command-palette keyboard navigation. The first-save history-display defect was found, fixed, and retested in the real Mac browser: counts 1/2, Current/Restore controls, cancel stability, restore producing revision 3, reload persistence, and caret selection preservation all passed. The final retry-hardening patch is additionally covered by deterministic simulated lost-response tests, proving exact key/payload reuse and no duplicate creation while newer typing is retained. Cloud Browser denied loopback preview (`ERR_BLOCKED_BY_CLIENT`); this was not bypassed. The actual desktop journal/Studio and mobile article/editor screenshots were inspected and approved for this release’s visual design. This is not an exhaustive browser/device matrix or a formal WCAG audit. Docker runtime verification depends on an available daemon; configuration inspection is not a successful container run. No production server was deployed.

## Lightweight local process sample

Static Linux amd64 binary: 14,352,544 bytes, confirmed by `file` as statically linked (`ldd`: not a dynamic executable). On a disposable six-post demo after 25 sequential public API reads, `/proc/<pid>/status` reported RSS 18,260 kB and 11 threads; data-directory files including WAL/SHM totaled 102,465 bytes. This is one warm-idle observation, not a maximum-memory promise or a measured comparison with Halo.

## Operations integration

The integrated Python standard-library operations package passed 57 unit tests and 10 real Linux-binary integration groups. Coverage includes probe/doctor, logical export, new-instance restore, exact content and expected audit/revision transitions, isolated recovery drill, source preservation, corrupted/non-empty restore refusal, stopped offline archive/verification/new-directory restore/restart, image-byte preservation, non-root Compose ownership checks, and actual static ELF/CA staging. UTC RFC3339 fractional timestamps are supported. The package manifest was verified and regenerated after report updates. Container lifecycle unit tests use a fake Docker driver; Docker itself is unavailable and no container run or production deployment is claimed.
