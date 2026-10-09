package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestLocaleAliasesAndFallback(t *testing.T) {
	for _, tt := range []struct {
		input, want string
		supported   bool
	}{
		{"zh-CN", Chinese, true}, {"zh", Chinese, true}, {"ZH_cn", Chinese, true}, {"zh-Hans", Chinese, true}, {"zh-Hans-CN", Chinese, true}, {" en_US ", English, true}, {"en", English, true}, {"en-GB", English, true}, {"EN-au", English, true}, {"", Chinese, false}, {"fr-FR", Chinese, false}, {"zh-TW", Chinese, false}, {"not-a-locale", Chinese, false},
	} {
		t.Run(tt.input, func(t *testing.T) {
			if got := Resolve(tt.input); got != tt.want {
				t.Errorf("Resolve(%q)=%q, want %q", tt.input, got, tt.want)
			}
			locale, ok := Supported(tt.input)
			if ok != tt.supported {
				t.Errorf("Supported(%q)=%q,%v", tt.input, locale, ok)
			}
			if ok && locale != tt.want {
				t.Errorf("alias resolved to %q", locale)
			}
		})
	}
}

func TestAcceptLanguageQualityAndStablePriority(t *testing.T) {
	for _, tt := range []struct{ header, want string }{
		{"", Chinese}, {"fr-FR,de;q=0.8", Chinese}, {"en-US,en;q=0.9,zh-CN;q=0.5", English}, {"en;q=0.3,zh-CN;q=0.9", Chinese}, {"zh-CN;q=0.2,en-GB;q=0.8", English}, {"en;q=0.7,zh;q=0.7", English}, {"zh;q=0.7,en;q=0.7", Chinese}, {"en;q=0,zh;q=0.2", Chinese}, {"fr;q=1,en;q=0.4", English}, {"en;q=bogus,zh;q=0.5", Chinese}, {"en;q=1.2,zh;q=0.5", Chinese}, {"zh;q=-1,en;q=0.2", English}, {"en;q=NaN,zh;q=0.5", Chinese}, {"en;q=+Inf,zh;q=0.5", Chinese}, {"*;q=1,en;q=0.1", English},
	} {
		if got := AcceptLanguage(tt.header); got != tt.want {
			t.Errorf("AcceptLanguage(%q)=%q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestRequestAndCookieLocalePrecedence(t *testing.T) {
	for _, tt := range []struct{ name, cookie, accept, explicit, request, cookieResult string }{
		{"default", "", "", "", Chinese, Chinese}, {"cookie", "en", "", "", English, English}, {"header overrides cookie", "zh-CN", "en", "", English, Chinese}, {"explicit overrides header", "en", "en", "zh-CN", Chinese, English}, {"unsupported explicit falls back", "en", "en", "fr", Chinese, English}, {"unsupported cookie", "fr", "", "", Chinese, Chinese}, {"header alias", "", "en-AU", "", English, Chinese},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://example.com", nil)
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: "folio.locale", Value: tt.cookie})
			}
			r.Header.Set("Accept-Language", tt.accept)
			r.Header.Set("X-Folio-Lang", tt.explicit)
			if got := RequestLocale(r); got != tt.request {
				t.Errorf("request locale=%q, want %q", got, tt.request)
			}
			if got := CookieLocale(r); got != tt.cookieResult {
				t.Errorf("document locale=%q, want %q", got, tt.cookieResult)
			}
		})
	}
}

func TestMessageFormattingPreservesProtocolArguments(t *testing.T) {
	template := "Revision conflict: expected %d, current %d. Fetch before retrying."
	if !Known(template) {
		t.Fatal("dynamic conflict template absent")
	}
	if got := Format(English, template, 7, 11); got != "Revision conflict: expected 7, current 11. Fetch before retrying." {
		t.Fatalf("English format changed: %q", got)
	}
	if got := Format(Chinese, template, 7, 11); got != "版本冲突：预期版本为 7，当前版本为 11。请获取最新版本后再重试。" {
		t.Fatalf("Chinese format wrong: %q", got)
	}
	got := Format(Chinese, "This token cannot perform %s; a publisher/admin token is required", "posts.publish")
	if !strings.Contains(got, "posts.publish") || strings.Contains(got, "%!") {
		t.Fatalf("operation identifier not preserved: %q", got)
	}
	unknown := "User authored content MUST remain unchanged / 用户内容"
	if Known(unknown) || Message(Chinese, unknown) != unknown || Message(English, unknown) != unknown {
		t.Fatal("unknown/user text was translated")
	}
}

func TestTranslationFormatSpecifiersMatch(t *testing.T) {
	verbs := regexp.MustCompile(`%(?:\[[0-9]+\])?[+#\-. 0-9]*[a-zA-Z]`)
	for canonical, translated := range messages {
		if strings.TrimSpace(translated) == "" {
			t.Errorf("empty translation for %q", canonical)
		}
		englishVerbs := verbs.FindAllString(canonical, -1)
		chineseVerbs := verbs.FindAllString(translated, -1)
		if !reflect.DeepEqual(englishVerbs, chineseVerbs) {
			t.Errorf("format placeholder mismatch %q => %q: %v vs %v", canonical, translated, englishVerbs, chineseVerbs)
		}
		if Message(English, canonical) != canonical {
			t.Errorf("English canonical text changed: %q", canonical)
		}
	}
}

func TestCoreAndHTTPErrorTemplatesHaveTranslations(t *testing.T) {
	// Parse source rather than enumerate a stale list. This catches new operation
	// errors which would otherwise silently fall back to English in a Chinese UI.
	files, e := filepath.Glob(filepath.Join("..", "core", "*.go"))
	if e != nil {
		t.Fatal(e)
	}
	files = append(files, filepath.Join("..", "server", "server.go"))
	seen := map[string]bool{}
	missing := map[string]string{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		tree, e := parser.ParseFile(fset, file, nil, 0)
		if e != nil {
			t.Fatal(e)
		}
		ast.Inspect(tree, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			isErr := false
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				isErr = fun.Name == "Err"
			case *ast.SelectorExpr:
				if pkg, ok := fun.X.(*ast.Ident); ok {
					isErr = pkg.Name == "core" && fun.Sel.Name == "Err"
				}
			}
			if !isErr {
				return true
			}
			literal, ok := call.Args[1].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Errorf("error template is not a string literal: %s", fset.Position(call.Pos()))
				return true
			}
			template, e := strconv.Unquote(literal.Value)
			if e != nil {
				t.Error(e)
				return true
			}
			seen[template] = true
			if !Known(template) {
				missing[template] = fset.Position(literal.Pos()).String()
			}
			return true
		})
	}
	if len(seen) < 40 {
		t.Fatalf("catalog coverage scan unexpectedly found only %d templates", len(seen))
	}
	keys := make([]string, 0, len(missing))
	for template := range missing {
		keys = append(keys, template)
	}
	sort.Strings(keys)
	for _, template := range keys {
		t.Errorf("missing Chinese translation: %q (%s)", template, missing[template])
	}
}
