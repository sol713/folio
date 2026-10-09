package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	_ "modernc.org/sqlite"
)

type Store struct {
	db         *sql.DB
	mu         sync.Mutex
	State      State
	path       string
	instanceID string
}

func Open(dir string) (*Store, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	absolute, e := filepath.Abs(filepath.Join(dir, "folio.db"))
	if e != nil {
		return nil, e
	}
	dsn := (&url.URL{Scheme: "file", Path: absolute, RawQuery: "_txlock=immediate"}).String()
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON", "CREATE TABLE IF NOT EXISTS migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)", "CREATE TABLE IF NOT EXISTS state(id INTEGER PRIMARY KEY CHECK(id=1), document TEXT NOT NULL)", "INSERT OR IGNORE INTO migrations VALUES(1,datetime('now'))"} {
		if _, e = db.Exec(q); e != nil {
			db.Close()
			return nil, e
		}
	}
	s := &Store{db: db, path: dir}
	var raw string
	e = db.QueryRow("SELECT document FROM state WHERE id=1").Scan(&raw)
	if e == sql.ErrNoRows {
		s.State = freshState()
		b, _ := json.Marshal(s.State)
		_, e = db.Exec("INSERT INTO state VALUES(1,?)", string(b))
	} else if e == nil {
		e = json.Unmarshal([]byte(raw), &s.State)
	}
	if e != nil {
		db.Close()
		return nil, e
	}
	if s.State.Schema != 1 && s.State.Schema != SchemaVersion {
		db.Close()
		return nil, fmt.Errorf("unsupported schema")
	}
	if e = s.migrateRows(context.Background()); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.refresh(context.Background(), db, true); e != nil {
		db.Close()
		return nil, e
	}
	if e = s.upgradeStore(context.Background()); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func id() string              { b := make([]byte, 12); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func digest(b []byte) string  { x := sha256.Sum256(b); return hex.EncodeToString(x[:]) }

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))
var policy = bluemonday.UGCPolicy()

func Render(markdown string) string {
	var b bytes.Buffer
	_ = md.Convert([]byte(markdown), &b)
	return policy.Sanitize(b.String())
}

var slugRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func ValidatePost(p Post) error {
	if len(p.ID) > 128 || len(p.CreatedAt) > 64 || len(p.UpdatedAt) > 64 || len(p.PublishedAt) > 64 {
		return Err("validation", "Invalid post metadata lengths")
	}
	if strings.TrimSpace(p.Title) == "" || utf8.RuneCountInString(p.Title) > 200 {
		return Err("validation", "Title must contain 1–200 characters")
	}
	if len(p.Slug) > 160 || !slugRE.MatchString(p.Slug) {
		return Err("validation", "Slug must use lowercase letters, numbers, and single hyphens")
	}
	if len(p.Markdown) > 1024*1024 {
		return Err("validation", "Markdown must be at most 1 MiB")
	}
	if len(p.Excerpt) > 2000 || len(p.Category) > 100 || len(p.Tags) > 20 {
		return Err("validation", "Excerpt, category, or tag limit exceeded")
	}
	for _, t := range p.Tags {
		if strings.TrimSpace(t) == "" || len(t) > 80 {
			return Err("validation", "Tags must contain 1–80 bytes")
		}
	}
	if p.Cover != "" && !strings.HasPrefix(p.Cover, "/media/") {
		return Err("validation", "Cover must be a local /media/ asset")
	}
	return nil
}
func validateSettings(v Settings) error {
	u, e := url.Parse(v.BaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return Err("validation", "base_url must be an absolute HTTP(S) origin without credentials, subpath, query, or fragment")
	}
	if len(v.Title) == 0 || len(v.Title) > 150 || len(v.Description) > 1000 || len(v.Author) > 100 {
		return Err("validation", "Invalid site settings lengths")
	}
	return nil
}
func (s *Store) Public() (Settings, []Post) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh(context.Background(), s.db, false)
	posts := []Post{}
	for _, r := range s.State.Posts {
		if !r.Deleted && r.Live != nil {
			p := *r.Live
			p.Tags = append([]string{}, p.Tags...)
			posts = append(posts, p)
		}
	}
	sort.Slice(posts, func(i, j int) bool {
		if posts[i].PublishedAt == posts[j].PublishedAt {
			return posts[i].ID < posts[j].ID
		}
		return posts[i].PublishedAt > posts[j].PublishedAt
	})
	return s.State.Settings, posts
}
func (s *Store) Media(id string) (Media, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh(context.Background(), s.db, false)
	m, ok := s.State.Media[id]
	return m, ok
}
func decode(raw json.RawMessage, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return Err("validation", "Invalid arguments: %s", e)
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return Err("validation", "Only one JSON value allowed")
	}
	return nil
}
func (s *Store) Call(ctx context.Context, op string, raw json.RawMessage, actor string) (any, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	spec, ok := Specs[op]
	if !ok {
		return nil, Err("not_found", "Unknown operation %q", op)
	}
	if op == "system.capabilities" {
		return Capabilities(), nil
	}
	if strings.HasPrefix(op, "migration.") {
		return s.callMigration(ctx, op, raw, actor)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	// Reload within a transaction for atomic CAS across concurrent process clients.
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = s.refresh(ctx, tx, false); e != nil {
		return nil, e
	}
	before := s.State
	if !spec.ReadOnly {
		s.State = cloneForWrite(s.State, raw)
	}
	var envelope struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	_ = json.Unmarshal(raw, &envelope)
	key := op + ":" + actor + ":" + envelope.IdempotencyKey
	fingerprintBytes := append([]byte{}, raw...)
	source, hasSource := ctx.Value(importSourceKey{}).(ImportSource)
	if hasSource {
		if e := validateImportSource(source); e != nil {
			s.State = before
			return nil, e
		}
		b, _ := json.Marshal(source)
		fingerprintBytes = append(fingerprintBytes, b...)
	}
	fingerprint := digest(fingerprintBytes)
	if !spec.ReadOnly && envelope.IdempotencyKey != "" {
		if len(envelope.IdempotencyKey) > 128 {
			return nil, Err("validation", "Idempotency key too long")
		}
		if old, ok := s.State.Idempotency[key]; ok {
			if old.Fingerprint != fingerprint {
				return nil, Err("conflict", "Idempotency key reused with different arguments")
			}
			var result any
			_ = json.Unmarshal(old.Result, &result)
			return result, nil
		}
	}
	result, e := s.execute(op, raw, actor)
	if e != nil {
		if !spec.ReadOnly {
			s.State = before
		}
		return nil, e
	}
	if spec.ReadOnly {
		return result, nil
	}
	if hasSource {
		if resultMap, ok := result.(map[string]any); ok {
			if p, ok := resultMap["post"].(Post); ok {
				if s.State.ImportSources == nil {
					s.State.ImportSources = map[string]ImportSource{}
				}
				s.State.ImportSources[p.ID] = source
			}
		}
	}
	s.State.Revision++
	if envelope.IdempotencyKey != "" {
		b, _ := json.Marshal(result)
		s.State.Idempotency[key] = Idempotent{fingerprint, b}
	}
	size := logicalSize(s.State)
	if size > 64*1024*1024 {
		s.State = before
		return nil, Err("validation", "Instance exceeds v1 64 MiB logical limit; export and compact content")
	}
	if e = s.persistRows(ctx, tx, before); e != nil {
		s.State = before
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		s.State = before
		return nil, e
	}
	return result, nil
}
func (s *Store) audit(op, target, actor string, rev int) {
	s.State.Audit = append(s.State.Audit, Event{id(), op, target, actor, now(), rev})
	if len(s.State.Audit) > 10000 {
		s.State.Audit = s.State.Audit[len(s.State.Audit)-10000:]
	}
}
func (s *Store) record(id string) (*Record, error) {
	r, ok := s.State.Posts[id]
	if !ok || r.Deleted {
		return nil, Err("not_found", "Post not found")
	}
	return r, nil
}
func (s *Store) unique(slug, except string) bool {
	for k, r := range s.State.Posts {
		if k != except && !r.Deleted && (r.Draft.Slug == slug || (r.Live != nil && r.Live.Slug == slug)) {
			return false
		}
	}
	return true
}
func checkRevision(r *Record, expected int) error {
	if expected != r.Draft.Revision {
		return Err("conflict", "Revision conflict: expected %d, current %d. Fetch before retrying.", expected, r.Draft.Revision)
	}
	return nil
}

type postInput struct {
	ID             string   `json:"id"`
	Expected       int      `json:"expected_revision"`
	Title          string   `json:"title"`
	Slug           string   `json:"slug"`
	Excerpt        string   `json:"excerpt"`
	Markdown       string   `json:"markdown"`
	Tags           []string `json:"tags"`
	Category       string   `json:"category"`
	Cover          string   `json:"cover"`
	Featured       bool     `json:"featured"`
	IdempotencyKey string   `json:"idempotency_key"`
}
type actionInput struct {
	ID             string `json:"id"`
	Expected       int    `json:"expected_revision"`
	Revision       int    `json:"revision"`
	Confirm        bool   `json:"confirm"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (s *Store) execute(op string, raw json.RawMessage, actor string) (any, error) {
	if strings.HasPrefix(op, "proposals.") {
		return s.executeProposal(op, raw, actor)
	}
	if strings.HasPrefix(op, "schedules.") {
		return s.executeSchedule(op, raw, actor)
	}
	switch op {
	case "system.info":
		return map[string]any{"version": Version, "instance_id": s.InstanceID(), "storage": "SQLite / local embedded media", "revision": s.State.Revision, "ai": map[string]any{"available": false, "reason": "No model provider is built in; connect your own agent through MCP."}, "counts": map[string]int{"posts": len(s.State.Posts), "media": len(s.State.Media)}}, nil
	case "posts.list":
		var a struct {
			Status string `json:"status"`
			Query  string `json:"query"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		posts := []Post{}
		for _, r := range s.State.Posts {
			if r.Deleted && a.Status != "trash" {
				continue
			}
			p := r.Draft
			if a.Status != "" && a.Status != p.Status {
				continue
			}
			if a.Query != "" && !strings.Contains(strings.ToLower(p.Title+" "+p.Markdown), strings.ToLower(a.Query)) {
				continue
			}
			posts = append(posts, p)
		}
		sort.Slice(posts, func(i, j int) bool {
			if posts[i].UpdatedAt == posts[j].UpdatedAt {
				return posts[i].ID < posts[j].ID
			}
			return posts[i].UpdatedAt > posts[j].UpdatedAt
		})
		return map[string]any{"posts": posts}, nil
	case "posts.get", "posts.preview":
		var a actionInput
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		r, e := s.record(a.ID)
		if e != nil {
			return nil, e
		}
		p := r.Draft
		if a.Revision > 0 {
			found := false
			for _, v := range r.Revisions {
				if v.Revision == a.Revision {
					p = v
					found = true
					break
				}
			}
			if !found {
				return nil, Err("not_found", "Revision not found")
			}
		}
		return map[string]any{"post": p, "html": Render(p.Markdown), "revisions": r.Revisions, "live": r.Live}, nil
	case "posts.create", "posts.update":
		var a postInput
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		p := Post{ID: a.ID, Title: strings.TrimSpace(a.Title), Slug: a.Slug, Excerpt: a.Excerpt, Markdown: a.Markdown, Tags: a.Tags, Category: a.Category, Cover: a.Cover, Featured: a.Featured, Status: "draft", Revision: 1, CreatedAt: now(), UpdatedAt: now()}
		if p.Tags == nil {
			p.Tags = []string{}
		}
		if e := ValidatePost(p); e != nil {
			return nil, e
		}
		if !s.unique(p.Slug, p.ID) {
			return nil, Err("conflict", "Slug already exists")
		}
		var r *Record
		if op == "posts.create" {
			if a.ID != "" || a.Expected != 0 {
				return nil, Err("validation", "Create does not accept id or expected_revision")
			}
			p.ID = id()
			r = &Record{}
		} else {
			var e error
			r, e = s.record(a.ID)
			if e != nil {
				return nil, e
			}
			if e = checkRevision(r, a.Expected); e != nil {
				return nil, e
			}
			p.Revision = r.Draft.Revision + 1
			p.CreatedAt = r.Draft.CreatedAt
			p.PublishedRevision = r.Draft.PublishedRevision
			p.PublishedAt = r.Draft.PublishedAt
			if r.Live != nil {
				p.Status = "changed"
			}
		}
		p.HTML = Render(p.Markdown)
		r.Draft = p
		r.Revisions = append(r.Revisions, p)
		s.State.Posts[p.ID] = r
		s.audit(op, p.ID, actor, p.Revision)
		return map[string]any{"post": p}, nil
	case "posts.recover":
		var a actionInput
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		r, ok := s.State.Posts[a.ID]
		if !ok || !r.Deleted {
			return nil, Err("not_found", "Trashed post not found")
		}
		if e := checkRevision(r, a.Expected); e != nil {
			return nil, e
		}
		if !a.Confirm {
			return nil, Err("confirmation_required", "Set confirm:true")
		}
		if !s.unique(r.Draft.Slug, a.ID) {
			return nil, Err("conflict", "Slug is now in use")
		}
		r.Deleted = false
		r.Draft.Status = "draft"
		r.Draft.PublishedRevision = 0
		r.Draft.Revision++
		r.Draft.UpdatedAt = now()
		r.Revisions = append(r.Revisions, r.Draft)
		s.audit(op, a.ID, actor, r.Draft.Revision)
		return map[string]any{"post": r.Draft}, nil
	case "posts.publish", "posts.unpublish", "posts.delete", "posts.restore":
		var a actionInput
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		r, e := s.record(a.ID)
		if e != nil {
			return nil, e
		}
		if e = checkRevision(r, a.Expected); e != nil {
			return nil, e
		}
		if op != "posts.restore" && !a.Confirm {
			return nil, Err("confirmation_required", "Set confirm:true for this action")
		}
		switch op {
		case "posts.publish":
			if strings.TrimSpace(r.Draft.Markdown) == "" {
				return nil, Err("validation", "Cannot publish an empty post")
			}
			if r.Live != nil && r.Live.Slug != r.Draft.Slug {
				r.OldSlugs = append(r.OldSlugs, r.Live.Slug)
			}
			r.Draft.Status = "published"
			r.Draft.PublishedRevision = r.Draft.Revision
			r.Draft.PublishedAt = now()
			p := r.Draft
			r.Live = &p
		case "posts.unpublish":
			r.Live = nil
			r.Draft.Status = "draft"
			r.Draft.PublishedRevision = 0
		case "posts.delete":
			r.Deleted = true
			r.Live = nil
			r.Draft.Status = "trash"
		case "posts.restore":
			found := false
			for _, p := range r.Revisions {
				if p.Revision == a.Revision {
					if !s.unique(p.Slug, p.ID) {
						return nil, Err("conflict", "Historical slug now in use")
					}
					p.Revision = r.Draft.Revision + 1
					p.UpdatedAt = now()
					p.PublishedRevision = r.Draft.PublishedRevision
					p.Status = "draft"
					if r.Live != nil {
						p.Status = "changed"
					}
					r.Draft = p
					r.Revisions = append(r.Revisions, p)
					found = true
					break
				}
			}
			if !found {
				return nil, Err("not_found", "Revision not found")
			}
		}
		if op == "posts.publish" || op == "posts.unpublish" || op == "posts.delete" {
			s.cancelPostSchedules(a.ID, op, actor)
		}
		s.audit(op, a.ID, actor, r.Draft.Revision)
		return map[string]any{"post": r.Draft}, nil
	case "settings.get":
		return map[string]any{"settings": s.State.Settings, "revision": s.State.SettingsRevision}, nil
	case "settings.update":
		var a struct {
			Expected       int      `json:"expected_revision"`
			Settings       Settings `json:"settings"`
			IdempotencyKey string   `json:"idempotency_key"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if a.Expected != s.State.SettingsRevision {
			return nil, Err("conflict", "Settings revision conflict")
		}
		if e := validateSettings(a.Settings); e != nil {
			return nil, e
		}
		a.Settings.BaseURL = strings.TrimRight(a.Settings.BaseURL, "/")
		s.State.Settings = a.Settings
		s.State.SettingsRevision++
		s.audit(op, "site", actor, s.State.SettingsRevision)
		return map[string]any{"settings": s.State.Settings, "revision": s.State.SettingsRevision}, nil
	case "media.list":
		list := []Media{}
		for _, m := range s.State.Media {
			m.Data = ""
			list = append(list, m)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].CreatedAt == list[j].CreatedAt {
				return list[i].ID < list[j].ID
			}
			return list[i].CreatedAt > list[j].CreatedAt
		})
		return map[string]any{"media": list}, nil
	case "media.upload":
		var a struct {
			Name           string `json:"name"`
			Base64         string `json:"base64"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if a.Name == "" || len(a.Name) > 200 || strings.ContainsAny(a.Name, "/\\") || a.Name == ".." {
			return nil, Err("validation", "Use a filename without directory separators")
		}
		b, e := base64.StdEncoding.DecodeString(a.Base64)
		if e != nil || len(b) == 0 || len(b) > 5*1024*1024 {
			return nil, Err("validation", "Expected base64 image, maximum 5 MiB")
		}
		mime := http.DetectContentType(b)
		ext := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/gif": "gif", "image/webp": "webp"}[mime]
		if ext == "" {
			return nil, Err("validation", "Only PNG, JPEG, GIF, and WebP images are allowed")
		}
		key := digest(b) + "." + ext
		m := Media{key, a.Name, "/media/" + key, mime, len(b), now(), base64.StdEncoding.EncodeToString(b), digest(b)}
		s.State.Media[key] = m
		s.audit(op, key, actor, 0)
		m.Data = ""
		return map[string]any{"media": m}, nil
	case "audit.list":
		events := append([]Event{}, s.State.Audit...)
		sort.Slice(events, func(i, j int) bool {
			if events[i].At == events[j].At {
				return events[i].ID < events[j].ID
			}
			return events[i].At > events[j].At
		})
		return map[string]any{"events": events}, nil
	case "backup.export":
		state := s.State
		state.Idempotency = map[string]Idempotent{}
		b, _ := json.Marshal(state)
		return Backup{"folio-backup", SchemaVersion, now(), digest(b), b}, nil
	case "backup.restore":
		var a struct {
			Backup         Backup `json:"backup"`
			Confirm        bool   `json:"confirm"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if !a.Confirm {
			return nil, Err("confirmation_required", "Set confirm:true")
		}
		if len(s.State.Posts) > 0 || len(s.State.Media) > 0 || len(s.State.Proposals) > 0 || len(s.State.Schedules) > 0 {
			return nil, Err("conflict", "Restore requires an empty instance; initialize a separate data directory")
		}
		st, e := canonicalBackupState(a.Backup)
		if e != nil {
			return nil, e
		}
		if e := ValidateState(st); e != nil {
			return nil, e
		}
		sourceSchema := st.Schema
		st.Schema = SchemaVersion
		st.Idempotency = map[string]Idempotent{}
		s.State = st
		s.audit(op, "instance", actor, st.Revision)
		return map[string]any{"restored": true, "posts": len(st.Posts), "media": len(st.Media), "source_schema": sourceSchema, "schema": SchemaVersion, "schema_upgraded": sourceSchema != SchemaVersion}, nil
	}
	return nil, Err("not_found", "Unknown operation")
}
func ValidateState(st State) error {
	if (st.Schema != 1 && st.Schema != SchemaVersion) || st.Posts == nil || st.Media == nil || st.SettingsRevision < 1 || st.Revision < 1 {
		return Err("validation", "Unsupported backup state")
	}
	if st.Schema == 1 && (len(st.Proposals) > 0 || len(st.Schedules) > 0 || len(st.ImportSources) > 0) {
		return Err("validation", "Legacy backup cannot contain workflow records")
	}
	if e := validateSettings(st.Settings); e != nil {
		return e
	}
	if len(st.Audit) > 10000 {
		return Err("validation", "Backup audit limit exceeded")
	}
	for _, event := range st.Audit {
		if len(event.ID) > 128 || len(event.Operation) > 128 || len(event.Target) > 256 || len(event.Actor) > 128 || len(event.At) > 64 {
			return Err("validation", "Invalid audit event")
		}
	}
	activeSlugs := map[string]string{}
	for key, r := range st.Posts {
		if r == nil {
			return Err("validation", "Invalid null post")
		}
		for _, old := range r.OldSlugs {
			if len(old) > 160 || !slugRE.MatchString(old) {
				return Err("validation", "Invalid historical slug")
			}
		}
		if r.Draft.Revision < 1 {
			return Err("validation", "Invalid post revision")
		}
		if key != r.Draft.ID {
			return Err("validation", "Post ID mismatch")
		}
		if e := ValidatePost(r.Draft); e != nil {
			return e
		}
		if !r.Deleted {
			if owner, ok := activeSlugs[r.Draft.Slug]; ok && owner != key {
				return Err("validation", "Duplicate active slug")
			}
			activeSlugs[r.Draft.Slug] = key
		}
		r.Draft.HTML = Render(r.Draft.Markdown)
		if r.Live != nil {
			if r.Live.ID != key || r.Live.Revision < 1 || r.Live.Revision > r.Draft.Revision || r.Deleted {
				return Err("validation", "Invalid published revision")
			}
			if owner, ok := activeSlugs[r.Live.Slug]; ok && owner != key {
				return Err("validation", "Duplicate published slug")
			}
			activeSlugs[r.Live.Slug] = key
			if e := ValidatePost(*r.Live); e != nil {
				return e
			}
			r.Live.HTML = Render(r.Live.Markdown)
		}
		seenRevisions := map[int]bool{}
		for i, p := range r.Revisions {
			if p.ID != key || p.Revision < 1 || p.Revision > r.Draft.Revision || seenRevisions[p.Revision] {
				return Err("validation", "Invalid historical revision")
			}
			seenRevisions[p.Revision] = true
			if e := ValidatePost(p); e != nil {
				return e
			}
			r.Revisions[i].HTML = Render(p.Markdown)
		}
	}
	for key, m := range st.Media {
		b, e := base64.StdEncoding.DecodeString(m.Data)
		if e != nil || len(b) == 0 || len(b) > 5*1024*1024 || len(m.Name) > 200 || strings.ContainsAny(m.Name, "/\\") || digest(b) != m.SHA256 || m.Size != len(b) || key != m.ID || !strings.HasPrefix(key, m.SHA256+".") || m.URL != "/media/"+key || strings.ContainsAny(key, "/\\") {
			return Err("validation", "Invalid backup media")
		}
		if http.DetectContentType(b) != m.MIME || map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}[m.MIME] == "" || key != m.SHA256+"."+map[string]string{"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp"}[m.MIME] {
			return Err("validation", "Invalid media type")
		}
	}
	if e := validateMigrationSources(st); e != nil {
		return e
	}
	return validateWorkflows(st)
}

func (s *Store) Redirect(slug string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh(context.Background(), s.db, false)
	for _, r := range s.State.Posts {
		if r.Deleted || r.Live == nil {
			continue
		}
		for _, old := range r.OldSlugs {
			if old == slug {
				return r.Live.Slug
			}
		}
	}
	return ""
}

func (s *Store) Origin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.refresh(context.Background(), s.db, false)
	return s.State.Settings.BaseURL
}
