package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"folio/internal/core"
)

const adminToken = "secret-admin-regression-token"
const draftToken = "secret-draft-regression-token"
const readToken = "secret-reader-regression-token"

func testServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	s, e := core.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	srv := &Server{Store: s, Token: adminToken, DraftToken: draftToken, ReadToken: readToken, Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(`<!doctype html><html><head><title>UI</title></head><body><div id="app"></div><script src="/app.js"></script></body></html>`)}, "app.js": &fstest.MapFile{Data: []byte(`console.log("public app")`)}}}
	return srv, srv.Handler()
}
func request(h http.Handler, method, path, token, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://blog.example"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func operation(t *testing.T, h http.Handler, op, token string, args any) map[string]json.RawMessage {
	t.Helper()
	b, _ := json.Marshal(args)
	w := request(h, "POST", "/api/op/"+op, token, string(b), nil)
	if w.Code != 200 {
		t.Fatalf("%s HTTP %d: %s", op, w.Code, w.Body.String())
	}
	var env struct {
		OK   bool                       `json:"ok"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &env); e != nil || !env.OK {
		t.Fatalf("bad envelope %s: %v", w.Body.String(), e)
	}
	return env.Data
}
func postResult(t *testing.T, m map[string]json.RawMessage) core.Post {
	t.Helper()
	var p core.Post
	if e := json.Unmarshal(m["post"], &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func newPost(t *testing.T, h http.Handler) core.Post {
	return postResult(t, operation(t, h, "posts.create", draftToken, map[string]any{"title": "Public original", "slug": "public-original", "markdown": "PUBLIC_ORIGINAL_BODY", "excerpt": "Original public summary"}))
}
func lifecycleAction(p core.Post) map[string]any {
	return map[string]any{"id": p.ID, "expected_revision": p.Revision, "confirm": true}
}

func TestAuthoringAuthAndRoles(t *testing.T) {
	_, h := testServer(t)
	for _, token := range []string{"", "wrong", "Bearer " + adminToken} {
		w := request(h, "POST", "/api/op/posts.list", token, `{}`, nil)
		if w.Code != 401 {
			t.Errorf("invalid token %q status %d", token, w.Code)
		}
	}
	for _, token := range []string{adminToken, draftToken, readToken} {
		if w := request(h, "POST", "/api/op/posts.list", token, `{}`, nil); w.Code != 200 {
			t.Errorf("read failed with role token: %d %s", w.Code, w.Body.String())
		}
	}
	p := newPost(t, h)
	matrix := []struct{ op, token, body string }{
		{"posts.create", readToken, `{"title":"No","slug":"no","markdown":"body"}`},
		{"posts.publish", draftToken, `{"id":"` + p.ID + `","expected_revision":1,"confirm":true}`},
		{"posts.delete", draftToken, `{"id":"` + p.ID + `","expected_revision":1,"confirm":true}`},
		{"settings.update", draftToken, `{}`}, {"backup.export", draftToken, `{}`}, {"backup.restore", draftToken, `{}`}, {"audit.list", readToken, `{}`},
	}
	for _, tt := range matrix {
		w := request(h, "POST", "/api/op/"+tt.op, tt.token, tt.body, nil)
		if w.Code != 403 {
			t.Errorf("role failed to forbid %s: %d %s", tt.op, w.Code, w.Body.String())
		}
	}
	w := request(h, "POST", "/api/op/system.capabilities", "", `{}`, nil)
	if w.Code != 200 {
		t.Fatalf("discovery should be public: %d", w.Code)
	}
	w = request(h, "GET", "/api/op/posts.list", adminToken, "", nil)
	if w.Code == 200 || w.Header().Get("Allow") != "POST" {
		t.Fatal("GET authoring operation accepted")
	}
	operation(t, h, "posts.publish", adminToken, lifecycleAction(p))
	audit := operation(t, h, "audit.list", adminToken, map[string]any{})
	for _, token := range []string{adminToken, draftToken, readToken} {
		if strings.Contains(string(audit["events"]), token) {
			t.Fatal("audit contains raw bearer token")
		}
	}
}

func TestCrossOriginAndMalformedRequests(t *testing.T) {
	_, h := testServer(t)
	for _, origin := range []string{"https://evil.example", "null", "http://blog.example", "https://user@blog.example", "https://blog.example/not-an-origin", "https://blog.example?query", "https://blog.example#fragment"} {
		w := request(h, "POST", "/api/op/posts.list", adminToken, `{}`, map[string]string{"Origin": origin})
		if w.Code != 403 {
			t.Errorf("cross-origin %q accepted with %d", origin, w.Code)
		}
	}
	w := request(h, "POST", "/api/op/posts.list", adminToken, `{}`, map[string]string{"Origin": "https://blog.example"})
	if w.Code != 200 {
		t.Fatalf("same origin rejected: %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`[]`, `null`, `42`, `{"x":`, `{} {}`, " \n ", `{"unknown":1}`} {
		w = request(h, "POST", "/api/op/posts.list", adminToken, body, nil)
		if w.Code != 400 {
			t.Errorf("invalid JSON accepted %q: %d", body, w.Code)
		}
	}
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "application/jsonevil"} {
		w = request(h, "POST", "/api/op/posts.list", adminToken, `{}`, map[string]string{"Content-Type": ct})
		if w.Code != 400 && w.Code != 415 {
			t.Errorf("bad content type %q accepted: %d", ct, w.Code)
		}
	}
	w = request(h, "POST", "/api/op/posts.list", adminToken, `{}`, map[string]string{"Content-Type": "application/json; charset=utf-8"})
	if w.Code != 200 {
		t.Error("valid parameterized JSON type rejected")
	}
	w = request(h, "POST", "/api/op/posts.create", draftToken, `{"title":"Large","slug":"large","markdown":"`+strings.Repeat("x", 8*1024*1024)+`"}`, nil)
	if w.Code != 400 && w.Code != 413 {
		t.Fatalf("oversized body not rejected: %d", w.Code)
	}
}

func TestPrivateEditsStayOutOfEveryPublicSurface(t *testing.T) {
	_, h := testServer(t)
	p := newPost(t, h)
	for _, path := range []string{"/api/public/site", "/api/public/search?q=PUBLIC_ORIGINAL_BODY", "/rss.xml", "/sitemap.xml", "/"} {
		w := request(h, "GET", path, "", "", nil)
		if strings.Contains(w.Body.String(), "PUBLIC_ORIGINAL_BODY") || strings.Contains(w.Body.String(), "public-original") {
			t.Fatalf("draft leaked through %s: %s", path, w.Body.String())
		}
	}
	operation(t, h, "posts.publish", adminToken, lifecycleAction(p))
	original := map[string]string{}
	for _, path := range []string{"/api/public/site", "/api/public/posts/public-original", "/api/public/search?q=PUBLIC_ORIGINAL_BODY", "/rss.xml", "/sitemap.xml", "/posts/public-original", "/"} {
		w := request(h, "GET", path, "", "", nil)
		if w.Code != 200 {
			t.Fatalf("published surface %s unavailable", path)
		}
		original[path] = w.Body.String()
	}
	next := postResult(t, operation(t, h, "posts.update", draftToken, map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": "PRIVATE_CHANGED_TITLE", "slug": "private-changed-slug", "markdown": "PRIVATE_SECRET_BODY", "excerpt": "PRIVATE_SECRET_EXCERPT"}))
	for path, before := range original {
		w := request(h, "GET", path, "", "", nil)
		if w.Body.String() != before {
			t.Errorf("private edit altered %s\nbefore %s\nafter %s", path, before, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "PRIVATE_") {
			t.Errorf("private content leaked at %s", path)
		}
	}
	w := request(h, "GET", "/api/public/search?q=PRIVATE_SECRET_BODY", "", "", nil)
	if strings.Contains(w.Body.String(), `"id":`) {
		t.Fatal("private edit entered search")
	}
	for _, path := range []string{"/api/public/posts/private-changed-slug", "/posts/private-changed-slug"} {
		w = request(h, "GET", path, "", "", nil)
		if w.Code != 404 {
			t.Errorf("unpublished route became public %s: %d", path, w.Code)
		}
	}
	w = request(h, "GET", "/posts/public-original", "", "", nil)
	for _, expected := range []string{"Public original · FOLIO", `name="description" content="Original public summary"`, `rel="canonical" href="http://localhost:8080/posts/public-original"`, "PUBLIC_ORIGINAL_BODY"} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Errorf("SEO page missing %s", expected)
		}
	}
	w = request(h, "POST", "/api/op/posts.publish", adminToken, `{"id":"`+p.ID+`","expected_revision":1,"confirm":true}`, nil)
	if w.Code != 409 {
		t.Fatal("stale publish accepted")
	}
	operation(t, h, "posts.unpublish", adminToken, lifecycleAction(next))
	for _, path := range []string{"/api/public/site", "/rss.xml", "/sitemap.xml", "/"} {
		w = request(h, "GET", path, "", "", nil)
		if strings.Contains(w.Body.String(), "public-original") {
			t.Errorf("unpublished content remains at %s", path)
		}
	}
}

func TestPublicHeadersTokensAndSafeSEO(t *testing.T) {
	srv, h := testServer(t)
	b, _ := json.Marshal(map[string]any{"title": `<img src=x onerror=alert(1)>`, "slug": "safe-escaping", "markdown": "# Safe\n<script>alert(1)</script>", "excerpt": `\" onmouseover=\"alert(1)`})
	v, e := srv.Store.Call(context.Background(), "posts.create", b, "test")
	if e != nil {
		t.Fatal(e)
	}
	result, _ := json.Marshal(v)
	var cp struct {
		Post core.Post `json:"post"`
	}
	json.Unmarshal(result, &cp)
	operation(t, h, "posts.publish", adminToken, lifecycleAction(cp.Post))
	for _, path := range []string{"/", "/studio", "/api/public/site", "/posts/safe-escaping", "/rss.xml", "/sitemap.xml", "/robots.txt", "/app.js", "/healthz"} {
		w := request(h, "GET", path, "", "", nil)
		for _, token := range []string{adminToken, draftToken, readToken} {
			if strings.Contains(w.Body.String(), token) {
				t.Errorf("%s leaks bearer token", path)
			}
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s missing security headers", path)
		}
	}
	w := request(h, "GET", "/posts/safe-escaping", "", "", nil)
	if strings.Contains(w.Body.String(), "<img src=x") || strings.Contains(w.Body.String(), "<script>alert(1)") {
		t.Fatal("unsafe content reached SEO HTML")
	}
	w = request(h, "GET", "/studio", "", "", nil)
	if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), `name="robots" content="noindex,nofollow"`) {
		t.Fatal("studio missing private page metadata")
	}
	w = request(h, "POST", "/api/op/posts.list", adminToken, `{}`, nil)
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authoring response may be cached")
	}
}

