package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testToken = "adapter-test-token-do-not-log"

var testOperations = []Operation{
	{Name: "system.capabilities", Description: "Discover the canonical operation registry", InputSchema: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`), ReadOnly: true},
	{Name: "posts.list", Description: "List posts", InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1}},"additionalProperties":false}`), ReadOnly: true},
	{Name: "posts.publish", Description: "Publish an explicitly approved draft", InputSchema: json.RawMessage(`{"type":"object","required":["id","expected_revision","confirm"],"properties":{"id":{"type":"string"},"expected_revision":{"type":"integer"},"confirm":{"const":true}},"additionalProperties":false}`), ReadOnly: false},
}

func daemonFixture(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", r.Method)
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/op/")
		if name != "system.capabilities" && r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"ok":false,"error":{"code":"unauthorized","message":"Not authorized"}}`)
			return
		}
		var args map[string]any
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("invalid request JSON: %v", err)
		}
		switch name {
		case "system.capabilities":
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": Capabilities{Version: "test", Operations: testOperations}})
		case "posts.publish":
			if args["confirm"] != true {
				w.WriteHeader(http.StatusConflict)
				io.WriteString(w, `{"ok":false,"error":{"code":"confirmation_required","message":"Publishing requires confirm:true","details":{"field":"confirm"}}}`)
				return
			}
			fallthrough
		case "posts.list":
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]any{"operation": name, "arguments": args}})
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"ok":false,"error":{"code":"unknown_operation","message":"Operation not found"}}`)
		}
	}))
}

func TestCLIJSONContract(t *testing.T) {
	daemon := daemonFixture(t)
	defer daemon.Close()
	t.Setenv("FOLIO_URL", daemon.URL)
	t.Setenv("FOLIO_TOKEN", testToken)
	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantErr  bool
		contains string
	}{
		{"capabilities", []string{"capabilities"}, "", false, `"input_schema"`},
		{"default input", []string{"call", "posts.list"}, "", false, `"arguments":{}`},
		{"json input", []string{"call", "posts.list", "--json", `{"limit":2}`}, "", false, `"limit":2`},
		{"stdin input", []string{"call", "posts.list", "--file", "-"}, `{"limit":3}`, false, `"limit":3`},
		{"malformed input", []string{"call", "posts.list", "--json", `{`}, "", true, `"invalid_json"`},
		{"array input", []string{"call", "posts.list", "--json", `[]`}, "", true, `"invalid_json"`},
		{"null input", []string{"call", "posts.list", "--json", `null`}, "", true, `"invalid_json"`},
		{"empty input", []string{"call", "posts.list", "--json", ``}, "", true, `"invalid_json"`},
		{"extra JSON", []string{"call", "posts.list", "--json", `{} {}`}, "", true, `"invalid_json"`},
		{"bad operation", []string{"call", "../health"}, "", true, `"invalid_operation"`},
		{"missing operation", []string{"call"}, "", true, `"usage_error"`},
		{"extra argument", []string{"capabilities", "ignored"}, "", true, `"usage_error"`},
		{"two sources", []string{"call", "posts.list", "--json", "{}", "--file", "-"}, "", true, `"usage_error"`},
		{"unknown command", []string{"wat"}, "", true, `"usage_error"`},
		{"missing file", []string{"call", "posts.list", "--file", "/no-such-folio-test-file"}, "", true, `"input_read_failed"`},
		{"business error", []string{"call", "posts.publish", "--json", `{"id":"p1","expected_revision":1}`}, "", true, `"details":{"field":"confirm"}`},
		{"publish", []string{"call", "posts.publish", "--json", `{"id":"p1","expected_revision":1,"confirm":true}`}, "", false, `"confirm":true`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := RunCLI(context.Background(), tt.args, strings.NewReader(tt.stdin), &stdout, &stderr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error=%v wantErr=%v output=%s", err, tt.wantErr, stdout.String())
			}
			if !json.Valid(stdout.Bytes()) {
				t.Fatalf("stdout is not exactly one JSON value: %q", stdout.String())
			}
			if !strings.Contains(stdout.String(), tt.contains) {
				t.Errorf("stdout=%s; want %s", stdout.String(), tt.contains)
			}
			if stderr.Len() != 0 {
				t.Errorf("unexpected stderr=%s", stderr.String())
			}
			if strings.Contains(stdout.String(), testToken) {
				t.Error("token leaked in stdout")
			}
		})
	}
}

