// Package migration implements bounded, deterministic Markdown migration.
// It contains no database, network, publication, or credential implementation.
package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	Version          = 1
	MaxMarkdownBytes = 1 << 20
	MaxFileBytes     = MaxMarkdownBytes + (64 << 10)
	MaxDocuments     = 1000
	MaxBatchBytes    = 64 << 20
)

type Document struct {
	SourcePath  string   `json:"source_path"`
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Markdown    string   `json:"markdown"`
	Excerpt     string   `json:"excerpt"`
	Tags        []string `json:"tags"`
	Category    string   `json:"category"`
	Cover       string   `json:"cover"`
	Featured    bool     `json:"featured"`
	Date        string   `json:"date,omitempty"`
	PublishDate string   `json:"publish_date,omitempty"`
	Draft       bool     `json:"draft"`
	// Extra preserves unsupported top-level metadata values as source literals.
	Extra       map[string]string `json:"extra,omitempty"`
	Warnings    []string          `json:"warnings"`
	ContentHash string            `json:"content_hash"`
}

type Existing struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Revision int    `json:"revision"`
}

type Options struct {
	// Conflict is error (default), skip, or update. Update is explicit draft replacement.
	Conflict string `json:"conflict"`
}

type Diagnostic struct {
	SourcePath string `json:"source_path"`
	Code       string `json:"code"`
	Message    string `json:"message"`
}

type Entry struct {
	Document         Document `json:"document"`
	Action           string   `json:"action"` // create, update, skip, or blocked
	TargetID         string   `json:"target_id,omitempty"`
	ExpectedRevision int      `json:"expected_revision,omitempty"`
	IdempotencyKey   string   `json:"idempotency_key,omitempty"`
}

// ImportPlan is named separately because Go cannot give a type and function the same name.
type ImportPlan struct {
	Format      string       `json:"format"`
	Version     int          `json:"version"`
	ID          string       `json:"id"`
	Entries     []Entry      `json:"entries"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Caller returns the unwrapped operation data object, e.g. {"post":{...}}.
// The application's adapter must enforce its normal authentication and role checks.
type Caller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

type CallerFunc func(context.Context, string, json.RawMessage) (json.RawMessage, error)

func (f CallerFunc) Call(ctx context.Context, op string, args json.RawMessage) (json.RawMessage, error) {
	return f(ctx, op, args)
}

type ApplyOptions struct{ ContinueOnError bool }
type Outcome struct {
	SourcePath     string `json:"source_path"`
	Action         string `json:"action"`
	Status         string `json:"status"` // applied, skipped, failed, or pending
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	PostID         string `json:"post_id,omitempty"`
	Revision       int    `json:"revision,omitempty"`
	Error          string `json:"error,omitempty"`
}
type Report struct {
	PlanID   string    `json:"plan_id"`
	Complete bool      `json:"complete"`
	Outcomes []Outcome `json:"outcomes"`
}

var slugRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// HashDocument excludes presentation diagnostics and path but includes retained metadata.
func HashDocument(d Document) string {
	d.SourcePath = ""
	d.Warnings = nil
	d.ContentHash = ""
	if d.Tags == nil {
		d.Tags = []string{}
	}
	if len(d.Extra) == 0 {
		d.Extra = nil
	}
	b, _ := json.Marshal(d)
	return digest(b)
}

func ValidateDocument(d Document) error {
	if err := validPath(d.SourcePath); err != nil {
		return err
	}
	for _, s := range []string{d.Title, d.Slug, d.Markdown, d.Excerpt, d.Category, d.Cover, d.Date, d.PublishDate} {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return fmt.Errorf("content must be UTF-8 without NUL")
		}
	}
	if strings.TrimSpace(d.Title) == "" || utf8.RuneCountInString(d.Title) > 200 {
		return fmt.Errorf("title must contain 1–200 characters")
	}
	if len(d.Slug) > 160 || !slugRE.MatchString(d.Slug) {
		return fmt.Errorf("slug must contain lowercase ASCII words separated by hyphens, maximum 160 bytes")
	}
	if len(d.Markdown) > MaxMarkdownBytes {
		return fmt.Errorf("Markdown exceeds 1 MiB")
	}
	if len(d.Excerpt) > 2000 || len(d.Category) > 100 || len(d.Tags) > 20 {
		return fmt.Errorf("excerpt, category, or tag limit exceeded")
	}
	for _, t := range d.Tags {
		if !utf8.ValidString(t) || strings.ContainsRune(t, 0) || strings.TrimSpace(t) == "" || len(t) > 80 {
			return fmt.Errorf("tags must contain 1–80 UTF-8 bytes")
		}
	}
	if d.Cover != "" && (!strings.HasPrefix(d.Cover, "/media/") || strings.Contains(d.Cover, "..") || strings.ContainsAny(d.Cover, "\\\r\n")) {
		return fmt.Errorf("cover must be an existing local /media/ reference")
	}
	for _, s := range []string{d.Date, d.PublishDate} {
		if s != "" {
			v, e := normalizeDate(s)
			if e != nil || v != s {
				return fmt.Errorf("dates must be normalized RFC3339 UTC")
			}
		}
	}
	if len(d.Extra) > 100 {
		return fmt.Errorf("too many unsupported metadata fields")
	}
	for k, v := range d.Extra {
		if !utf8.ValidString(k+v) || len(k) > 128 || len(v) > 64<<10 || strings.ContainsRune(k+v, 0) {
			return fmt.Errorf("invalid unsupported metadata")
		}
	}
	if d.ContentHash != "" && d.ContentHash != HashDocument(d) {
		return fmt.Errorf("document content_hash mismatch")
	}
	return nil
}
