package server

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"folio/internal/core"
	"folio/internal/i18n"
)

func localizedError(t *testing.T, w *httptest.ResponseRecorder, status int, code, locale string) string {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP %d, want %d: %s", w.Code, status, w.Body.String())
	}
	if got := w.Header().Get("Content-Language"); got != locale {
		t.Errorf("Content-Language=%q, want %q", got, locale)
	}
	var envelope map[string]json.RawMessage
	if e := json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
		t.Fatal(e)
	}
	if string(envelope["ok"]) != "false" {
		t.Errorf("error changed machine envelope: %s", w.Body.String())
	}
	var err map[string]string
	if e := json.Unmarshal(envelope["error"], &err); e != nil {
		t.Fatal(e)
	}
	if len(err) != 2 || err["code"] != code || err["message"] == "" {
		t.Fatalf("unstable error keys/code: %+v", err)
	}
	return err["message"]
}

func TestLocalizedHTTPErrorsKeepCodesAndArgumentsStable(t *testing.T) {
	_, h := testServer(t)
	p := newPost(t, h)
	operation(t, h, "posts.update", draftToken, map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": "Updated", "slug": p.Slug, "markdown": "Next version"})
	cases := []struct {
		name, op, token, body, code, template string
		status                                int
		args                                  []any
	}{
		{"auth", "posts.list", "", `{}`, "unauthorized", "A valid Bearer token is required", 401, nil},
		{"validation", "posts.create", draftToken, `{"title":"","slug":"invalid-title","markdown":"body"}`, "validation", "Title must contain 1–200 characters", 400, nil},
		{"conflict", "posts.publish", adminToken, `{"id":"` + p.ID + `","expected_revision":1,"confirm":true}`, "conflict", "Revision conflict: expected %d, current %d. Fetch before retrying.", 409, []any{1, 2}},
		{"role", "posts.publish", draftToken, `{"id":"` + p.ID + `","expected_revision":2,"confirm":true}`, "forbidden", "This token cannot perform %s; a publisher/admin token is required", 403, []any{"posts.publish"}},
		{"confirmation", "posts.publish", adminToken, `{"id":"` + p.ID + `","expected_revision":2,"confirm":false}`, "confirmation_required", "Set confirm:true for this action", 400, nil},
		{"unknown operation", "not.real", adminToken, `{}`, "not_found", "Unknown operation", 404, nil},
	}
	for _, tt := range cases {
		for _, locale := range []string{i18n.Chinese, i18n.English} {
			t.Run(tt.name+"/"+locale, func(t *testing.T) {
				w := request(h, "POST", "/api/op/"+tt.op, tt.token, tt.body, map[string]string{"Accept-Language": locale})
				message := localizedError(t, w, tt.status, tt.code, locale)
				if want := i18n.Format(locale, tt.template, tt.args...); message != want {
					t.Errorf("message=%q, want %q", message, want)
				}
			})
		}
	}
	w := request(h, "POST", "/api/op/posts.list", "", `{}`, nil)
	if got := localizedError(t, w, 401, "unauthorized", i18n.Chinese); got != "需要有效的 Bearer 访问令牌" {
		t.Fatalf("default not Chinese: %s", got)
	}
	w = request(h, "POST", "/api/op/posts.list", "", `{}`, map[string]string{"Accept-Language": "en", "X-Folio-Lang": "zh-CN"})
	if got := localizedError(t, w, 401, "unauthorized", i18n.Chinese); got != "需要有效的 Bearer 访问令牌" {
		t.Fatal("explicit language did not win")
	}
	w = request(h, "POST", "/api/op/posts.create", draftToken, `{"title":"X","slug":"x","markdown":"body","unexpected_key":true}`, map[string]string{"Accept-Language": "zh-CN"})
	message := localizedError(t, w, 400, "validation", i18n.Chinese)
	if !strings.HasPrefix(message, "参数无效：") || !strings.Contains(message, "unexpected_key") {
		t.Fatalf("dynamic JSON validation context lost: %q", message)
	}
}

func TestErrorCanonicalRepresentationStaysEnglish(t *testing.T) {
	err := core.Err("conflict", "Revision conflict: expected %d, current %d. Fetch before retrying.", 3, 4)
	ce, ok := err.(*core.Error)
	if !ok {
		t.Fatal("not a core Error")
	}
	if ce.Message != "Revision conflict: expected 3, current 4. Fetch before retrying." || err.Error() != ce.Message {
		t.Fatalf("core error lost canonical English: %+v", ce)
	}
	if ce.Template == "" || len(ce.Arguments) != 2 {
		t.Fatal("dynamic localization metadata missing")
	}
	raw, e := json.Marshal(ce)
	if e != nil {
		t.Fatal(e)
	}
	var decoded map[string]any
	json.Unmarshal(raw, &decoded)
	if len(decoded) != 2 || decoded["code"] != "conflict" || decoded["message"] != ce.Message {
		t.Fatalf("private localization metadata leaked into JSON: %s", raw)
	}
}

func TestCapabilitiesRemainCanonicalEnglishAcrossLocales(t *testing.T) {
	_, h := testServer(t)
	var baseline []byte
	for _, headers := range []map[string]string{nil, {"Accept-Language": "zh-CN"}, {"Accept-Language": "en"}, {"X-Folio-Lang": "en", "Accept-Language": "zh-CN"}} {
		w := request(h, "POST", "/api/op/system.capabilities", "", `{}`, headers)
		if w.Code != 200 {
			t.Fatalf("capabilities: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Content-Language") != i18n.English {
			t.Fatal("canonical schemas mislabeled as translated")
		}
		if baseline == nil {
			baseline = append([]byte{}, w.Body.Bytes()...)
		} else if !bytes.Equal(baseline, w.Body.Bytes()) {
			t.Fatal("locale changed operation identifiers or capability schemas")
		}
	}
	var result struct {
		Data json.RawMessage `json:"data"`
	}
	json.Unmarshal(baseline, &result)
	canonical, _ := json.Marshal(core.Capabilities())
	var got, want any
	json.Unmarshal(result.Data, &got)
	json.Unmarshal(canonical, &want)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("HTTP capabilities diverged from core schemas")
	}
	if !strings.Contains(string(baseline), `"name":"posts.publish"`) || !strings.Contains(string(baseline), `"expected_revision"`) {
		t.Fatal("protocol identifiers missing")
	}
}

func TestSystemInfoLocalizesOnlyHumanReason(t *testing.T) {
	srv, h := testServer(t)
	read := func(locale string) map[string]any {
		t.Helper()
		w := request(h, "POST", "/api/op/system.info", readToken, `{}`, map[string]string{"Accept-Language": locale})
		if w.Code != 200 {
			t.Fatalf("info failed: %s", w.Body.String())
		}
		var out struct {
			Data map[string]any `json:"data"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out.Data
	}
	en, zh := read(i18n.English), read(i18n.Chinese)
	englishAI := en["ai"].(map[string]any)
	chineseAI := zh["ai"].(map[string]any)
	englishReason := englishAI["reason"].(string)
	chineseReason := chineseAI["reason"].(string)
	if !i18n.Known(englishReason) || chineseReason == englishReason || chineseReason != i18n.Message(i18n.Chinese, englishReason) {
		t.Fatalf("AI availability reason not correctly localized: en=%q zh=%q", englishReason, chineseReason)
	}
	chineseAI["reason"] = englishReason
	if !reflect.DeepEqual(en, zh) {
		t.Fatalf("locale changed system.info protocol data beyond reason: en=%v zh=%v", en, zh)
	}
	direct, e := srv.Store.Call(context.Background(), "system.info", json.RawMessage(`{}`), "test")
	if e != nil {
		t.Fatal(e)
	}
	if direct.(map[string]any)["ai"].(map[string]any)["reason"] != englishReason {
		t.Fatal("HTTP localization mutated canonical core text")
	}
}

func TestDocumentLanguageUsesCookieAndPreservesAuthoredContent(t *testing.T) {
	srv, _ := testServer(t)
	srv.Assets = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte(`<!doctype html><html lang="en"><head><title>FOLIO</title></head><body><div id="app"></div></body></html>`)}}
	h := srv.Handler()
	operation(t, h, "settings.update", adminToken, map[string]any{"expected_revision": 1, "settings": core.Settings{Title: "A valid Bearer token is required", Description: "User-owned description / 用户描述", Author: "Author 作者", BaseURL: "https://blog.example"}})
	p := postResult(t, operation(t, h, "posts.create", draftToken, map[string]any{"title": "That page is not here", "slug": "user-content", "excerpt": "Post not found", "markdown": "# Not found\n\nThese are the author's English words. 用户自己的内容。"}))
	operation(t, h, "posts.publish", adminToken, lifecycleAction(p))
	cases := []struct{ name, cookie, accept, explicit, want string }{{"Chinese default despite browser English", "", "en", "", i18n.Chinese}, {"English saved preference", "en", "zh-CN", "zh-CN", i18n.English}, {"Chinese saved preference", "zh-CN", "en", "en", i18n.Chinese}, {"unsupported preference falls back", "fr", "en", "en", i18n.Chinese}}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{"Accept-Language": tt.accept, "X-Folio-Lang": tt.explicit}
			if tt.cookie != "" {
				headers["Cookie"] = "folio.locale=" + tt.cookie
			}
			for _, path := range []string{"/", "/posts/user-content", "/studio"} {
				w := request(h, "GET", path, "", "", headers)
				if w.Code != 200 {
					t.Fatalf("page %s failed: %d", path, w.Code)
				}
				if !strings.Contains(w.Body.String(), `<html lang="`+tt.want+`">`) || w.Header().Get("Content-Language") != tt.want {
					t.Errorf("%s wrong document language: %s", path, w.Body.String())
				}
				if path != "/studio" && !strings.Contains(w.Body.String(), "A valid Bearer token is required") {
					t.Error("authored site title was translated")
				}
				if path == "/posts/user-content" {
					for _, content := range []string{"That page is not here", "Post not found", "These are the author's English words.", "用户自己的内容。"} {
						if !strings.Contains(html.UnescapeString(w.Body.String()), content) {
							t.Errorf("authored content changed: %q", content)
						}
					}
				}
			}
			w := request(h, "GET", "/posts/missing", "", "", headers)
			if w.Code != 404 || !strings.Contains(w.Body.String(), i18n.Message(tt.want, "That page is not here")) {
				t.Errorf("404 UI copy not localized: %d %s", w.Code, w.Body.String())
			}
		})
	}
	english := request(h, "GET", "/api/public/site", "", "", map[string]string{"Accept-Language": "en"})
	chinese := request(h, "GET", "/api/public/site", "", "", map[string]string{"Accept-Language": "zh-CN"})
	if !bytes.Equal(english.Body.Bytes(), chinese.Body.Bytes()) {
		t.Fatal("locale translated public authored JSON content")
	}
}

func TestIndexHTMLRedirectDoesNotBypassLocaleSelection(t *testing.T) {
	_, h := testServer(t)
	w := request(h, "GET", "/index.html", "", "", map[string]string{"Cookie": "folio.locale=en", "Accept-Language": "zh-CN"})
	if w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "/" {
		t.Fatalf("direct index should redirect to locale-aware page: %d location=%q", w.Code, w.Header().Get("Location"))
	}
}
