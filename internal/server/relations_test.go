package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRelationsPermissionAndPrivateDataNotPublic(t *testing.T) {
	srv, h := testServer(t)
	srv.ProposalToken = "proposal-relations-regression-token"
	target := newPost(t, h)
	source := postResult(t, operation(t, h, "posts.create", draftToken, map[string]any{"title": "PRIVATE_SOURCE_TITLE", "slug": "private-source", "markdown": "[target](/posts/" + target.Slug + ")"}))
	raw, _ := json.Marshal(map[string]any{"id": target.ID, "revision": target.Revision})
	for _, token := range []string{adminToken, draftToken, readToken, srv.ProposalToken} {
		w := request(h, "POST", "/api/op/posts.relations", token, string(raw), nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), source.ID) || !strings.Contains(w.Body.String(), source.Title) {
			t.Fatalf("private-read role denied %d", w.Code)
		}
	}
	for _, token := range []string{"", "bad"} {
		known := request(h, "POST", "/api/op/posts.relations", token, string(raw), nil)
		unknown := request(h, "POST", "/api/op/posts.relations", token, `{"id":"unknown","revision":1}`, nil)
		if known.Code != 401 || known.Body.String() != unknown.Body.String() {
			t.Fatal("unauthorized existence oracle")
		}
	}
	w := request(h, "POST", "/api/op/posts.relations", readToken, string(raw), map[string]string{"Origin": "https://evil.example"})
	if w.Code != 403 {
		t.Fatal("origin guard bypassed")
	}
	w = request(h, "POST", "/api/op/posts.relations", readToken, `{"id":"x","revision":0}`, map[string]string{"Accept-Language": "zh-CN"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "文章引用关系") {
		t.Fatal("validation not localized")
	}
	operation(t, h, "posts.publish", adminToken, lifecycleAction(target))
	for _, path := range []string{"/api/public/site", "/api/public/posts/" + target.Slug, "/api/public/search?q=PRIVATE_SOURCE_TITLE", "/posts/" + target.Slug, "/feed.xml", "/sitemap.xml"} {
		w = request(h, "GET", path, "", "", nil)
		if strings.Contains(w.Body.String(), source.ID) || strings.Contains(w.Body.String(), source.Title) || strings.Contains(w.Body.String(), source.Slug) {
			t.Fatalf("private relation leaked through %s", path)
		}
	}
}
