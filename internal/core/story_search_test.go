package core

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func searchSaved(t *testing.T, s *Store, args map[string]any) StorySearch {
	t.Helper()
	return call(t, s, "posts.search", args).(StorySearch)
}

func TestStorySearchFieldsRankingAndReadOnly(t *testing.T) {
	s := testStore(t)
	createHit := func(slug, title, excerpt, markdown string, tags []string) Post {
		return getPost(t, call(t, s, "posts.create", map[string]any{"slug": slug, "title": title, "excerpt": excerpt, "markdown": markdown, "tags": tags}))
	}
	body := createHit("body", "Fictional body", "", "İİ 🐾\n中文雨声\n第二处雨声", nil)
	summary := createHit("summary", "Fictional summary", "只有摘要含雨声", "无命中", nil)
	title := createHit("title", "雨声标题", "", "无命中", nil)
	before, _ := json.Marshal(s.State)
	r := searchSaved(t, s, map[string]any{"query": " 雨声 "})
	again := searchSaved(t, s, map[string]any{"query": " 雨声 "})
	after, _ := json.Marshal(s.State)
	if string(before) != string(after) || !reflect.DeepEqual(r, again) || r.Query != "雨声" || r.Total != 3 || r.Limit != 20 || r.Truncated || r.InstanceID != s.instanceID {
		t.Fatal("search wrote state, changed repeat result, or lost query/snapshot identity")
	}
	if r.Results[0].ID != title.ID || r.Results[1].ID != summary.ID || r.Results[2].ID != body.ID {
		t.Fatal("field priority is not title, excerpt, Markdown")
	}
	m := r.Results[2].Matches[0]
	if m.Field != "markdown" || m.Line != 2 || m.Start != 7 || m.End != 9 || m.Text != "雨声" || []rune(body.Markdown)[m.Start] != '雨' {
		t.Fatalf("original Unicode source location drifted: %+v", m)
	}
	legacy := createHit("legacy-slug", "Fictional old options", "", "body", []string{"中文标签"})
	if searchSaved(t, s, map[string]any{"query": "legacy-slug"}).Results[0].Matches[0].Field != "slug" || searchSaved(t, s, map[string]any{"query": "标签"}).Results[0].Matches[0].Field != "tags" {
		t.Fatal("existing Studio slug/tag search options lost")
	}
	call(t, s, "posts.delete", action(legacy, true))
	if searchSaved(t, s, map[string]any{"query": "标签"}).Total != 0 || searchSaved(t, s, map[string]any{"query": "标签", "status": "trash"}).Total != 1 {
		t.Fatal("trash was not explicitly isolated")
	}
	limited := searchSaved(t, s, map[string]any{"query": "雨声", "limit": 1})
	if len(limited.Results) != 1 || limited.Total != 3 || !limited.Truncated || limited.Results[0].ID != title.ID {
		t.Fatal("bounded result limit/rank/count")
	}
}