func TestMediaServingHeadETagAndTraversal(t *testing.T) {
	_, h := testServer(t)
	data := operation(t, h, "media.upload", draftToken, map[string]any{"name": "one.png", "base64": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6cqQAAAAASUVORK5CYII="})
	var m core.Media
	json.Unmarshal(data["media"], &m)
	w := request(h, "GET", m.URL, "", "", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Body.Len() != m.Size {
		t.Fatalf("media fetch failed: %d %s", w.Code, w.Body.String())
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("media needs ETag")
	}
	w = request(h, "HEAD", m.URL, "", "", nil)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD returned image bytes")
	}
	w = request(h, "GET", m.URL, "", "", map[string]string{"If-None-Match": etag})
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal("conditional GET incorrect")
	}
	for _, path := range []string{"/media/../folio.db", "/media/%2e%2e/folio.db", "/media/..%5Cfolio.db", "/media/foo/bar"} {
		w = request(h, "GET", path, "", "", nil)
		if w.Code != 404 {
			t.Errorf("traversal returned %d for %s", w.Code, path)
		}
	}
}

func TestHTTPHandlerDoesNotPanicOnMalformedInput(t *testing.T) {
	_, h := testServer(t)
	for _, body := range []string{"", `{}`, `null`, `{"id":null}`, `{"id":[]}`, `{"id":{}}`, `{"id":true}`, `{"id":1e999}`, `{"id":"x","expected_revision":-1,"confirm":true}`} {
		for _, op := range []string{"posts.get", "posts.update", "posts.publish", "backup.restore", "media.upload"} {
			func() {
				defer func() {
					if v := recover(); v != nil {
						t.Errorf("%s panicked on %q: %v", op, body, v)
					}
				}()
				r := httptest.NewRequest("POST", "https://blog.example/api/op/"+op, io.NopCloser(strings.NewReader(body)))
				r.Header.Set("Authorization", "Bearer "+adminToken)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code >= 500 {
					t.Errorf("malformed input generated server error: %s %q => %d %s", op, body, w.Code, w.Body.String())
				}
			}()
		}
	}
}

func TestPublishedSlugRedirectsOnlyAfterRepublish(t *testing.T) {
	_, h := testServer(t)
	p := newPost(t, h)
	operation(t, h, "posts.publish", adminToken, lifecycleAction(p))
	p = postResult(t, operation(t, h, "posts.update", draftToken, map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": "Renamed", "slug": "renamed-public-post", "markdown": "Renamed body"}))
	for _, path := range []string{"/posts/public-original", "/api/public/posts/public-original"} {
		w := request(h, "GET", path, "", "", nil)
		if w.Code != 200 {
			t.Errorf("private slug edit affected old live route %s: %d", path, w.Code)
		}
	}
	operation(t, h, "posts.publish", adminToken, lifecycleAction(p))
	routes := map[string]string{"/posts/public-original": "/posts/renamed-public-post", "/api/public/posts/public-original": "/api/public/posts/renamed-public-post"}
	for old, target := range routes {
		w := request(h, "GET", old, "", "", nil)
		if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != target {
			t.Errorf("old slug %s did not redirect safely: %d %q", old, w.Code, w.Header().Get("Location"))
		}
	}
	operation(t, h, "posts.unpublish", adminToken, lifecycleAction(p))
	for old := range routes {
		w := request(h, "GET", old, "", "", nil)
		if w.Code != 404 {
			t.Errorf("unpublished old slug remains reachable: %s %d", old, w.Code)
		}
	}
}

func TestHTTPSOriginBehindHTTPReverseProxy(t *testing.T) {
	_, h := testServer(t)
	proxyRequest := func(origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://blog.example/api/op/posts.list", strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer "+adminToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := proxyRequest("https://blog.example"); w.Code != 403 {
		t.Fatalf("unconfigured forwarded origin accepted: %d", w.Code)
	}
	operation(t, h, "settings.update", adminToken, map[string]any{"expected_revision": 1, "settings": core.Settings{Title: "Proxy site", Description: "Public HTTPS", Author: "Test", BaseURL: "https://blog.example"}})
	if w := proxyRequest("https://blog.example"); w.Code != 200 {
		t.Fatalf("configured HTTPS origin rejected behind HTTP proxy: %d %s", w.Code, w.Body.String())
	}
	for _, origin := range []string{"http://blog.example", "https://evil.example", "https://user@blog.example", "https://blog.example/path", "https://blog.example?query", "https://blog.example#fragment", "null"} {
		if w := proxyRequest(origin); w.Code != 403 {
			t.Errorf("bad proxy origin %q accepted: %d", origin, w.Code)
		}
	}
}
