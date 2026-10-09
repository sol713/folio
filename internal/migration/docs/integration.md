# Integration contract

Copy `types.go`, `parse.go`, `plan.go`, `apply.go`, `export.go`, `fs.go` and their tests/fixtures into `folio/internal/migration`. Keep LICENSE and REFERENCES. Do not copy this standalone go.mod into the parent module. The optional CLI imports `folio-migration`; change that import to `folio/internal/migration` only if incorporating its standalone command. Existing Folio CLI can instead call ScanDirectory and submit content JSON to migration.plan. No dependencies or go.sum are required.

The package name is `migration`. All operation schemas and authentication remain in Folio. Package import direction is core/adapters → migration; migration imports only stdlib, so no core import cycle.

## Memory request and output

Input files should have a relative `path` plus `content`, bounded by the parent operation before Parse. Do not expose local `directory` or server file paths through HTTP/MCP. Parse emits Document with the following JSON fields:

```
source_path, title, slug, markdown, excerpt, tags, category, cover, featured,
date?, publish_date?, draft, extra? (map<string,string> source literals),
warnings (array<string>), content_hash
```

Existing snapshot fields: `id`, `slug`, `revision`. Options: `conflict: error|skip|update` (empty = error).

Plan/BuildPlan returns ImportPlan with `format:"folio-migration-plan"`, `version:1`, `id`, `entries`, `diagnostics`. Each entry includes `document`, `action:create|update|skip|blocked`, and optional `target_id`, `expected_revision`, `idempotency_key`. Diagnostic fields are `source_path`, `code`, `message`. Arrays are stable; entries sorted by source_path; diagnostics sorted by path/code/message; map keys use Go encoding/json deterministic ordering.

`content_hash` uses SHA256 of JSON Document after clearing SourcePath/Warnings/ContentHash and canonicalizing Tags/empty Extra. `plan.ID` uses SHA256 of ImportPlan JSON with ID empty. Keys are `migration-` plus SHA256 of `action + ':' + JSON postRequest_without_key`. Field order and Go JSON escaping are part of version1. Prefer calling the Go implementation rather than reimplementing hashes in JavaScript.

## Apply adapter

```go
caller := migration.CallerFunc(func(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
    // Invoke the application's existing authorized operation gateway.
    // Do not recursively call the migration wrapper and do not hold a Store mutex
    // around the entire Apply loop. Individual Store operations own their locks.
    data, err := authorizedOperation(ctx, op, args)
    if err != nil { return nil, err }
    return json.Marshal(data) // unwrapped data, not {ok:true,data:...}
})
report, err := migration.Apply(ctx, caller, frozenPlan, migration.ApplyOptions{})
```

Apply validates the entire plan first, then only calls posts.create/update. Create requests omit ID, expected_revision, date, draft, extras, publication fields, HTML and status. Update requests contain target ID and fixed expected_revision. Both carry exactly the supported editable fields and deterministic idempotency_key. Do not auto-publish in the adapter, and do not infer permissions from plan hashes.

Partial report: `plan_id`, `complete`, `outcomes[]`. Each outcome has source_path/action/status and optional idempotency_key/post_id/revision/error. Status `failed` may indicate a committed write whose receipt was lost. `ErrPartialApply` signals incomplete migration; successful rows are retained. Frozen request replay works because core checks persistent key/fingerprint before revision CAS.

The wrapper should bind to a target-instance identity and authorized actor, enforce source/target identity before apply, and ensure the snapshot includes reserved/trash slugs where possible. Re-planning after partial success can produce different conflicts; retry the original plan. When replacing an existing draft, all editable fields are replaced; missing source fields become empty/default. This must be visible in the plan review.

## Export

Map an authorized current draft Post into Document; fill source_path with a safe name such as slug + `.md`, Draft true for safety, and set Date/PublishDate only if intentionally mapping CreatedAt/PublishedAt metadata (normalize RFC3339). Clear ContentHash then use HashDocument. Do not export online HTML as editable source.

`Export` is a single Markdown byte slice. `BuildExport` emits Files (`Path`, `Data` bytes, `SHA256`, `Bytes`) and Manifest bytes. These are in-memory Go fields; a parent HTTP/MCP schema should explicitly convert Data and Manifest to text or a declared base64/file artifact representation. They are not an implicit full-instance backup.

Unknown fields and source dates live in ManifestItem.Source. DocumentsFromExport validates in-memory files/manifest and returns documents including preserved metadata. Safe plain Markdown is independently usable, but loses these extra fields unless the manifest is retained.

## Scope retained by parent

No support for historical timestamp writes in current post API: date/publishDate do not change CreatedAt/PublishedAt or schedule a post. If adding such support later, expose explicit reviewed core operations and maintain revision/audit/idempotency semantics. No batch atomicity claim. Proposal/schedule/i18n features are independent parent work; this package neither implements nor invokes them. Parent should localize its wrapper presentation; diagnostic codes are stable, package strings are English.
