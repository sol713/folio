package migration

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Plan is the in-memory entry point for HTTP/MCP adapters. It never reads files.
func Plan(documents []Document, existing []Existing, options Options) (ImportPlan, error) {
	p := ImportPlan{Format: "folio-migration-plan", Version: Version, Entries: []Entry{}, Diagnostics: []Diagnostic{}}
	if len(documents) > MaxDocuments || len(existing) > 100000 {
		return p, fmt.Errorf("too many input documents or existing records")
	}
	policy := options.Conflict
	if policy == "" {
		policy = "error"
	}
	if policy != "error" && policy != "skip" && policy != "update" {
		return p, fmt.Errorf("conflict policy must be error, skip, or update")
	}
	index := map[string]Existing{}
	ids := map[string]bool{}
	for _, x := range existing {
		if x.ID == "" || len(x.ID) > 128 || strings.ContainsAny(x.ID, "\x00\r\n") || x.Revision < 1 || !slugRE.MatchString(x.Slug) || len(x.Slug) > 160 {
			return p, fmt.Errorf("invalid existing snapshot")
		}
		if _, ok := index[x.Slug]; ok || ids[x.ID] {
			return p, fmt.Errorf("ambiguous existing snapshot")
		}
		index[x.Slug] = x
		ids[x.ID] = true
	}
	docs := append([]Document(nil), documents...)
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].SourcePath == docs[j].SourcePath {
			return HashDocument(docs[i]) < HashDocument(docs[j])
		}
		return docs[i].SourcePath < docs[j].SourcePath
	})
	paths := map[string]int{}
	slugs := map[string]int{}
	total := 0
	for _, d := range docs {
		paths[d.SourcePath]++
		slugs[d.Slug]++
		b, _ := json.Marshal(d)
		total += len(b)
	}
	if total > MaxBatchBytes {
		return p, fmt.Errorf("documents exceed 64 MiB plan limit")
	}
	for _, d := range docs {
		// JSON clone freezes caller-owned slices/maps and canonicalizes empty fields.
		b, e := json.Marshal(d)
		if e != nil {
			return p, e
		}
		var frozen Document
		if e = json.Unmarshal(b, &frozen); e != nil {
			return p, e
		}
		d = frozen
		if d.Tags == nil {
			d.Tags = []string{}
		}
		if d.Warnings == nil {
			d.Warnings = []string{}
		}
		entry := Entry{Document: d, Action: "create"}
		block := func(code, message string) {
			entry.Action = "blocked"
			p.Diagnostics = append(p.Diagnostics, Diagnostic{d.SourcePath, code, message})
		}
		if e = ValidateDocument(d); e != nil {
			block("invalid_document", e.Error())
		} else if paths[d.SourcePath] > 1 {
			block("duplicate_path", "Multiple documents have the same source path")
		} else if slugs[d.Slug] > 1 {
			block("duplicate_slug", "Multiple source documents have the same slug")
		} else if x, ok := index[d.Slug]; ok {
			switch policy {
			case "error":
				block("existing_slug", "Existing article retained; select skip or explicit update before planning")
			case "skip":
				entry.Action = "skip"
				entry.TargetID = x.ID
				entry.ExpectedRevision = x.Revision
			case "update":
				entry.Action = "update"
				entry.TargetID = x.ID
				entry.ExpectedRevision = x.Revision
			}
		}
		entry.Document.ContentHash = HashDocument(entry.Document)
		if entry.Action == "create" || entry.Action == "update" {
			entry.IdempotencyKey = requestKey(entry)
		}
		p.Entries = append(p.Entries, entry)
	}
	sealPlan(&p)
	return p, nil
}

func BuildPlan(documents []Document, existing []Existing, options Options) (ImportPlan, error) {
	return Plan(documents, existing, options)
}

// WithDiagnostics freezes adapter-level validation failures into the plan itself.
// A preview with source parse errors or instance-specific conflicts must never
// become an applicable subset simply because its caller ignores preview fields.
// The returned plan remains deterministic but ValidatePlan rejects diagnostics.
func WithDiagnostics(p ImportPlan, diagnostics []Diagnostic) ImportPlan {
	p.Diagnostics = append(append([]Diagnostic{}, p.Diagnostics...), diagnostics...)
	sealPlan(&p)
	return p
}

