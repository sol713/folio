package core

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
)

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Migration 2 splits the transactional catalog, individual posts and binary
// media into separate rows. A post edit no longer rewrites every media asset.
func (s *Store) migrateRows(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS posts(id TEXT PRIMARY KEY, document TEXT NOT NULL)", "CREATE TABLE IF NOT EXISTS media(id TEXT PRIMARY KEY, metadata TEXT NOT NULL, bytes BLOB NOT NULL)"} {
		if _, e = tx.ExecContext(ctx, q); e != nil {
			return e
		}
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM migrations WHERE version=2").Scan(&count); e != nil {
		return e
	}
	if count == 0 {
		if e = s.persistRows(ctx, tx, State{}); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO migrations VALUES(2,datetime('now'))"); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (s *Store) loadRows(ctx context.Context, q querier, meta string) error {
	var st State
	if e := json.Unmarshal([]byte(meta), &st); e != nil {
		return e
	}
	st.Posts = map[string]*Record{}
	st.Media = map[string]Media{}
	rows, e := q.QueryContext(ctx, "SELECT id,document FROM posts")
	if e != nil {
		return e
	}
	for rows.Next() {
		var key, raw string
		if e = rows.Scan(&key, &raw); e != nil {
			rows.Close()
			return e
		}
		var r Record
		if e = json.Unmarshal([]byte(raw), &r); e != nil {
			rows.Close()
			return e
		}
		st.Posts[key] = &r
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	rows, e = q.QueryContext(ctx, "SELECT id,metadata,bytes FROM media")
	if e != nil {
		return e
	}
	for rows.Next() {
		var key, raw string
		var b []byte
		if e = rows.Scan(&key, &raw, &b); e != nil {
			rows.Close()
			return e
		}
		var m Media
		if e = json.Unmarshal([]byte(raw), &m); e != nil {
			rows.Close()
			return e
		}
		m.Data = base64.StdEncoding.EncodeToString(b)
		st.Media[key] = m
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	s.State = st
	return nil
}
func (s *Store) refresh(ctx context.Context, q querier, force bool) error {
	var meta string
	if e := q.QueryRowContext(ctx, "SELECT document FROM state WHERE id=1").Scan(&meta); e != nil {
		return e
	}
	var version struct {
		Revision int `json:"revision"`
	}
	if e := json.Unmarshal([]byte(meta), &version); e != nil {
		return e
	}
	if force || version.Revision != s.State.Revision {
		return s.loadRows(ctx, q, meta)
	}
	return nil
}
func (s *Store) persistRows(ctx context.Context, tx *sql.Tx, before State) error {
	for id, r := range s.State.Posts {
		if before.Posts[id] == r {
			continue
		}
		b, e := json.Marshal(r)
		if e != nil {
			return e
		}
		old, _ := json.Marshal(before.Posts[id])
		if string(old) == string(b) {
			continue
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO posts(id,document) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", id, string(b)); e != nil {
			return e
		}
	}
	for id := range before.Posts {
		if _, ok := s.State.Posts[id]; !ok {
			if _, e := tx.ExecContext(ctx, "DELETE FROM posts WHERE id=?", id); e != nil {
				return e
			}
		}
	}
	for id, m := range s.State.Media {
		if old, ok := before.Media[id]; ok && old == m {
			continue
		}
		b, e := base64.StdEncoding.DecodeString(m.Data)
		if e != nil {
			return e
		}
		m.Data = ""
		metadata, e := json.Marshal(m)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, "INSERT INTO media(id,metadata,bytes) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET metadata=excluded.metadata,bytes=excluded.bytes", id, string(metadata), b); e != nil {
			return e
		}
	}
	for id := range before.Media {
		if _, ok := s.State.Media[id]; !ok {
			if _, e := tx.ExecContext(ctx, "DELETE FROM media WHERE id=?", id); e != nil {
				return e
			}
		}
	}
	metadata := s.State
	metadata.Posts = nil
	metadata.Media = nil
	b, e := json.Marshal(metadata)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, "UPDATE state SET document=? WHERE id=1", string(b))
	return e
}

// Clone only mutable containers and the targeted record. Media strings and
// immutable content snapshots can be shared safely until a new revision exists.
func cloneForWrite(st State, raw json.RawMessage) State {
	out := st
	out.Posts = make(map[string]*Record, len(st.Posts))
	for k, v := range st.Posts {
		out.Posts[k] = v
	}
	out.Media = make(map[string]Media, len(st.Media))
	for k, v := range st.Media {
		out.Media[k] = v
	}
	out.Idempotency = make(map[string]Idempotent, len(st.Idempotency))
	for k, v := range st.Idempotency {
		out.Idempotency[k] = v
	}
	out.Audit = append([]Event{}, st.Audit...)
	var a struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &a)
	if r := st.Posts[a.ID]; r != nil {
		copy := *r
		copy.Revisions = append([]Post{}, r.Revisions...)
		copy.OldSlugs = append([]string{}, r.OldSlugs...)
		if r.Live != nil {
			p := *r.Live
			copy.Live = &p
		}
		out.Posts[a.ID] = &copy
	}
	sources := make(map[string]ImportSource, len(st.ImportSources))
	for key, value := range st.ImportSources {
		sources[key] = value
	}
	out.ImportSources = sources
	return cloneWorkflows(out)
}

// A conservative logical-size bound includes all raw content, rendered content,
// revision metadata, asset backup base64, retry responses and serialization overhead.
// It avoids serializing the entire media library on every small draft edit.
func logicalSize(st State) int {
	n := 4096 + jsonStringSize(st.Settings.Title) + jsonStringSize(st.Settings.Description) + jsonStringSize(st.Settings.Author) + jsonStringSize(st.Settings.BaseURL)
	postSize := func(p Post) int {
		x := 1024 + jsonStringSize(p.Title) + jsonStringSize(p.Slug) + jsonStringSize(p.Markdown) + jsonStringSize(p.HTML) + jsonStringSize(p.Excerpt) + jsonStringSize(p.Category) + jsonStringSize(p.Cover)
		for _, t := range p.Tags {
			x += jsonStringSize(t) + 8
		}
		return x
	}
	for _, r := range st.Posts {
		n += postSize(r.Draft)
		if r.Live != nil {
			n += postSize(*r.Live)
		}
		for _, p := range r.Revisions {
			n += postSize(p)
		}
		for _, old := range r.OldSlugs {
			n += len(old) + 8
		}
	}
	for _, m := range st.Media {
		n += len(m.Data) + jsonStringSize(m.Name) + 1024
	}
	for k, v := range st.Idempotency {
		n += jsonStringSize(k) + len(v.Result) + 256
	}
	for _, e := range st.Audit {
		n += 256 + jsonStringSize(e.ID) + jsonStringSize(e.Operation) + jsonStringSize(e.Target) + jsonStringSize(e.Actor) + jsonStringSize(e.At)
	}
	sources, _ := json.Marshal(st.ImportSources)
	return n + workflowSize(st) + len(sources)
}

func jsonStringSize(s string) int {
	n := 2
	for _, r := range s {
		switch {
		case r < 32 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029':
			n += 6
		case r == '"' || r == '\\':
			n += 2
		case r < 128:
			n++
		case r < 2048:
			n += 2
		case r < 65536:
			n += 3
		default:
			n += 4
		}
	}
	return n
}
