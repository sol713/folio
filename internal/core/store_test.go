package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func call(t *testing.T, s *Store, op string, args any) any {
	t.Helper()
	b, e := json.Marshal(args)
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.Call(context.Background(), op, b, "test")
	if e != nil {
		t.Fatalf("%s: %v", op, e)
	}
	return v
}
func callError(t *testing.T, s *Store, op string, args any, code string) {
	t.Helper()
	b, _ := json.Marshal(args)
	_, e := s.Call(context.Background(), op, b, "test")
	var ce *Error
	if !errors.As(e, &ce) || ce.Code != code {
		t.Fatalf("%s: expected %s, got %v", op, code, e)
	}
}
func getPost(t *testing.T, v any) Post {
	t.Helper()
	b, _ := json.Marshal(v)
	var result struct {
		Post Post `json:"post"`
	}
	if e := json.Unmarshal(b, &result); e != nil {
		t.Fatal(e)
	}
	return result.Post
}
func create(t *testing.T, s *Store, slug string) Post {
	return getPost(t, call(t, s, "posts.create", map[string]any{"title": "Original title", "slug": slug, "markdown": "Original public body", "excerpt": "Original excerpt", "tags": []string{"original"}}))
}
func action(p Post, confirm bool) map[string]any {
	return map[string]any{"id": p.ID, "expected_revision": p.Revision, "confirm": confirm}
}
func edited(p Post) map[string]any {
	return map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": "Private changed title", "slug": "private-new-slug", "markdown": "PRIVATE_SECRET_137 body", "excerpt": "Private secret excerpt", "tags": []string{"private"}}
}

func TestDraftLifecycleAndImmutablePublication(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "first-post")
	if p.Status != "draft" || p.Revision != 1 {
		t.Fatalf("bad initial draft: %+v", p)
	}
	_, public := s.Public()
	if len(public) != 0 {
		t.Fatal("draft became public")
	}
	callError(t, s, "posts.publish", action(p, false), "confirmation_required")
	live := getPost(t, call(t, s, "posts.publish", action(p, true)))
	if live.Status != "published" || live.PublishedRevision != 1 {
		t.Fatalf("bad publication: %+v", live)
	}
	changed := getPost(t, call(t, s, "posts.update", edited(live)))
	if changed.Status != "changed" || changed.Revision != 2 {
		t.Fatalf("bad changed draft: %+v", changed)
	}
	_, public = s.Public()
	if len(public) != 1 || public[0].Markdown != live.Markdown || public[0].Slug != live.Slug || public[0].Title != live.Title {
		t.Fatalf("private edit altered publication: %+v", public)
	}
	preview := getPost(t, call(t, s, "posts.preview", map[string]any{"id": p.ID, "revision": 1}))
	if preview.Markdown != p.Markdown || preview.Revision != 1 {
		t.Fatal("historical preview was not immutable")
	}
	callError(t, s, "posts.update", edited(live), "conflict")
	callError(t, s, "posts.publish", action(live, true), "conflict")
	restored := getPost(t, call(t, s, "posts.restore", map[string]any{"id": p.ID, "revision": 1, "expected_revision": 2}))
	if restored.Revision != 3 || restored.Markdown != p.Markdown || restored.Status != "changed" {
		t.Fatalf("restore should append private revision: %+v", restored)
	}
	_, public = s.Public()
	if public[0].Revision != 1 {
		t.Fatal("restore altered publication")
	}
	unpublished := getPost(t, call(t, s, "posts.unpublish", action(restored, true)))
	_, public = s.Public()
	if len(public) != 0 || unpublished.Status != "draft" {
		t.Fatal("unpublish left content public")
	}
	call(t, s, "posts.delete", action(unpublished, true))
	callError(t, s, "posts.get", map[string]any{"id": p.ID}, "not_found")
	if !s.State.Posts[p.ID].Deleted || len(s.State.Posts[p.ID].Revisions) != 3 {
		t.Fatal("trash should retain content revisions")
	}
}

