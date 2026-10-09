package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"folio/internal/i18n"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCLIHelpLanguages(t *testing.T) {
	for _, tt := range []struct {
		name, env    string
		args         []string
		want, absent string
	}{
		{"default Chinese", "", []string{"--help"}, "用法：", "Usage:"},
		{"Chinese environment", "zh-CN", []string{"help"}, "用法：", "Usage:"},
		{"English environment", "en", []string{"help"}, "Usage:", "用法："},
		{"invalid environment fallback", "not-a-locale", []string{"help"}, "用法：", "Usage:"},
		{"flag overrides environment", "zh-CN", []string{"--lang", "en", "help"}, "Usage:", "用法："},
		{"equals flag overrides environment", "en", []string{"--lang=zh-CN", "help"}, "用法：", "Usage:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("FOLIO_LANG", tt.env)
			var out, stderr bytes.Buffer
			if err := RunCLI(context.Background(), tt.args, strings.NewReader(""), &out, &stderr); err != nil {
				t.Fatal(err)
			}
			if out.Len() != 0 || !strings.Contains(stderr.String(), tt.want) || strings.Contains(stderr.String(), tt.absent) {
				t.Fatalf("stdout=%q stderr=%q", out.String(), stderr.String())
			}
			for _, stable := range []string{"posts.list", "expected_revision", "confirm:true", "FOLIO_LANG"} {
				if !strings.Contains(stderr.String(), stable) {
					t.Errorf("help is missing stable identifier %s", stable)
				}
			}
		})
	}
}

func TestParseGlobalLanguage(t *testing.T) {
	t.Setenv("FOLIO_LANG", "zh-CN")
	locale, args, err := ParseGlobalLanguage([]string{"--lang", "en", "serve", "--data", "data"})
	if err != nil || locale != "en" || !reflect.DeepEqual(args, []string{"serve", "--data", "data"}) {
		t.Fatalf("locale=%s args=%v err=%v", locale, args, err)
	}
	for _, input := range [][]string{{"--lang"}, {"--lang", "fr", "help"}, {"--lang="}, {"--lang=EN"}} {
		_, _, err := ParseGlobalLanguage(input)
		api, ok := err.(*APIError)
		if !ok || api.Code != "invalid_locale" || !strings.Contains(api.Message, "语言") {
			t.Errorf("input=%v error=%v", input, err)
		}
	}
	_, _, err = ParseGlobalLanguage([]string{"--lang=en", "--lang=zh-CN", "help"})
	if err == nil || !strings.Contains(err.Error(), "usage_error") {
		t.Fatalf("duplicate flag error=%v", err)
	}
	// The parser must not reinterpret JSON/filename arguments after a command.
	original := []string{"call", "posts.list", "--json", `{"query":"--lang=en"}`}
	_, args, err = ParseGlobalLanguage(original)
	if err != nil || !reflect.DeepEqual(args, original) {
		t.Fatalf("command arguments changed: %v, %v", args, err)
	}
}

func TestCLILocalizedErrorsStableContract(t *testing.T) {
	t.Setenv("FOLIO_URL", DefaultURL)
	t.Setenv("FOLIO_TOKEN", "")
	for _, locale := range []string{"zh-CN", "en"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("FOLIO_LANG", locale)
			var out, stderr bytes.Buffer
			err := RunCLI(context.Background(), []string{"call", "posts.list", "--json", "[]"}, strings.NewReader(""), &out, &stderr)
			if err == nil {
				t.Fatal("expected validation failure")
			}
			var result map[string]json.RawMessage
			if json.Unmarshal(out.Bytes(), &result) != nil || len(result) != 2 || result["ok"] == nil || result["error"] == nil {
				t.Fatalf("machine envelope changed: %s", out.String())
			}
			var detail APIError
			json.Unmarshal(result["error"], &detail)
			want := i18n.CLIMessage(locale, "Operation input must be one valid JSON object")
			if detail.Code != "invalid_json" || detail.Message != want {
				t.Fatalf("detail=%+v want message=%s", detail, want)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("stderr return not localized: %v", err)
			}
			out.Reset()
			err = RunCLI(context.Background(), []string{"--lang", "not-supported", "help"}, strings.NewReader(""), &out, &stderr)
			if err == nil || !bytes.Contains(out.Bytes(), []byte(`"code":"invalid_locale"`)) {
				t.Fatalf("invalid locale contract: %s %v", out.String(), err)
			}
		})
	}
}

