# Working on FOLIO

- Go core operations are the single source of truth for writes; keep CLI, MCP and Studio parity
- Read docs/ARCHITECTURE.md and docs/SECURITY.md before persistence or permission changes
- Never publish on draft save. Never expose draft metadata from public APIs
- Every write needs explicit schema, validation, appropriate scope, audit event and revision/retry behavior
- Frontend is original vanilla HTML/CSS/JS embedded into the Go binary; no build step required
- Tests: go test ./...; go test -race ./...; go vet ./...; build with -buildvcs=false in non-git archives
- Use scripts/e2e.py for real daemon, CLI and MCP lifecycle checks
- Do not add fake AI output or hidden provider calls
- Do not commit data directories, bearer tokens, live backups, logs or generated binaries
- Rebuild the binary after web changes; embedded assets are compile-time inputs
