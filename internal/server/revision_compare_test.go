package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRevisionComparisonPermissionAndHistoricalPrivacy(t *testing.T) {
	srv, h := testServer(t)
	srv.ProposalToken = "proposal-revision-regression-token"
	private := postResult(t, operation(t, h, "posts.create", draftToken, map[string]any{"title": "PRIVATE_HISTORY_TITLE", "slug": "private-history-slug", "markdown": "PRIVATE_HISTORY_BODY", "excerpt": "PRIVATE_HISTORY_EXCERPT"}))
	current := postResult(t, operation(t, h, "posts.update", draftToken, map[string]any{"id": private.ID, "expected_revision": private.Revision, "title": "Public revision", "slug": "public-revision", "markdown": "Public body"}))
	operation(t, h, "posts.publish", adminToken, lifecycleAction(current))
	raw, _ := json.Marshal(map[string]any{"id": current.ID, "revision": current.Revision, "from_revision": current.Revision, "to_revision": private.Revision})
	for _, token := range []string{adminToken, draftToken, readToken, srv.ProposalToken} {
		w := request(h, "POST", "/api/op/posts.compare", token, string(raw), nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), private.Title) || !strings.Contains(w.Body.String(), private.Markdown) {
			t.Fatalf("private read denied %d", w.Code)
		}
		if token == readToken || token == srv.ProposalToken {
			w = request(h, "POST", "/api/op/posts.restore", token, `{}`, nil)
			if w.Code != 403 {
				t.Fatal("comparison expanded restore authority")
			}
		}
	}
	for _, token := range []string{"", "invalid"} {
		known := request(h, "POST", "/api/op/posts.compare", token, string(raw), nil)
		unknown := request(h, "POST", "/api/op/posts.compare", token, `{"id":"unknown","revision":1,"from_revision":1,"to_revision":1}`, nil)
		if known.Code != 401 || known.Body.String() != unknown.Body.String() {
			t.Fatal("unauthorized existence oracle")
		}
	}
	w := request(h, "POST", "/api/op/posts.compare", readToken, string(raw), map[string]string{"Origin": "https://evil.example"})
	if w.Code != 403 {
		t.Fatal("origin guard bypassed")
	}
	w = request(h, "POST", "/api/op/posts.compare", readToken, `{}`, map[string]string{"Accept-Language": "zh-CN"})
	if w.Code != 400 || !strings.Contains(w.Body.String(), "版本比较") {
		t.Fatal("validation not localized")
	}
	// A restore creates a private draft; public endpoints must continue serving R2.
	operation(t, h, "posts.restore", adminToken, map[string]any{"id": current.ID, "revision": private.Revision, "expected_revision": current.Revision, "idempotency_key": "private-restore"})
	for _, path := range []string{"/api/public/site", "/api/public/posts/" + current.Slug, "/api/public/search?q=PRIVATE_HISTORY", "/posts/" + current.Slug, "/feed.xml", "/sitemap.xml"} {
		w = request(h, "GET", path, "", "", nil)
		for _, marker := range []string{private.Title, private.Markdown, private.Slug, private.Excerpt} {
			if strings.Contains(w.Body.String(), marker) {
				t.Fatalf("historical private content leaked through %s", path)
			}
		}
	}
}