func TestIdempotencyAndStrictInputs(t *testing.T) {
	s := testStore(t)
	args := map[string]any{"title": "One", "slug": "one", "markdown": "body", "idempotency_key": "create-one"}
	first := getPost(t, call(t, s, "posts.create", args))
	second := getPost(t, call(t, s, "posts.create", args))
	if !reflect.DeepEqual(first, second) || len(s.State.Posts) != 1 || len(s.State.Audit) != 1 {
		t.Fatal("identical retry duplicated write")
	}
	args["title"] = "Changed"
	callError(t, s, "posts.create", args, "conflict")
	callError(t, s, "posts.create", map[string]any{"title": "Two", "slug": "one", "markdown": "body"}, "conflict")
	callError(t, s, "posts.update", map[string]any{"id": first.ID, "expected_revision": 0, "title": "X", "slug": "x", "markdown": "body"}, "conflict")
	callError(t, s, "posts.create", map[string]any{"title": "X", "slug": "x", "markdown": "body", "unexpected": true}, "validation")
	for _, raw := range []string{`{"title":"X","slug":"x","markdown":"body"} {}`, `{"title":1}`, `[]`, `null`} {
		_, e := s.Call(context.Background(), "posts.create", json.RawMessage(raw), "test")
		if e == nil {
			t.Errorf("accepted malformed input %s", raw)
		}
	}
}

func TestMarkdownSanitization(t *testing.T) {
	cases := []string{`<script>alert(1)</script>`, `<img src=x onerror=alert(1)>`, `[bad](javascript:alert(1))`, `![bad](data:text/html;base64,PHNjcmlwdD4=)`, `<iframe src="https://evil.example"></iframe>`, `<a href="javascript:alert(1)">bad</a>`, `<svg onload=alert(1)>x</svg>`}
	for _, input := range cases {
		out := strings.ToLower(Render(input))
		for _, bad := range []string{"<script", "<iframe", "<svg", "onerror=", "onload=", "href=\"javascript:", "src=\"data:text/html"} {
			if strings.Contains(out, bad) {
				t.Errorf("unsafe render %q => %q", input, out)
			}
		}
	}
	out := Render("# Heading\n\n**bold** and [safe](https://example.com)\n\n- one\n- two")
	for _, good := range []string{"<h1>", "<strong>", `href="https://example.com"`, "<ul>"} {
		if !strings.Contains(out, good) {
			t.Errorf("normal Markdown lost %q: %s", good, out)
		}
	}
}

const tinyPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6cqQAAAAASUVORK5CYII="

func TestMediaSniffingAndTraversal(t *testing.T) {
	s := testStore(t)
	v := call(t, s, "media.upload", map[string]any{"name": "photo.png", "base64": tinyPNG})
	b, _ := json.Marshal(v)
	var result struct {
		Media Media `json:"media"`
	}
	json.Unmarshal(b, &result)
	m := result.Media
	if m.MIME != "image/png" || m.Data != "" || !strings.HasPrefix(m.URL, "/media/") {
		t.Fatalf("invalid media result %+v", m)
	}
	stored, ok := s.Media(m.ID)
	if !ok || stored.Data != tinyPNG {
		t.Fatal("media bytes not stored")
	}
	for _, name := range []string{"../evil.png", "nested/evil.png", `..\evil.png`, "..", ""} {
		callError(t, s, "media.upload", map[string]any{"name": name, "base64": tinyPNG}, "validation")
	}
	for _, payload := range []string{`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`, `<!doctype html><script>alert(1)</script>`, "ordinary text"} {
		callError(t, s, "media.upload", map[string]any{"name": "innocent.png", "base64": base64.StdEncoding.EncodeToString([]byte(payload))}, "validation")
	}
	callError(t, s, "media.upload", map[string]any{"name": "bad.png", "base64": "not-base64"}, "validation")
	list, _ := json.Marshal(call(t, s, "media.list", map[string]any{}))
	if strings.Contains(string(list), tinyPNG) || strings.Contains(string(list), `"base64"`) {
		t.Fatal("media listing leaks full asset bytes")
	}
}

