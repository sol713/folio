package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func compareSaved(t *testing.T, s *Store, p Post, from, to int) RevisionComparison {
	t.Helper()
	return call(t, s, "posts.compare", map[string]any{"id": p.ID, "revision": p.Revision, "from_revision": from, "to_revision": to}).(RevisionComparison)
}
func TestRevisionComparisonAllFieldsReadOnlyAndSharedDiff(t *testing.T) {
	s := testStore(t)
	first := create(t, s, "first")
	first = getPost(t, call(t, s, "posts.publish", action(first, true)))
	second := getPost(t, call(t, s, "posts.update", map[string]any{"id": first.ID, "expected_revision": first.Revision, "title": "<img src=x> 新稿", "slug": "new-slug", "markdown": "共同行\n新内容\n尾行\n", "excerpt": "新摘要", "tags": []string{"新话题"}, "category": "新分类", "cover": "/media/fake.png", "featured": true}))
	before, _ := json.Marshal(s.State)
	result := compareSaved(t, s, second, second.Revision, first.Revision)
	again := compareSaved(t, s, second, second.Revision, first.Revision)
	after, _ := json.Marshal(s.State)
	if string(before) != string(after) || !reflect.DeepEqual(result, again) {
		t.Fatal("read wrote state or repeat changed result")
	}
	if result.ID != first.ID || result.Revision != second.Revision || !result.From.Current || result.To.Current || !result.To.Published || result.PublishedRevision != first.Revision || !result.Restorable {
		t.Fatalf("wrong snapshot %+v", result)
	}
	if len(result.Review.ChangedFields) != 8 || !reflect.DeepEqual(result.Review, reviewProposal(Proposal{Base: &result.From.Content, Candidate: result.To.Content})) {
		t.Fatal("all fields/shared review mismatch")
	}
	if result.From.Content.Title != second.Title || result.To.Content.Markdown != first.Markdown {
		t.Fatal("content conflated")
	}
	same := compareSaved(t, s, second, second.Revision, second.Revision)
	if len(same.Review.ChangedFields) != 0 || len(same.Review.MarkdownDiff) != 0 || same.Restorable || same.RestoreReason != "current_revision" {
		t.Fatal("same revision comparison")
	}
	reversed := compareSaved(t, s, second, first.Revision, second.Revision)
	if !reflect.DeepEqual(reversed.Review, reviewContent(result.To.Content, result.From.Content)) {
		t.Fatal("direction not respected")
	}
}
func TestRevisionComparisonValidationStaleDeletedAndSchema(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "schema")
	valid := map[string]any{"id": p.ID, "revision": 1, "from_revision": 1, "to_revision": 1}
	for _, args := range []map[string]any{{}, {"id": p.ID, "revision": 1, "from_revision": 1}, {"id": "", "revision": 1, "from_revision": 1, "to_revision": 1}, {"id": p.ID, "revision": 0, "from_revision": 1, "to_revision": 1}, {"id": p.ID, "revision": 1, "from_revision": -1, "to_revision": 1}, {"id": p.ID, "revision": 1, "from_revision": 1, "to_revision": 1, "markdown": "arbitrary"}} {
		callError(t, s, "posts.compare", args, "validation")
	}
	callError(t, s, "posts.compare", map[string]any{"id": p.ID, "revision": 2, "from_revision": 1, "to_revision": 1}, "conflict")
	callError(t, s, "posts.compare", map[string]any{"id": p.ID, "revision": 1, "from_revision": 2, "to_revision": 1}, "not_found")
	spec := Specs["posts.compare"]
	if spec.Scope != "read" || !spec.ReadOnly || spec.InputSchema["additionalProperties"] != false || len(spec.InputSchema["required"].([]string)) != 4 {
		t.Fatal("schema/scope changed")
	}
	call(t, s, "posts.delete", action(p, true))
	callError(t, s, "posts.compare", valid, "not_found")
}
func TestRevisionComparisonRestoreRetryConflictAndLiveIsolation(t *testing.T) {
	s := testStore(t)
	first := create(t, s, "original")
	first = getPost(t, call(t, s, "posts.publish", action(first, true)))
	second := relationUpdate(t, s, first, "Changed private content", "private-slug")
	comparison := compareSaved(t, s, second, second.Revision, first.Revision)
	if !comparison.Restorable {
		t.Fatal("valid target blocked")
	}
	payload := map[string]any{"id": second.ID, "expected_revision": second.Revision, "revision": first.Revision, "idempotency_key": "fictional-restore-retry"}
	restored := getPost(t, call(t, s, "posts.restore", payload))
	retry := getPost(t, call(t, s, "posts.restore", payload))
	if !reflect.DeepEqual(restored, retry) || restored.Revision != second.Revision+1 || restored.Markdown != first.Markdown || s.State.Posts[first.ID].Live.Markdown != first.Markdown || s.State.Posts[first.ID].Live.Revision != first.Revision {
		t.Fatal("restore retry/live isolation")
	}
	next := compareSaved(t, s, restored, restored.Revision, first.Revision)
	if next.Restorable || next.RestoreReason != "no_content_changes" {
		t.Fatal("duplicate restore not advised as no-op")
	}
	newer := relationUpdate(t, s, restored, "Another writer", "private-slug")
	callError(t, s, "posts.restore", map[string]any{"id": restored.ID, "expected_revision": restored.Revision, "revision": first.Revision, "idempotency_key": "fictional-stale-restore"}, "conflict")
	if s.State.Posts[first.ID].Draft.Markdown != newer.Markdown {
		t.Fatal("stale restore overwrote writer")
	}
}
func TestRevisionComparisonSparseHistorySlugConflictAndLinearBounds(t *testing.T) {
	s := testStore(t)
	first := create(t, s, "reserved")
	second := relationUpdate(t, s, first, "second", "second")
	create(t, s, "reserved")
	result := compareSaved(t, s, second, second.Revision, first.Revision)
	if result.Restorable || result.RestoreReason != "historical_slug_in_use" {
		t.Fatal("occupied slug advertised as restorable")
	}
	record := &Record{Draft: Post{ID: "sparse", Revision: 3}, Live: &Post{ID: "sparse", Revision: 1}, Revisions: []Post{{ID: "sparse", Revision: 2}}}
	snapshot, inHistory, e := comparedRevision(record, 1)
	if e != nil || inHistory || snapshot.Kind != "published_snapshot" || !snapshot.Published {
		t.Fatal("sparse live snapshot lost")
	}
	// Hostile full-sized plain text has a single bounded changed range, preserving
	// exact source reconstruction without a quadratic line-matching matrix.
	a := ProposalContent{Markdown: strings.Repeat("\x00", 1<<20), Tags: []string{}}
	b := ProposalContent{Markdown: strings.Repeat("中", (1<<20)/3), Tags: []string{}}
	review := reviewContent(a, b)
	if len(review.MarkdownDiff) > 4 || len(review.ChangedFields) != 1 {
		t.Fatal("unbounded changed ranges")
	}
	var old, new strings.Builder
	for _, chunk := range review.MarkdownDiff {
		if chunk.Kind != "add" {
			old.WriteString(chunk.Text)
		}
		if chunk.Kind != "remove" {
			new.WriteString(chunk.Text)
		}
	}
	if old.String() != a.Markdown || new.String() != b.Markdown {
		t.Fatal("diff lost bytes")
	}
	raw, _ := json.Marshal(RevisionComparison{From: ComparedRevision{Content: a}, To: ComparedRevision{Content: b}, Review: review})
	if len(raw) >= 64<<20 {
		t.Fatal("comparison exceeds client response limit")
	}
}
