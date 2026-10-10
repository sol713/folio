package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestComparisonJSONStringCountingAndAllFields(t *testing.T) {
	for _, value := range []string{"", "plain / text", "\x00\b\f\n\r\t\"\\<>&", "中文😀\u2028\u2029", string([]byte{0xff, 0xfe})} {
		raw, _ := json.Marshal(value)
		if got := jsonStringBound(value, 1<<20); got < int64(len(raw)) || utf8.ValidString(value) && got != int64(len(raw)) {
			t.Fatalf("JSON byte count %d != %d", got, len(raw))
		}
		if jsonStringBound(value, 0) != 1 {
			t.Fatal("counter did not saturate")
		}
	}
	// All eight fields are counted; booleans/keys/array punctuation have a fixed
	// allowance. Escaping can make tiny raw values cost six times as much.
	small := ProposalContent{Title: "Title", Slug: "slug", Markdown: "中文", Tags: []string{"topic"}, Featured: true}
	if !comparisonWithinBudget(small, small) {
		t.Fatal("ordinary content rejected")
	}
	for _, field := range []string{"title", "slug", "markdown", "excerpt", "tags", "category", "cover"} {
		p := small
		huge := strings.Repeat("\x00", 4*1024*1024)
		switch field {
		case "title":
			p.Title = huge
		case "slug":
			p.Slug = huge
		case "markdown":
			p.Markdown = huge
		case "excerpt":
			p.Excerpt = huge
		case "tags":
			p.Tags = []string{huge}
		case "category":
			p.Category = huge
		case "cover":
			p.Cover = huge
		}
		if comparisonWithinBudget(p, small) {
			t.Fatalf("field %s omitted from escaping budget", field)
		}
	}
	// An accepted result remains below the transport bound even with worst-case
	// escaping. The existing full-size Markdown reconstruction test also applies.
	p := small
	p.Cover = "/media/" + strings.Repeat("\x00", 1024*1024)
	q := small
	q.Cover = "/media/" + strings.Repeat("<&", 512*1024)
	if !comparisonWithinBudget(p, q) {
		t.Fatal("bounded valid strings rejected")
	}
	raw, _ := json.Marshal(RevisionComparison{From: ComparedRevision{Content: p}, To: ComparedRevision{Content: q}, Review: reviewContent(p, q)})
	if int64(len(raw))+64*1024 > revisionComparisonJSONLimit {
		t.Fatal("accepted response exceeds transport budget")
	}
}

func TestComparisonOversizedLegacyCoverPreservesBackupReadability(t *testing.T) {
	src := testStore(t)
	first := create(t, src, "legacy-budget")
	current := relationUpdate(t, src, first, "Current bounded draft", first.Slug)
	legacy := src.State.Posts[first.ID].Revisions[0]
	legacy.Cover = "/media/" + strings.Repeat("a", 40*1024*1024)
	if e := ValidatePost(legacy); e != nil {
		t.Fatal("fixture is not a valid legacy post")
	}
	src.State.Posts[first.ID].Revisions[0] = legacy
	if e := ValidateState(src.State); e != nil {
		t.Fatal("fixture is not a valid legacy state")
	}
	backup := exported(t, src)
	raw, _ := json.Marshal(backup)
	if len(raw) >= 64*1024*1024 {
		t.Fatal("fixture does not fit the existing backup budget")
	}
	dst := testStore(t)
	call(t, dst, "backup.restore", restoreArgs(backup))
	if dst.State.Posts[first.ID].Revisions[0].Cover != legacy.Cover {
		t.Fatal("legacy content changed on import")
	}
	// No huge result is formatted into test output if the regression returns it.
	args, _ := json.Marshal(map[string]any{"id": first.ID, "revision": current.Revision, "from_revision": 1, "to_revision": 1})
	result, e := dst.Call(context.Background(), "posts.compare", args, "test")
	if e == nil {
		encoded, _ := json.Marshal(result)
		t.Fatalf("oversized comparison returned %d JSON bytes instead of a controlled budget error", len(encoded))
	}
	err, ok := e.(*Error)
	if !ok || err.Code != "validation" || !strings.Contains(err.Message, "64 MiB JSON budget") {
		t.Fatal("missing controlled localized-budget error")
	}
	after := exported(t, dst)
	var st State
	if e = json.Unmarshal(after.State, &st); e != nil {
		t.Fatal(e)
	}
	if st.Posts[first.ID].Revisions[0].Cover != legacy.Cover {
		t.Fatal("rejected read changed legacy backup content")
	}
	// A small current/current comparison still works on the same imported post.
	v := compareSaved(t, dst, current, current.Revision, current.Revision)
	if v.From.Content.Cover != "" {
		t.Fatal("current snapshot conflated with huge historical field")
	}
}