func exported(t *testing.T, s *Store) Backup {
	t.Helper()
	v := call(t, s, "backup.export", map[string]any{})
	b, _ := json.Marshal(v)
	var result Backup
	if e := json.Unmarshal(b, &result); e != nil {
		t.Fatal(e)
	}
	return result
}
func mutatedBackup(t *testing.T, source Backup, mutate func(*State)) Backup {
	t.Helper()
	var st State
	if e := json.Unmarshal(source.State, &st); e != nil {
		t.Fatal(e)
	}
	mutate(&st)
	source.State, _ = json.Marshal(st)
	source.SHA256 = digest(source.State)
	return source
}
func restoreArgs(b Backup) map[string]any { return map[string]any{"backup": b, "confirm": true} }
func TestBackupRoundTripAndIntegrity(t *testing.T) {
	src := testStore(t)
	p := create(t, src, "backup-post")
	call(t, src, "posts.publish", action(p, true))
	call(t, src, "posts.update", edited(p))
	call(t, src, "media.upload", map[string]any{"name": "pixel.png", "base64": tinyPNG})
	backup := exported(t, src)
	var raw State
	json.Unmarshal(backup.State, &raw)
	if len(raw.Idempotency) != 0 {
		t.Fatal("backup retained retry ledger")
	}
	dst := testStore(t)
	call(t, dst, "backup.restore", restoreArgs(backup))
	_, pub := dst.Public()
	if len(pub) != 1 || pub[0].Slug != "backup-post" {
		t.Fatal("backup lost published snapshot")
	}
	if len(dst.State.Posts[p.ID].Revisions) != 2 || dst.State.Posts[p.ID].Draft.Markdown != "PRIVATE_SECRET_137 body" {
		t.Fatal("backup lost private history")
	}
	if len(dst.State.Media) != 1 {
		t.Fatal("backup lost assets")
	}
	callError(t, dst, "backup.restore", restoreArgs(backup), "conflict")
	bad := backup
	bad.SHA256 = strings.Repeat("0", 64)
	callError(t, testStore(t), "backup.restore", restoreArgs(bad), "validation")
	bad = backup
	bad.Version = 999
	callError(t, testStore(t), "backup.restore", restoreArgs(bad), "validation")
	callError(t, testStore(t), "backup.restore", map[string]any{"backup": backup, "confirm": false}, "confirmation_required")
}

func TestBackupRejectsMalformedState(t *testing.T) {
	src := testStore(t)
	p := create(t, src, "backup-post")
	call(t, src, "media.upload", map[string]any{"name": "pixel.png", "base64": tinyPNG})
	backup := exported(t, src)
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{"null post", func(st *State) { st.Posts[p.ID] = nil }},
		{"mismatched post id", func(st *State) { st.Posts[p.ID].Draft.ID = "wrong" }},
		{"bad slug", func(st *State) { st.Posts[p.ID].Draft.Slug = "../../escape" }},
		{"media path traversal", func(st *State) {
			for id, m := range st.Media {
				delete(st.Media, id)
				m.ID = "../escape"
				m.URL = "/media/../escape"
				st.Media[m.ID] = m
				break
			}
		}},
		{"bad media hash", func(st *State) {
			for id, m := range st.Media {
				m.SHA256 = strings.Repeat("0", 64)
				st.Media[id] = m
				break
			}
		}},
		{"invalid settings", func(st *State) { st.Settings.BaseURL = "javascript:alert(1)" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("malformed backup panicked instead of validation error: %v", p)
				}
			}()
			dst := testStore(t)
			callError(t, dst, "backup.restore", restoreArgs(mutatedBackup(t, backup, tt.mutate)), "validation")
			if len(dst.State.Posts) != 0 || len(dst.State.Media) != 0 {
				t.Fatal("failed restore altered instance")
			}
		})
	}
}

func TestBackupRebuildsUntrustedHTML(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "safe")
	call(t, s, "posts.publish", action(p, true))
	b := mutatedBackup(t, exported(t, s), func(st *State) {
		r := st.Posts[p.ID]
		r.Draft.HTML = "<script>evil()</script>"
		r.Live.HTML = r.Draft.HTML
		r.Revisions[0].HTML = r.Draft.HTML
	})
	dst := testStore(t)
	call(t, dst, "backup.restore", restoreArgs(b))
	r := dst.State.Posts[p.ID]
	for _, html := range []string{r.Draft.HTML, r.Live.HTML, r.Revisions[0].HTML} {
		if strings.Contains(html, "script") {
			t.Fatal("trusted unsafe backed-up HTML")
		}
	}
}