func TestCLIFileAndHelp(t *testing.T) {
	t.Setenv("FOLIO_LANG", "en")
	daemon := daemonFixture(t)
	defer daemon.Close()
	t.Setenv("FOLIO_URL", daemon.URL)
	t.Setenv("FOLIO_TOKEN", testToken)
	path := t.TempDir() + "/input.json"
	if err := os.WriteFile(path, []byte(`{"limit":5}`), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := RunCLI(context.Background(), []string{"call", "posts.list", "--file", path}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"limit":5`) {
		t.Fatal(stdout.String())
	}
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		stdout.Reset()
		stderr.Reset()
		if err := RunCLI(context.Background(), args, strings.NewReader(""), &stdout, &stderr); err != nil {
			t.Fatal(err)
		}
		if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	}
}

func TestAuthenticationAndDiscovery(t *testing.T) {
	daemon := daemonFixture(t)
	defer daemon.Close()
	client, err := NewClient(daemon.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Capabilities(context.Background()); err != nil {
		t.Fatalf("public discovery: %v", err)
	}
	if _, err = client.Call(context.Background(), "posts.list", nil); err == nil || !strings.Contains(err.Error(), "FOLIO_TOKEN") {
		t.Fatalf("missing-token error=%v", err)
	}
	client, err = NewClient(daemon.URL, "wrong-token")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.Call(context.Background(), "posts.list", nil)
	if err == nil || !strings.Contains(err.Error(), "authentication_failed") || raw != nil {
		t.Fatalf("raw=%s error=%v", raw, err)
	}
	if strings.Contains(err.Error(), "wrong-token") {
		t.Fatal("token leaked")
	}
}

func TestClientSafety(t *testing.T) {
	for _, raw := range []string{"file:///tmp/folio", "http://example.com", "https://user:secret@example.com", "https://example.com?token=secret", "https://example.com#token"} {
		if _, err := NewClient(raw, "secret"); err == nil {
			t.Errorf("unsafe URL accepted: %s", raw)
		}
	}
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); io.WriteString(w, `{"ok":true}`) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client, _ := NewClient(redirect.URL, testToken)
	if _, err := client.Call(context.Background(), "posts.list", nil); err == nil || !strings.Contains(err.Error(), "redirect_refused") {
		t.Fatalf("redirect error=%v", err)
	}
	if targetCalls.Load() != 0 {
		t.Fatal("followed redirect")
	}
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "<html>not a daemon</html>") }))
	defer invalid.Close()
	client, _ = NewClient(invalid.URL, testToken)
	if _, err := client.Call(context.Background(), "posts.list", nil); err == nil || !strings.Contains(err.Error(), "invalid_response") {
		t.Fatalf("invalid response error=%v", err)
	}
	offline := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	offline.Close()
	client, _ = NewClient(offline.URL, testToken)
	if _, err := client.Call(context.Background(), "posts.list", nil); err == nil || !strings.Contains(err.Error(), "folio serve") || strings.Contains(err.Error(), testToken) {
		t.Fatalf("offline error=%v", err)
	}
}

// TestMCPStdio performs a real official-SDK initialize handshake, tools/list and
// tools/call against a child process over OS stdin/stdout pipes. It verifies
// schemas, auth, operation argument parity and structured tool error semantics.
func TestMCPStdio(t *testing.T) {
	daemon := daemonFixture(t)
	defer daemon.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMCPStdioHelper$")
	cmd.Env = append(os.Environ(), "FOLIO_MCP_TEST_HELPER=1", "FOLIO_URL="+daemon.URL, "FOLIO_TOKEN="+testToken)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("MCP process: %v; stderr=%s", err, stderr.String())
		}
	}()
	client := mcp.NewClient(&mcp.Implementation{Name: "folio-integration-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.IOTransport{Reader: stdout, Writer: stdin}, nil)
	if err != nil {
		t.Fatalf("initialize failed: %v", err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(listed.Tools) != len(testOperations) {
		t.Fatalf("tools=%d want=%d", len(listed.Tools), len(testOperations))
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}
	for _, op := range testOperations {
		tool := byName[strings.ReplaceAll(op.Name, ".", "_")]
		if tool == nil {
			t.Fatalf("tool missing: %s", op.Name)
		}
		var expected any
		json.Unmarshal(op.InputSchema, &expected)
		if !reflect.DeepEqual(tool.InputSchema, expected) {
			t.Errorf("%s schema differs: %#v", op.Name, tool.InputSchema)
		}
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != op.ReadOnly {
			t.Errorf("%s annotations=%#v", op.Name, tool.Annotations)
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "posts_list", Arguments: map[string]any{"limit": 7}})
	if err != nil || result.IsError {
		t.Fatalf("tools/call: result=%#v error=%v", result, err)
	}
	raw, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(raw), `"limit":7`) || !strings.Contains(string(raw), `"operation":"posts.list"`) {
		t.Fatalf("lost call arguments: %s", raw)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected JSON text fallback, got %#v", result.Content)
	}
	failure, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "posts_publish", Arguments: map[string]any{"id": "p1", "expected_revision": 1}})
	if err != nil || !failure.IsError {
		t.Fatalf("expected tool error result; result=%#v error=%v", failure, err)
	}
	raw, _ = json.Marshal(failure.StructuredContent)
	if !strings.Contains(string(raw), "confirmation_required") || !strings.Contains(string(raw), `"details"`) {
		t.Fatalf("lost structured error: %s", raw)
	}
	published, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "posts_publish", Arguments: map[string]any{"id": "p1", "expected_revision": 1, "confirm": true}})
	if err != nil || published.IsError {
		t.Fatalf("publish result=%#v error=%v", published, err)
	}
}

func TestMCPStdioHelper(t *testing.T) {
	if os.Getenv("FOLIO_MCP_TEST_HELPER") != "1" {
		return
	}
	if err := RunMCP(context.Background()); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(0)
}

func TestCLIHealthcheck(t *testing.T) {
	t.Setenv("FOLIO_TOKEN", testToken)
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{"healthy", 200, `{"status":"ok","version":"test"}`, false},
		{"unhealthy HTTP", 503, `{"status":"ok"}`, true},
		{"unhealthy body", 200, `{"status":"down"}`, true},
		{"not JSON", 200, `unhealthy`, true},
		{"not an object", 200, `[]`, true},
		{"redirect", 302, `{"status":"ok"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method=%s", r.Method)
				}
				if r.URL.Path != "/healthz" {
					t.Errorf("path=%s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("healthcheck must not send token")
				}
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			defer server.Close()
			t.Setenv("FOLIO_URL", server.URL)
			for _, args := range [][]string{{"healthcheck"}, {"healthcheck", "--url", server.URL + "/healthz"}} {
				var out, stderr bytes.Buffer
				err := RunCLI(context.Background(), args, strings.NewReader(""), &out, &stderr)
				if (err != nil) != tt.wantErr {
					t.Errorf("error=%v wantErr=%v", err, tt.wantErr)
				}
				if !json.Valid(out.Bytes()) {
					t.Errorf("invalid JSON=%s", out.String())
				}
				if strings.Contains(out.String(), testToken) {
					t.Error("token leaked")
				}
				if !tt.wantErr && !strings.Contains(out.String(), `"status":"ok"`) {
					t.Errorf("output=%s", out.String())
				}
			}
		})
	}
	for _, args := range [][]string{{"healthcheck", "--url"}, {"healthcheck", "wat"}, {"healthcheck", "--url", ""}} {
		var out, stderr bytes.Buffer
		if err := RunCLI(context.Background(), args, strings.NewReader(""), &out, &stderr); err == nil {
			t.Errorf("invalid arguments accepted: %v", args)
		}
	}
}

func TestHealthcheckCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := healthCheck(ctx, server.URL+"/healthz"); err == nil {
		t.Fatal("expected a canceled healthcheck to fail")
	}
}
