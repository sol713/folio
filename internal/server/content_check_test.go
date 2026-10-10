package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContentCheckAuthorizationLocalizationAndNoPublicLeak(t *testing.T) {
	srv, h := testServer(t)
	srv.ProposalToken = "proposal-test-token"
	target := postResult(t, operation(t, h, "posts.create", draftToken, map[string]any{"title": "Private target secret", "slug": "private-target-secret", "markdown": "Private body"}))
	source := postResult(t, operation(t, h, "posts.create", draftToken, map[string]any{"title": "Source", "slug": "source", "markdown": "[target](/posts/" + target.Slug + ")"}))
	raw, _ := json.Marshal(map[string]any{"id": source.ID, "revision": source.Revision})
	for _, token := range []string{adminToken, draftToken, readToken, srv.ProposalToken} {
		w := request(h, "POST", "/api/op/posts.check", token, string(raw), nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"article_private"`) {
			t.Fatalf("authorized %s status %d", token, w.Code)
		}
		if strings.Contains(w.Body.String(), target.ID) || strings.Contains(w.Body.String(), target.Title) || strings.Contains(w.Body.String(), "Private body") {
			t.Fatal("unnecessary target metadata disclosed")
		}
	}
	for _, token := range []string{"", "incorrect"} {
		known := request(h, "POST", "/api/op/posts.check", token, string(raw), nil)
		unknown := request(h, "POST", "/api/op/posts.check", token, `{"id":"unknown","revision":1}`, nil)
		if known.Code != 401 || known.Body.String() != unknown.Body.String() {
			t.Fatal("unauthorized existence oracle")
		}
	}
	w := request(h, "POST", "/api/op/posts.check", readToken, string(raw), map[string]string{"Accept-Language": "zh-CN"})
	if !strings.Contains(w.Body.String(), "私密草稿") {
		t.Fatal("findings not localized")
	}
	w = request(h, "POST", "/api/op/posts.check", readToken, string(raw), map[string]string{"Origin": "https://evil.example"})
	if w.Code != 403 {
		t.Fatal("check bypassed origin policy")
	}
	w = request(h, "GET", "/api/public/posts/"+target.Slug, "", "", nil)
	if w.Code != 404 || strings.Contains(w.Body.String(), target.ID) || strings.Contains(w.Body.String(), target.Title) {
		t.Fatal("private target became public")
	}
}