func TestLanguageHeaderAndSuccessPreservation(t *testing.T) {
	languages := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		languages <- r.Header.Get("Accept-Language")
		io.WriteString(w, `{"ok":true,"data":{"status":"draft","title":"我的 English 原文","message":"User-authored text is not an error"}}`)
	}))
	defer server.Close()
	t.Setenv("FOLIO_URL", server.URL)
	t.Setenv("FOLIO_TOKEN", testToken)
	t.Setenv("FOLIO_LANG", "zh-CN")
	for _, locale := range []string{"zh-CN", "en"} {
		var out, stderr bytes.Buffer
		if err := RunCLI(context.Background(), []string{"--lang", locale, "call", "posts.list"}, strings.NewReader(""), &out, &stderr); err != nil {
			t.Fatal(err)
		}
		if got := <-languages; got != locale {
			t.Errorf("Accept-Language=%q want=%q", got, locale)
		}
		var result struct {
			Data map[string]string `json:"data"`
		}
		json.Unmarshal(out.Bytes(), &result)
		if result.Data["status"] != "draft" || result.Data["title"] != "我的 English 原文" || result.Data["message"] != "User-authored text is not an error" {
			t.Errorf("success content translated: %s", out.String())
		}
	}
	// A context supplied by main takes precedence over environment after stripping
	// the global flag, without mutating FOLIO_LANG for later invocations.
	var out, stderr bytes.Buffer
	if err := RunCLI(WithLanguage(context.Background(), "en"), []string{"call", "posts.list"}, strings.NewReader(""), &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if got := <-languages; got != "en" {
		t.Fatal("main's context locale was not preserved")
	}
}

func TestMCPDiscoveryCanonicalErrorsLocalized(t *testing.T) {
	var mu sync.Mutex
	headers := map[string]string{}
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := strings.TrimPrefix(r.URL.Path, "/api/op/")
		mu.Lock()
		headers[op] = r.Header.Get("Accept-Language")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if op == "system.capabilities" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": Capabilities{Version: "test", Operations: testOperations}})
			return
		}
		w.WriteHeader(400)
		io.WriteString(w, `{"ok":false,"error":{"code":"invalid_json","message":"Operation input must be one valid JSON object","details":{"original":"Leave my English text alone","counter":9007199254740993}}}`)
	}))
	defer daemon.Close()
	client, err := NewClientWithLanguage(daemon.URL, testToken, "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewMCPServer(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "locale-test", Version: "1"}, nil)
	session, err := mcpClient.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	for _, operation := range testOperations {
		tool := byName[strings.ReplaceAll(operation.Name, ".", "_")]
		if tool == nil || tool.Description != operation.Description {
			t.Fatalf("canonical English tool metadata changed: %+v", tool)
		}
		var expected any
		json.Unmarshal(operation.InputSchema, &expected)
		if !reflect.DeepEqual(tool.InputSchema, expected) {
			t.Errorf("schema translated for %s", operation.Name)
		}
	}
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "posts_list", Arguments: map[string]any{}})
	if err != nil || !result.IsError {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "操作输入必须") || !strings.Contains(text, `"code":"invalid_json"`) || !strings.Contains(text, "Leave my English text alone") || !strings.Contains(text, "9007199254740993") {
		t.Fatalf("localized error damaged machine/detail data: %s", text)
	}
	mu.Lock()
	defer mu.Unlock()
	if headers["system.capabilities"] != "en" || headers["posts.list"] != "zh-CN" {
		t.Fatalf("wrong locale headers: %v", headers)
	}
}

func TestHealthcheckLanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Language") != "en" {
			t.Errorf("health locale=%q", r.Header.Get("Accept-Language"))
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	_, err := healthCheck(WithLanguage(context.Background(), "en"), server.URL+"/healthz")
	if err == nil || !strings.Contains(err.Error(), "Health endpoint did not return HTTP 200") {
		t.Fatalf("health error=%v", err)
	}
}
