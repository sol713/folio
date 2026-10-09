package core

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	portable "folio/internal/migration"
)

// ImportSource is private provenance, never included in anonymous Post responses.
// Historical source dates do not rewrite actual creation/publication timestamps.
type ImportSource struct {
	SourcePath  string            `json:"source_path"`
	Date        string            `json:"date,omitempty"`
	PublishDate string            `json:"publish_date,omitempty"`
	SourceDraft bool              `json:"source_draft"`
	Extra       map[string]string `json:"extra,omitempty"`
	Warnings    []string          `json:"warnings,omitempty"`
	ContentHash string            `json:"content_hash"`
}
type importSourceKey struct{}
type migrationFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

const maxMigrationFiles = 200
const maxMigrationBytes = 8 << 20

func init() {
	fileSchema := obj(map[string]any{"path": str("Safe relative source path, never a server filesystem path"), "content": str("UTF-8 Markdown including optional supported frontmatter")}, "path", "content")
	Specs["migration.plan"] = Spec{"migration.plan", "Preview a deterministic private-draft import plan; no writes. Keep the exact plan for confirmed application.", obj(map[string]any{"files": map[string]any{"type": "array", "items": fileSchema, "maxItems": maxMigrationFiles}, "conflict": map[string]any{"type": "string", "enum": []string{"error", "skip", "update"}}}, "files"), true, "admin"}
	Specs["migration.apply"] = Spec{"migration.apply", "Apply a reviewed frozen plan to this exact instance as private drafts. Partial successes remain; retry the same plan.", obj(map[string]any{"plan": map[string]any{"type": "object", "description": "Exact immutable plan returned by migration.plan"}, "target_instance_id": str("Exact target_instance_id from plan response"), "confirm": boolean("Explicit approval of this import plan, including any draft replacements"), "continue_on_error": boolean("Continue after an individual write fails; default stops and reports pending rows")}, "plan", "target_instance_id", "confirm"), false, "admin"}
	Specs["migration.export"] = Spec{"migration.export", "Export portable Markdown and a checksum manifest with private source provenance; no publication or filesystem writes", obj(map[string]any{"status": map[string]any{"type": "string", "enum": []string{"all", "draft", "published"}}, "ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": maxMigrationFiles}}), true, "admin"}
}
func sourceFromDocument(d portable.Document) ImportSource {
	return ImportSource{d.SourcePath, d.Date, d.PublishDate, d.Draft, d.Extra, append([]string{}, d.Warnings...), d.ContentHash}
}
func validateImportSource(v ImportSource) error {
	if _, e := portable.Parse(v.SourcePath, []byte("# Source\n")); e != nil {
		return Err("validation", "Invalid migration source metadata")
	}
	if len(v.ContentHash) != 64 || strings.Trim(v.ContentHash, "0123456789abcdef") != "" || len(v.Extra) > 100 || len(v.Warnings) > 1000 {
		return Err("validation", "Invalid migration source metadata")
	}
	for _, date := range []string{v.Date, v.PublishDate} {
		if date != "" {
			if _, e := time.Parse(time.RFC3339Nano, date); e != nil {
				return Err("validation", "Invalid migration source metadata")
			}
		}
	}
	for k, value := range v.Extra {
		if !utf8.ValidString(k+value) || strings.ContainsRune(k+value, 0) || len(k) > 128 || len(value) > 64<<10 {
			return Err("validation", "Invalid migration source metadata")
		}
	}
	b, _ := json.Marshal(v)
	if len(b) > 256<<10 {
		return Err("validation", "Migration source metadata exceeds 256 KiB")
	}
	return nil
}
func validateMigrationSources(st State) error {
	for key, v := range st.ImportSources {
		if _, ok := st.Posts[key]; !ok {
			return Err("validation", "Migration source refers to a missing post")
		}
		if e := validateImportSource(v); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) migrationSnapshot(ctx context.Context) ([]portable.Existing, map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.refresh(ctx, s.db, false); e != nil {
		return nil, nil, e
	}
	existing := []portable.Existing{}
	reserved := map[string]string{}
	for id, r := range s.State.Posts {
		if r.Deleted {
			continue
		}
		existing = append(existing, portable.Existing{ID: id, Slug: r.Draft.Slug, Revision: r.Draft.Revision})
		if r.Live != nil && r.Live.Slug != r.Draft.Slug {
			reserved[r.Live.Slug] = id
		}
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i].Slug < existing[j].Slug })
	return existing, reserved, nil
}
func migrationPlanIssues(plan portable.ImportPlan, reserved map[string]string) []portable.Diagnostic {
	issues := []portable.Diagnostic{}
	for _, entry := range plan.Entries {
		if owner, ok := reserved[entry.Document.Slug]; ok && owner != entry.TargetID {
			issues = append(issues, portable.Diagnostic{SourcePath: entry.Document.SourcePath, Code: "published_slug_reserved", Message: "This slug belongs to another published revision; choose a different slug"})
		}
		if e := validateImportSource(sourceFromDocument(entry.Document)); e != nil {
			issues = append(issues, portable.Diagnostic{SourcePath: entry.Document.SourcePath, Code: "invalid_source_metadata", Message: e.Error()})
		}
	}
	return issues
}
func (s *Store) callMigration(ctx context.Context, op string, raw json.RawMessage, actor string) (any, error) {
	if actor != "admin" {
		return nil, Err("forbidden", "Only the owner can migrate private content")
	}
	switch op {
	case "migration.plan":
		var a struct {
			Files    []migrationFile `json:"files"`
			Conflict string          `json:"conflict"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if len(a.Files) == 0 || len(a.Files) > maxMigrationFiles {
			return nil, Err("validation", "Provide between 1 and 200 Markdown files")
		}
		docs := []portable.Document{}
		issues := []portable.Diagnostic{}
		total := 0
		for _, file := range a.Files {
			total += len(file.Content)
			if total > maxMigrationBytes {
				return nil, Err("validation", "Migration input exceeds 8 MiB")
			}
			doc, e := portable.Parse(file.Path, []byte(file.Content))
			if e != nil {
				issues = append(issues, portable.Diagnostic{SourcePath: file.Path, Code: "parse_error", Message: e.Error()})
				continue
			}
			docs = append(docs, doc)
		}
		existing, reserved, e := s.migrationSnapshot(ctx)
		if e != nil {
			return nil, e
		}
		plan, e := portable.Plan(docs, existing, portable.Options{Conflict: a.Conflict})
		if e != nil {
			return nil, Err("validation", "Invalid migration plan: %s", e)
		}
		issues = append(issues, migrationPlanIssues(plan, reserved)...)
		plan = portable.WithDiagnostics(plan, issues)
		summary := map[string]int{"create": 0, "update": 0, "skip": 0, "blocked": 0}
		for _, entry := range plan.Entries {
			summary[entry.Action]++
		}
		summary["blocked"] += len(issues)
		return map[string]any{"plan": plan, "target_instance_id": s.InstanceID(), "can_apply": len(issues) == 0 && portable.ValidatePlan(plan) == nil, "validation_issues": issues, "summary": summary}, nil
	case "migration.apply":
		var a struct {
			Plan     portable.ImportPlan `json:"plan"`
			Target   string              `json:"target_instance_id"`
			Confirm  bool                `json:"confirm"`
			Continue bool                `json:"continue_on_error"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if !a.Confirm {
			return nil, Err("confirmation_required", "Set confirm:true to apply this private-draft import plan")
		}
		if a.Target == "" || a.Target != s.InstanceID() {
			return nil, Err("conflict", "Migration plan targets a different instance")
		}
		if len(a.Plan.Entries) > maxMigrationFiles {
			return nil, Err("validation", "Migration plan exceeds 200 files")
		}
		if e := portable.ValidatePlan(a.Plan); e != nil {
			return nil, Err("validation", "Invalid migration plan: %s", e)
		}
		total := 0
		for _, entry := range a.Plan.Entries {
			total += len(entry.Document.Markdown)
		}
		if total > maxMigrationBytes {
			return nil, Err("validation", "Migration input exceeds 8 MiB")
		}
		_, reserved, e := s.migrationSnapshot(ctx)
		if e != nil {
			return nil, e
		}
		if len(migrationPlanIssues(a.Plan, reserved)) > 0 {
			return nil, Err("validation", "Migration plan has invalid source metadata or a reserved published slug")
		}
		sources := map[string]ImportSource{}
		for _, entry := range a.Plan.Entries {
			if entry.IdempotencyKey != "" {
				sources[entry.IdempotencyKey] = sourceFromDocument(entry.Document)
			}
		}
		caller := portable.CallerFunc(func(inner context.Context, operation string, args json.RawMessage) (json.RawMessage, error) {
			if operation != "posts.create" && operation != "posts.update" {
				return nil, Err("forbidden", "Migration may only create or update private drafts")
			}
			var key struct {
				Key string `json:"idempotency_key"`
			}
			if e := json.Unmarshal(args, &key); e != nil {
				return nil, e
			}
			source, ok := sources[key.Key]
			if !ok {
				return nil, Err("validation", "Migration write is not in the approved plan")
			}
			value, e := s.Call(context.WithValue(inner, importSourceKey{}, source), operation, args, actor)
			if e != nil {
				return nil, e
			}
			return json.Marshal(value)
		})
		report, e := portable.Apply(ctx, caller, a.Plan, portable.ApplyOptions{ContinueOnError: a.Continue})
		if e != nil && !errors.Is(e, portable.ErrPartialApply) {
			return nil, Err("validation", "Invalid migration plan: %s", e)
		}
		return map[string]any{"report": report, "target_instance_id": s.InstanceID()}, nil
	case "migration.export":
		var a struct {
			Status string   `json:"status"`
			IDs    []string `json:"ids"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if a.Status == "" {
			a.Status = "all"
		}
		if a.Status != "all" && a.Status != "draft" && a.Status != "published" {
			return nil, Err("validation", "Export status must be all, draft, or published")
		}
		if len(a.IDs) > maxMigrationFiles {
			return nil, Err("validation", "Migration export exceeds 200 files")
		}
		s.mu.Lock()
		if e := s.refresh(ctx, s.db, false); e != nil {
			s.mu.Unlock()
			return nil, e
		}
		selected := map[string]bool{}
		for _, id := range a.IDs {
			selected[id] = true
		}
		docs := []portable.Document{}
		for id, r := range s.State.Posts {
			if r.Deleted || len(selected) > 0 && !selected[id] {
				continue
			}
			p := r.Draft
			if a.Status == "published" {
				if r.Live == nil {
					continue
				}
				p = *r.Live
			} else if a.Status == "draft" && p.Status == "published" {
				continue
			}
			d := portable.Document{SourcePath: p.Slug + ".md", Title: p.Title, Slug: p.Slug, Markdown: p.Markdown, Excerpt: p.Excerpt, Tags: append([]string{}, p.Tags...), Category: p.Category, Cover: p.Cover, Featured: p.Featured, Draft: true, Warnings: []string{}}
			if source, ok := s.State.ImportSources[id]; ok {
				d.SourcePath = source.SourcePath
				d.Date = source.Date
				d.PublishDate = source.PublishDate
				d.Draft = source.SourceDraft
				d.Extra = source.Extra
				d.Warnings = append([]string{}, source.Warnings...)
			} else {
				if t, e := time.Parse(time.RFC3339Nano, p.CreatedAt); e == nil {
					d.Date = t.UTC().Format(time.RFC3339Nano)
				}
				if t, e := time.Parse(time.RFC3339Nano, p.PublishedAt); e == nil {
					d.PublishDate = t.UTC().Format(time.RFC3339Nano)
				}
			}
			d.ContentHash = portable.HashDocument(d)
			docs = append(docs, d)
		}
		s.mu.Unlock()
		if len(docs) > maxMigrationFiles {
			return nil, Err("validation", "Migration export exceeds 200 files")
		}
		bundle, e := portable.BuildExport(docs)
		if e != nil {
			return nil, Err("validation", "Could not export Markdown: %s", e)
		}
		files := []map[string]any{}
		for _, file := range bundle.Files {
			files = append(files, map[string]any{"path": file.Path, "content": string(file.Data), "sha256": file.SHA256, "bytes": file.Bytes})
		}
		var manifest any
		if e = json.Unmarshal(bundle.Manifest, &manifest); e != nil {
			return nil, e
		}
		return map[string]any{"format": "folio-markdown-bundle", "version": 1, "files": files, "manifest": manifest}, nil
	}
	return nil, Err("not_found", "Unknown migration operation")
}
