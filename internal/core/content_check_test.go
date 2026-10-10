package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestContentCheckRoutingAndLocations(t *testing.T) {
	live := Post{ID: "live", Slug: "live"}
	renamed := Post{ID: "renamed", Slug: "new-name"}
	state := freshState()
	state.Settings.BaseURL = "https://blog.example"
	state.Posts = map[string]*Record{
		"live":    {Draft: Post{ID: "live", Slug: "private-change"}, Live: &live},
		"renamed": {Draft: renamed, Live: &renamed, OldSlugs: []string{"old-name"}},
		"private": {Draft: Post{ID: "private", Slug: "private"}},
		"deleted": {Draft: Post{ID: "deleted", Slug: "deleted"}, Live: &Post{Slug: "deleted"}, Deleted: true},
	}
	state.Media["good.png"] = Media{ID: "good.png"}
	p := Post{ID: "source", Slug: "source", Revision: 7, Excerpt: "", Cover: "/media/absent-cover.png"}
	state.Posts[p.ID] = &Record{Draft: p}
	p.Markdown = strings.Join([]string{
		"# Fictional source", "", "[live](/posts/live) [redirect](/posts/old-name)",
		"[relative](live) [relative parent](../posts/live?x=1#part)",
		"[origin](https://BLOG.example:443/posts/live) [escaped](/posts/%6cive)",
		"[self](/posts/source#future) [anchor](#missing) [query](?view=1)",
		"[private](/posts/private) [new draft URL](/posts/private-change)",
		"[missing](/posts/absent) [deleted](/posts/deleted)",
		"![exists](/media/good.png) ![missing](/media/absent.png)",
		"[ref][broken]\n\n[broken]: /posts/reference-missing", "",
		"[external](https://other.example/posts/absent) <https://other.example/media/absent.png>",
		"[email](mailto:a@example.com) [unsupported](/settings)",
		"[slash](/posts/live/) [encoded slash](/posts/live%2Fother) [dot](/posts/%2e%2e/posts/live)",
		"`[inline code](/posts/code-missing)`", "```md\n[code](/posts/fenced-missing)\n```",
		"<a href=\"/posts/html-missing\">raw HTML</a>",
	}, "\n")
	out := analyzeContent(p, state, "instance")
	want := map[string]int{"article_private": 2, "article_missing": 3, "media_missing": 2, "excerpt_missing": 1, "reference_unchecked": 3}
	got := map[string]int{}
	for _, f := range out.Findings {
		got[f.Code]++
		if f.Field == "markdown" && (f.Line < 1 || f.Precision != "line") {
			t.Errorf("location %+v", f)
		}
		for _, excluded := range []string{"code-missing", "fenced-missing", "html-missing", "other.example"} {
			if strings.Contains(f.Reference, excluded) {
				t.Errorf("false positive %+v", f)
			}
		}
		if f.Reference == "/posts/absent" && f.Line != 8 {
			t.Errorf("wrong exact line: %+v", f)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findings = %#v want %#v; %+v", got, want, out.Findings)
	}
	if out.ID != p.ID || out.Revision != 7 || out.InstanceRevision != state.Revision || out.InstanceID != "instance" || out.CanonicalURL != "https://blog.example/posts/source" || out.Truncated {
		t.Fatalf("bad snapshot %+v", out)
	}
	if out.Counts["external_urls_not_checked"] != 2 || out.Counts["self_urls_assumed_after_publish"] != 3 {
		t.Fatalf("bad counters %+v", out.Counts)
	}
}

func TestContentCheckReadOnlyRevisionAndStrictInput(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "source")
	before, _ := json.Marshal(s.State)
	for i := 0; i < 2; i++ {
		result := call(t, s, "posts.check", map[string]any{"id": p.ID, "revision": p.Revision}).(ContentCheck)
		if result.Revision != p.Revision {
			t.Fatal("wrong revision")
		}
	}
	after, _ := json.Marshal(s.State)
	if string(before) != string(after) {
		t.Fatal("check changed persistent state, audit, retry ledger or revisions")
	}
	for _, args := range []map[string]any{{"id": p.ID}, {"id": p.ID, "revision": 0}, {"revision": 1}, {"id": p.ID, "revision": 1, "markdown": "probe"}, {"id": p.ID, "revision": 1, "idempotency_key": "no-write"}} {
		callError(t, s, "posts.check", args, "validation")
	}
	callError(t, s, "posts.check", map[string]any{"id": "missing", "revision": 1}, "not_found")
	changed := getPost(t, call(t, s, "posts.update", edited(p)))
	callError(t, s, "posts.check", map[string]any{"id": p.ID, "revision": p.Revision}, "conflict")
	call(t, s, "posts.check", map[string]any{"id": p.ID, "revision": changed.Revision})
	call(t, s, "posts.delete", action(changed, true))
	callError(t, s, "posts.check", map[string]any{"id": p.ID, "revision": changed.Revision}, "not_found")
	spec := Specs["posts.check"]
	if !spec.ReadOnly || spec.Scope != "read" {
		t.Fatal("operation is not read-only")
	}
}

func TestContentCheckBoundsAndLiteralEvidence(t *testing.T) {
	state := freshState()
	p := Post{ID: "p", Slug: "p", Revision: 1, Excerpt: "summary"}
	for _, count := range []int{checkFindingLimit, checkFindingLimit + 1, checkReferenceLimit, checkReferenceLimit + 1} {
		p.Markdown = strings.Repeat("[missing](/posts/missing)\n", count)
		out := analyzeContent(p, state, "i")
		if len(out.Findings) != min(count, checkFindingLimit) || out.Truncated != (count > checkFindingLimit) || out.Counts["references"] != min(count, checkReferenceLimit) {
			t.Fatalf("bounds count=%d got %+v", count, out.Counts)
		}
	}
	p.Markdown = strings.Repeat("[external](https://external.example/)\n", checkReferenceLimit)
	if out := analyzeContent(p, state, "i"); out.Truncated {
		t.Fatal("exactly 2000 references marked truncated")
	}
	p.Markdown = "[missing](/posts/" + strings.Repeat("a", 4500) + ")\n![alt [not-link](/posts/absent)](/media/good.png)"
	state.Media["good.png"] = Media{ID: "good.png"}
	out := analyzeContent(p, state, "i")
	if len(out.Findings) != 1 || out.Findings[0].Code != "reference_unchecked" || len(out.Findings[0].Reference) > 515 {
		t.Fatalf("unbounded or false image-alt findings %+v", out.Findings)
	}
	for i, f := range out.Findings {
		if f.Message == "" {
			t.Fatal(fmt.Sprint("missing message ", i))
		}
	}
}