func TestDurabilityAndCrossHandleCAS(t *testing.T) {
	dir := t.TempDir()
	a, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p := create(t, a, "durable")
	call(t, b, "posts.update", edited(p))
	callError(t, a, "posts.update", edited(p), "conflict")
	c, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	read := getPost(t, call(t, c, "posts.get", map[string]any{"id": p.ID}))
	if read.Revision != 2 {
		t.Fatal("committed content did not survive reopen")
	}
	if _, e := os.Stat(filepath.Join(dir, "folio.db")); e != nil {
		t.Fatal(e)
	}
}

func TestPublicReadsObserveOtherHandleCommits(t *testing.T) {
	dir := t.TempDir()
	reader, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	writer, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	p := create(t, writer, "new-public")
	call(t, writer, "posts.publish", action(p, true))
	_, posts := reader.Public()
	if len(posts) != 1 {
		t.Fatalf("public reader missed another handle's committed publication; got %d posts", len(posts))
	}
}

func TestRecoveryFromTrashRemainsPrivate(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "recover-me")
	call(t, s, "posts.publish", action(p, true))
	call(t, s, "posts.delete", action(p, true))
	callError(t, s, "posts.recover", action(p, false), "confirmation_required")
	recovered := getPost(t, call(t, s, "posts.recover", action(p, true)))
	if recovered.Status != "draft" || recovered.Revision != 2 || recovered.PublishedRevision != 0 {
		t.Fatalf("recovered post must be new private draft: %+v", recovered)
	}
	_, public := s.Public()
	if len(public) != 0 {
		t.Fatal("trash recovery republished content")
	}
	callError(t, s, "posts.recover", action(recovered, true), "not_found")
	call(t, s, "posts.delete", action(recovered, true))
	create(t, s, "recover-me")
	callError(t, s, "posts.recover", action(recovered, true), "conflict")
}