func sealPlan(p *ImportPlan) {
	sort.Slice(p.Diagnostics, func(i, j int) bool {
		a, b := p.Diagnostics[i], p.Diagnostics[j]
		if a.SourcePath != b.SourcePath {
			return a.SourcePath < b.SourcePath
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
	p.ID = ""
	b, _ := json.Marshal(p)
	p.ID = digest(b)
}

type postRequest struct {
	Title            string   `json:"title"`
	Slug             string   `json:"slug"`
	Markdown         string   `json:"markdown"`
	Excerpt          string   `json:"excerpt"`
	Tags             []string `json:"tags"`
	Category         string   `json:"category"`
	Cover            string   `json:"cover"`
	Featured         bool     `json:"featured"`
	ID               string   `json:"id,omitempty"`
	ExpectedRevision int      `json:"expected_revision,omitempty"`
	IdempotencyKey   string   `json:"idempotency_key,omitempty"`
}

func requestFor(e Entry, withKey bool) postRequest {
	d := e.Document
	r := postRequest{Title: d.Title, Slug: d.Slug, Markdown: d.Markdown, Excerpt: d.Excerpt, Tags: d.Tags, Category: d.Category, Cover: d.Cover, Featured: d.Featured}
	if e.Action == "update" {
		r.ID = e.TargetID
		r.ExpectedRevision = e.ExpectedRevision
	}
	if withKey {
		r.IdempotencyKey = e.IdempotencyKey
	}
	return r
}
func requestKey(e Entry) string {
	b, _ := json.Marshal(requestFor(e, false))
	return "migration-" + digest(append([]byte(e.Action+":"), b...))
}

// ValidatePlan detects mutations, arbitrary operations and malformed CAS requests before any call.
// This checksum is not an authorization signature: the adapter must bind confirmation to a plan ID.
func ValidatePlan(p ImportPlan) error {
	if p.Format != "folio-migration-plan" || p.Version != Version || len(p.Entries) > MaxDocuments {
		return fmt.Errorf("unsupported plan format or size")
	}
	copy := p
	sealPlan(&copy)
	if p.ID == "" || p.ID != copy.ID {
		return fmt.Errorf("plan checksum mismatch")
	}
	if len(p.Diagnostics) > 0 {
		return fmt.Errorf("plan contains diagnostics; fix inputs and re-plan")
	}
	paths := map[string]bool{}
	slugs := map[string]bool{}
	last := ""
	total := 0
	for _, e := range p.Entries {
		if err := ValidateDocument(e.Document); err != nil {
			return err
		}
		if e.Document.ContentHash == "" {
			return fmt.Errorf("missing document checksum")
		}
		if paths[e.Document.SourcePath] || slugs[e.Document.Slug] || e.Document.SourcePath < last {
			return fmt.Errorf("plan paths/slugs must be unique and sorted")
		}
		paths[e.Document.SourcePath] = true
		slugs[e.Document.Slug] = true
		last = e.Document.SourcePath
		b, _ := json.Marshal(e)
		total += len(b)
		if total > MaxBatchBytes {
			return fmt.Errorf("plan exceeds 64 MiB")
		}
		switch e.Action {
		case "create":
			if e.TargetID != "" || e.ExpectedRevision != 0 {
				return fmt.Errorf("create cannot target existing records")
			}
		case "update", "skip":
			if e.TargetID == "" || len(e.TargetID) > 128 || e.ExpectedRevision < 1 {
				return fmt.Errorf("existing target requires ID and revision")
			}
		default:
			return fmt.Errorf("unsupported or blocked action")
		}
		if e.Action == "skip" {
			if e.IdempotencyKey != "" {
				return fmt.Errorf("skip must not carry write key")
			}
		} else if e.IdempotencyKey != requestKey(e) {
			return fmt.Errorf("idempotency key mismatch")
		}
	}
	return nil
}
