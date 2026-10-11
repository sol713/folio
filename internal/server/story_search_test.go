package server

import (
	"bytes"
	"strings"
	"testing"
)

func TestStorySearchReadRolesAndPublicIsolation(t *testing.T) {
	srv, h := testServer(t)
	srv.ProposalToken = "proposal-search-regression-token"
	p := postResult(t, operation(t, h, "posts.create", draftToken, map[string]any{"title": "Fictional public title", "slug": "search-isolation", "markdown": "PUBLIC_SEARCH_BODY"}))
	operation(t, h, "posts.publish", adminToken, lifecycleAction(p))
	private := postResult(t, operation(t, h, "posts.update", draftToken, map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": "PRIVATE_SEARCH_TITLE", "slug": p.Slug, "markdown": "PRIVATE_SEARCH_BODY", "excerpt": "PRIVATE_SEARCH_SUMMARY_中文"}))
	before := operation(t, h, "backup.export", adminToken, map[string]any{})["state"]
	for _, token := range []string{adminToken, draftToken, readToken, srv.ProposalToken} {
		w := request(h, "POST", "/api/op/posts.search", token, `{"query":"中文"}`, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), private.Excerpt) || strings.Contains(w.Body.String(), `"markdown":`) || strings.Contains(w.Body.String(), `"html":`) {
			t.Fatal("existing private read role denied or full bodies exposed")
		}
	}
	for _, token := range []string{"", "invalid"} {
		a := request(h, "POST", "/api/op/posts.search", token, `{"query":"PRIVATE_SEARCH"}`, nil)
		b := request(h, "POST", "/api/op/posts.search", token, `{"query":"absent"}`, nil)
		if a.Code != 401 || a.Body.String() != b.Body.String() {
			t.Fatal("search added an anonymous existence oracle")
		}
	}
	if request(h, "POST", "/api/op/posts.search", readToken, `{"query":"中文"}`, map[string]string{"Origin": "https://evil.example"}).Code != 403 {
		t.Fatal("origin guard bypassed")
	}
	w := request(h, "POST", "/api/op/posts.search", readToken, `{"query":" "}`, map[string]string{"Accept-Language": "zh-CN"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "文章搜索") {
		t.Fatal("validation not localized")
	}
	for _, path := range []string{"/api/public/site", "/api/public/posts/search-isolation", "/api/public/search?q=PRIVATE_SEARCH", "/posts/search-isolation", "/feed.xml", "/sitemap.xml"} {
		w = request(h, "GET", path, "", "", nil)
		if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE_SEARCH") {
			t.Fatalf("saved-draft search leaked private edits through %s", path)
		}
	}
	if after := operation(t, h, "backup.export", adminToken, map[string]any{})["state"]; !bytes.Equal(before, after) {
		t.Fatal("read-only searches changed persisted state")
	}
}
