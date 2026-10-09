# Cloud refinement — 2026-10-09 UTC

本次从 `sol713/folio` 的原始提交 `8bfeb420aa23c62e44ebf2b19c17a72e58d89d20` 开始，tree 为 `e79b726ab1bd65695feac745946c5dc84a315c5c`。独立目录中的原始 worktree 用于基线和负向复现；精修保留 Go 核心、原生前端、33 操作及中英设计。没有公网部署。

This record describes checks actually executed in this cloud environment. Historical v0.2/Mac acceptance is retained separately in [VERIFICATION.md](VERIFICATION.md).

## Confirmed defects and fixes

| Trigger | Before | After and regression evidence |
|---|---|---|
| `init` encounters an invalid SQLite file | Credentials remain; correcting storage and retrying init is refused | Failed init removes only the credential inode it created, preserves existing data, and permits same-directory retry. Go and real CLI tests pass |
| `token` is a dangling symlink, or initializers race | `Stat` misses the dangling link; the write follows it. Check/write is not exclusive | Exclusive create refuses existing entries, including dangling links. Sixteen concurrent real initializers have exactly one successful credential owner |
| Lock Studio or browser Back during a save | Lock erases session/editor state; Back can rerender/discard while the write is pending | Active writes preserve the session, exact editor URL, buffer, caret and scroll. Three Node regressions fail before the fix and pass afterward; real Chromium reproduces the old Lock defect and passes the new flow |
| Close a pending publish dialog and continue typing | Successful publication rerenders and overwrites newer unsaved text; retry uses a fresh write key | Publication keeps the reviewed payload/key, protects navigation while pending, and reflects committed metadata without replacing newer text. Real daemon commit followed by dropped HTTP response retries once, with one publication audit event |
| Read the homepage at 320px | Grid min-content width expands article cards beyond the viewport | Cards can shrink and long text wraps. The previously overflowing fixture passes page-width checks at 320px and 390px |

Publication continues to activate the explicitly reviewed revision. Later typing remains private and dirty until saved. No operations, permission scopes, persistence schema, or automatic publishing behavior were added.

## Executed acceptance

Toolchain: official SHA256-verified Go 1.27.2, Linux/amd64; Node 24.19.0; Python 3.12.14; Playwright 1.62.0 and system Chromium 151.0.7922.173; Docker 28.4.0 client/daemon.

- Original worktree: `go test ./...`, `go test -race ./...`, `go vet ./...`, static build, five frontend contract files, 66 ops unit tests, 12 real lifecycle groups and 11 real workflow groups pass.
- Refined source: complete Go tests/race/vet and module integrity pass. Three new Go bootstrap regressions and three real CLI initialization groups pass. Six frontend test files execute eight passing Node test units, including all 766 bilingual keys.
- Refined executable: all 12 lifecycle and 11 workflow groups pass again, including exact HTTP/CLI/MCP schema parity for all 33 operations, role denials, stale-base CAS, immutable proposal approval, reviewed Markdown migration retries, paused restore, and exact pinned scheduled publication after restart.
- Operations: all 11 available real-binary integration groups pass. The optional real v0.1 binary migration/old-writer drill was not executed because no trusted v0.1 executable is present; historical coverage is not counted here.
- Actual container: the shipped scratch Dockerfile builds from the verified static ELF and host CA bundle, without downloading an application executable or base image. The container uses UID 1000 matching its disposable private bind mount, a read-only root filesystem, dropped capabilities and loopback-only host ports. Its image default remains UID 65532. Real healthcheck, SIGTERM/restart, exactly-once overdue pinned publication, independent-token paused backup restore and byte-identical media pass. Test containers and image tags are cleaned up.
- Real browser: eight passing groups cover default Chinese/English reader and sign-in, private first save/history, held-response Lock/Back/locale/caret preservation, lost-publication-response retry and newer typing retention, explicit publication/private edits, revision/time-pinned schedule review, 390px owner approval of proposal-only agent text, inert untrusted diffs, all five actual native downloads and private Markdown import. Chinese homepage/article/editor/schedule/migration surfaces pass page-width checks at both 390px and 320px. No browser JavaScript errors were observed.
- Source boundary: 179 original tracked files and the complete proposed 202-file source snapshot were checked for private-key/token patterns, host-user paths and runtime data; none were found by that scan. There are no source symlinks or runtime artifacts. All seven captures contain only fictional demo/acceptance writing. Credentials, SQLite files, logs, downloads/backups and generated executables stay outside the repository.

Machine-readable results are in [CLOUD-VERIFICATION.json](CLOUD-VERIFICATION.json). The tested unstripped static executable is 21,820,686 bytes, SHA256 `c7af03c2f78734dafb09a57bee0e21fb9edbdb6c8d738e6c3413701d980bc46a`.

## Reproduce the new gates

```sh
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o dist/folio ./cmd/folio
python3 scripts/reliability_e2e.py --binary dist/folio
python3 scripts/docker_e2e.py --binary dist/folio --report /tmp/folio-docker-results.json
python3 scripts/browser_e2e.py --binary dist/folio --chromium /path/to/chromium --output /tmp/folio-browser-results
python3 scripts/check_licenses.py
```

The browser gate requires Python Playwright 1.62.0 and a local Chromium executable. The Docker gate requires a daemon and a host CA bundle; it stages a private Docker configuration inside its temporary directory, so no writable home-directory configuration is required. Both gates use disposable fictional instances and clean up their processes.

## Dependency notices and review boundary

The original module inventory has 44 entries: FOLIO and 43 upstream modules. All upstream entries now have license coverage. The four previously omitted modules are graph-only in the tested Linux build; 21 modules are actually selected by the executable's package dependencies. Original license/PATENTS/AUTHORS bytes and the Go runtime/standard-library notices are retained, with 70 file hashes validated by `scripts/check_licenses.py`. No dependency version was changed. See [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).

`govulncheck@v1.8.0 ./...` was attempted, but the official vulnerability index at `https://vuln.go.dev/index/modules.json.gz` returned HTTP 403 before analysis. Dependency integrity and notice checks passed; this blocked vulnerability scan does not establish that dependencies have no known vulnerabilities.

The proposed CI uses standard `ubuntu-24.04` public-repository runners, official Actions pinned to verified commit SHAs, `contents: read`, no persisted checkout credential, no artifact upload, and disabled Go cache. It proposes Go 1.25.x/1.27.x gates. GitHub execution is a separate observation and is not implied by this local record. Standard public-repository runner billing is described by [GitHub](https://docs.github.com/en/billing/concepts/product-billing/github-actions).

Browser coverage is Linux Chromium, rather than a full device/accessibility matrix. The UI daemon pauses schedules for deliberate review; actual clock-driven execution is established by the CLI/workflow and Docker checks. Internet deployment, real-user data migration, multi-user identities, crash-proof browser-local unsaved drafts and a production security audit remain outside this refinement.

## Fictional browser captures

- [Desktop journal](screenshots/cloud-2026-10-09/home-desktop.png)
- [Mobile proposal review](screenshots/cloud-2026-10-09/proposal-mobile.png)
- [Mobile editor/history](screenshots/cloud-2026-10-09/editor-mobile.png)

The four original v0.2 screenshots remain tracked and unchanged. The root screenshot ignore is now anchored to `/screenshots/` so future documentation captures are not silently excluded.