func TestConcurrentCASHasExactlyOneWinner(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	stores := make([]*Store, n)
	for i := range stores {
		var e error
		stores[i], e = Open(dir)
		if e != nil {
			t.Fatal(e)
		}
		defer stores[i].Close()
	}
	p := create(t, stores[0], "cas-race")
	raw, _ := json.Marshal(edited(p))
	start := make(chan struct{})
	results := make(chan error, n)
	for _, s := range stores {
		go func(s *Store) {
			<-start
			_, e := s.Call(context.Background(), "posts.update", raw, "concurrent-test")
			results <- e
		}(s)
	}
	close(start)
	wins := 0
	for range n {
		e := <-results
		if e == nil {
			wins++
			continue
		}
		var ce *Error
		if !errors.As(e, &ce) || ce.Code != "conflict" {
			t.Errorf("losing CAS should be conflict, got %v", e)
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly one winning CAS, got %d", wins)
	}
	final := getPost(t, call(t, stores[0], "posts.get", map[string]any{"id": p.ID}))
	if final.Revision != 2 {
		t.Fatalf("concurrent writes produced revision %d", final.Revision)
	}
}

func TestBackupRejectsInconsistentHistory(t *testing.T) {
	src := testStore(t)
	p := create(t, src, "history")
	call(t, src, "posts.publish", action(p, true))
	b := exported(t, src)
	tests := []struct {
		name   string
		mutate func(*State)
	}{
		{"foreign live id", func(st *State) { st.Posts[p.ID].Live.ID = "another-post" }},
		{"foreign historical id", func(st *State) { st.Posts[p.ID].Revisions[0].ID = "another-post" }},
		{"future live revision", func(st *State) { st.Posts[p.ID].Live.Revision = 999 }},
		{"negative history revision", func(st *State) { st.Posts[p.ID].Revisions[0].Revision = -1 }},
		{"duplicate active slug", func(st *State) {
			copyRecord := *st.Posts[p.ID]
			copyRecord.Draft = st.Posts[p.ID].Draft
			copyRecord.Draft.ID = "other-id"
			copyRecord.Live = nil
			copyRecord.Revisions = []Post{copyRecord.Draft}
			st.Posts["other-id"] = &copyRecord
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := testStore(t)
			callError(t, dst, "backup.restore", restoreArgs(mutatedBackup(t, b, tt.mutate)), "validation")
		})
	}
}

func TestFormattedBackupJSONRestores(t *testing.T) {
	src := testStore(t)
	p := create(t, src, "formatted-backup")
	call(t, src, "posts.publish", action(p, true))
	backup := exported(t, src)
	raw, e := json.MarshalIndent(restoreArgs(backup), "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	dst := testStore(t)
	if _, e = dst.Call(context.Background(), "backup.restore", raw, "test"); e != nil {
		t.Fatalf("pretty-printing an exported JSON backup should preserve logical checksum: %v", e)
	}
	_, posts := dst.Public()
	if len(posts) != 1 || posts[0].ID != p.ID {
		t.Fatal("formatted restore lost publication")
	}
}

func TestMediaReadsObserveOtherHandleCommits(t *testing.T) {
	dir := t.TempDir()
	reader, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	writer, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer writer.Close()
	v := call(t, writer, "media.upload", map[string]any{"name": "cross-handle.png", "base64": tinyPNG})
	raw, _ := json.Marshal(v)
	var result struct {
		Media Media `json:"media"`
	}
	json.Unmarshal(raw, &result)
	m, ok := reader.Media(result.Media.ID)
	if !ok || m.Data != tinyPNG {
		t.Fatal("reader missed separately committed media")
	}
}

func TestSettingsCASAndValidation(t *testing.T) {
	s := testStore(t)
	settings := Settings{Title: "Updated site", Description: "Description", Author: "Test author", BaseURL: "https://blog.example/"}
	call(t, s, "settings.update", map[string]any{"settings": settings, "expected_revision": 1})
	public, _ := s.Public()
	if public.Title != settings.Title || public.BaseURL != "https://blog.example" {
		t.Fatalf("unexpected saved settings: %+v", public)
	}
	callError(t, s, "settings.update", map[string]any{"settings": settings, "expected_revision": 1}, "conflict")
	for _, badURL := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://user:password@example.com", "https://blog.example?token=secret", "https://blog.example#fragment", "/relative"} {
		settings.BaseURL = badURL
		callError(t, s, "settings.update", map[string]any{"settings": settings, "expected_revision": 2}, "validation")
	}
	after, _ := s.Public()
	if after != public {
		t.Fatal("failed settings updates modified instance")
	}
}

func TestBackupCanonicalChecksumSurvivesEquivalentJSON(t *testing.T) {
	src := testStore(t)
	p := getPost(t, call(t, src, "posts.create", map[string]any{"title": "星 🌟 <note> & café", "slug": "unicode-backup", "markdown": "A café note: 星 🌟 & <tag>\n\n[link](https://example.com/path)."}))
	call(t, src, "posts.publish", action(p, true))
	original := exported(t, src)
	// Python's ensure_ascii and jq-style reformatting can change key order,
	// HTML escaping, Unicode escaping, and whitespace without changing data.
	raw, _ := json.Marshal(restoreArgs(original))
	var generic any
	if e := json.Unmarshal(raw, &generic); e != nil {
		t.Fatal(e)
	}
	var transformed strings.Builder
	enc := json.NewEncoder(&transformed)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if e := enc.Encode(generic); e != nil {
		t.Fatal(e)
	}
	alternate := strings.NewReplacer("星", `\u661f`, "🌟", `\ud83c\udf1f`, "é", `\u00e9`, "/", `\/`).Replace(transformed.String())
	if !json.Valid([]byte(alternate)) {
		t.Fatal("alternate backup encoding fixture is invalid JSON")
	}
	dst := testStore(t)
	if _, e := dst.Call(context.Background(), "backup.restore", json.RawMessage(alternate), "test"); e != nil {
		t.Fatalf("logically identical backup encoding rejected: %v", e)
	}
	_, posts := dst.Public()
	if len(posts) != 1 || posts[0].Title != p.Title || posts[0].Markdown != p.Markdown {
		t.Fatalf("Unicode/escaping changed after restore: %+v", posts)
	}
}