func TestStorySearchCurrentDraftNotHistoryOrLive(t *testing.T) {
	s := testStore(t)
	p := getPost(t, call(t, s, "posts.create", map[string]any{"title": "Fictional live", "slug": "snapshot", "markdown": "PUBLIC_ONLY_WORD"}))
	p = getPost(t, call(t, s, "posts.publish", action(p, true)))
	p = getPost(t, call(t, s, "posts.update", map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": p.Title, "slug": p.Slug, "markdown": "PRIVATE_SAVED_WORD", "excerpt": "PRIVATE_SUMMARY_WORD"}))
	if searchSaved(t, s, map[string]any{"query": "PUBLIC_ONLY_WORD"}).Total != 0 {
		t.Fatal("current-draft search mixed live/history snapshots")
	}
	r := searchSaved(t, s, map[string]any{"query": "private_saved_word", "status": "changed"})
	if r.Total != 1 || r.Results[0].Revision != p.Revision || r.Results[0].PublishedRevision != 1 || r.Results[0].Matches[0].Text != "PRIVATE_SAVED_WORD" {
		t.Fatal("saved/live identity or simple case match")
	}
	if searchSaved(t, s, map[string]any{"query": "PRIVATE_SAVED_WORD", "status": "published"}).Total != 0 {
		t.Fatal("status filter ignored")
	}
	// Legacy operation output and semantics are deliberately untouched.
	if len(call(t, s, "posts.list", map[string]any{"query": "PRIVATE_SUMMARY_WORD"}).(map[string]any)["posts"].([]Post)) != 0 {
		t.Fatal("old posts.list query semantics changed")
	}
}

func TestStorySearchLiteralOffsetsAndResponseBounds(t *testing.T) {
	m, ok := storyMatch("markdown", "İİ 🐾 i", "i")
	if !ok || m.Start != 0 || m.End != 1 || m.Text != "İ" {
		t.Fatal("case conversion byte width changed original rune offsets")
	}
	if _, ok = storyMatch("markdown", "abc", ".*"); ok {
		t.Fatal("literal substring became regular expression")
	}
	query := strings.Repeat("雨", 128)
	m, ok = storyMatch("markdown", strings.Repeat("前", 1000)+query+strings.Repeat("后", 1000), query)
	if !ok || !m.TruncatedStart || !m.TruncatedEnd || m.SnippetStart != 960 || m.Start != 1000 || utf8.RuneCountInString(m.Snippet) != 228 {
		t.Fatal("snippet or offset bound")
	}
	s := testStore(t)
	call(t, s, "posts.create", map[string]any{"title": "Fictional long source", "slug": "long", "markdown": strings.Repeat("前", 10000) + query + strings.Repeat("后", 10000)})
	raw, _ := json.Marshal(searchSaved(t, s, map[string]any{"query": query}))
	if len(raw) > 5000 || strings.Contains(string(raw), `"html"`) || strings.Contains(string(raw), `"revisions"`) || strings.Contains(string(raw), `"markdown":`) {
		t.Fatal("search returned full bodies, HTML or history")
	}
}

func TestStorySearchValidationAndSchema(t *testing.T) {
	s := testStore(t)
	for _, a := range []map[string]any{{}, {"query": nil}, {"query": " "}, {"query": strings.Repeat("a", 129)}, {"query": "x", "limit": nil}, {"query": "x", "limit": 0}, {"query": "x", "limit": 51}, {"query": "x", "limit": 1.5}, {"query": "x", "limit": "1"}, {"query": "x", "status": nil}, {"query": "x", "status": "draft|changed"}, {"query": "x", "status": "all"}, {"query": "x", "markdown": "not a write"}} {
		callError(t, s, "posts.search", a, "validation")
	}
	spec := Specs["posts.search"]
	if spec.Scope != "read" || !spec.ReadOnly || spec.InputSchema["additionalProperties"] != false || !reflect.DeepEqual(spec.InputSchema["required"], []string{"query"}) {
		t.Fatal("search changed read authority or loose schema")
	}
}

func TestStorySearchTopFiftyAndStableTieOrder(t *testing.T) {
	s := testStore(t)
	ids := []string{}
	for i := 0; i < 55; i++ {
		p := getPost(t, call(t, s, "posts.create", map[string]any{"title": "Fictional source", "slug": "bounded-" + strconv.Itoa(i), "markdown": "声"}))
		// Equal timestamps must have a stable ID tie-break, independent of map iteration.
		s.State.Posts[p.ID].Draft.UpdatedAt = "2026-01-01T00:00:00Z"
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	title := getPost(t, call(t, s, "posts.create", map[string]any{"title": "声", "slug": "strongest-field", "markdown": "声", "excerpt": "声", "tags": []string{"声"}}))
	r := searchSaved(t, s, map[string]any{"query": "声", "limit": 50})
	if r.Total != 56 || !r.Truncated || len(r.Results) != 50 || r.Results[0].ID != title.ID || len(r.Results[0].Matches) != 4 {
		t.Fatal("one-character CJK, multiple fields, top-fifty ranking or total count")
	}
	for i := 1; i < 50; i++ {
		if r.Results[i].ID != ids[i-1] {
			t.Fatal("bounded candidates lost stable tie order")
		}
	}
	for i := 0; i < 10; i++ {
		if !reflect.DeepEqual(r, searchSaved(t, s, map[string]any{"query": "声", "limit": 50})) {
			t.Fatal("map iteration changed limited results")
		}
	}
}

func TestStorySearchRawNewlinesAndLineNumbers(t *testing.T) {
	for _, sample := range []struct {
		source, query    string
		start, end, line int
	}{
		{"first\r\n雨声", "雨声", 7, 9, 2},
		{"a\r\n声声", "声", 3, 4, 2},
		{"\n雨声", "雨声", 1, 3, 2},
		{"first\r雨声", "雨声", 6, 8, 2},
		{"🐾\r\n雨声🐾", "雨声", 3, 5, 2},
		{"\n🐾\r\nfirst\r声声\r\n雨声🐾", "雨声", 14, 16, 5},
		{"İ\r\n🐾声声", "声", 4, 5, 2},
	} {
		m, ok := storyMatch("markdown", sample.source, strings.ToLower(sample.query))
		if !ok || m.Start != sample.start || m.End != sample.end || m.Line != sample.line || m.Text != sample.query || m.Snippet != sample.source {
			t.Fatalf("raw source changed or CRLF/CR/LF line mapping drifted: %+v", m)
		}
	}
}

func TestStorySearchUnicodeWhitespaceContract(t *testing.T) {
	s := testStore(t)
	call(t, s, "posts.create", map[string]any{"title": "Fictional Unicode boundary", "slug": "whitespace-contract", "markdown": "雨声 \u200b雨声\u200b \ufeff雨声\ufeff"})
	spaces := []rune{9, 10, 11, 12, 13, 32, 0x85, 0xa0, 0x1680, 0x2000, 0x2001, 0x2002, 0x2003, 0x2004, 0x2005, 0x2006, 0x2007, 0x2008, 0x2009, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000}
	for _, space := range spaces {
		r := searchSaved(t, s, map[string]any{"query": string(space) + "雨声" + string(space)})
		if r.Query != "雨声" || r.Total != 1 || r.Results[0].Matches[0].Start != 0 {
			t.Fatalf("Unicode White_Space U+%04X disagrees with documented query contract", space)
		}
	}
	for _, literal := range []string{"\u200b雨声\u200b", "\ufeff雨声\ufeff"} {
		r := searchSaved(t, s, map[string]any{"query": literal})
		if r.Query != literal || r.Total != 1 || r.Results[0].Matches[0].Text != literal {
			t.Fatal("U+200B or U+FEFF was treated as trimming whitespace")
		}
	}
}
