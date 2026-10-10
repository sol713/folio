package core

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func relationUpdate(t *testing.T, s *Store, p Post, markdown, slug string) Post {
	t.Helper()
	return getPost(t, call(t, s, "posts.update", map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": p.Title, "slug": slug, "markdown": markdown, "excerpt": p.Excerpt, "tags": p.Tags, "category": p.Category, "cover": p.Cover, "featured": p.Featured}))
}
func relations(t *testing.T, s *Store, p Post) ArticleRelations {
	t.Helper()
	return call(t, s, "posts.relations", map[string]any{"id": p.ID, "revision": p.Revision}).(ArticleRelations)
}
func relationTo(edges []ArticleRelation, id, availability string) *ArticleRelation {
	for i := range edges {
		e := &edges[i]
		if e.Target != nil && e.Target.ID == id && e.Availability == availability {
			return e
		}
	}
	return nil
}
func TestRelationsSavedLiveDedupeAndSourceLocations(t *testing.T) {
	s := testStore(t)
	target := create(t, s, "target")
	target = getPost(t, call(t, s, "posts.publish", action(target, true)))
	private := create(t, s, "private")
	source := create(t, s, "source")
	source = relationUpdate(t, s, source, "[live target](/posts/target)", "source")
	source = getPost(t, call(t, s, "posts.publish", action(source, true)))
	oldLive := source.Revision
	source = relationUpdate(t, s, source, "[a](/posts/target) [b](/posts/target?x=1#anchor)\n[c](target)\n[d](../posts/target)\n[private](/posts/private)\n[self](#part)", "source")
	incoming := create(t, s, "incoming")
	incoming = relationUpdate(t, s, incoming, "[source](/posts/source)", "incoming")
	incoming = getPost(t, call(t, s, "posts.publish", action(incoming, true)))
	incoming = relationUpdate(t, s, incoming, "No saved outgoing links", "incoming")
	result := relations(t, s, source)
	if result.Revision != source.Revision || result.Published == nil || result.Published.Article.Revision != oldLive || result.PublishedRevision != oldLive || result.Truncated {
		t.Fatalf("bad revisions %+v", result)
	}
	edge := relationTo(result.Draft.Outgoing, target.ID, "public")
	if edge == nil || edge.Count != 4 || len(edge.Locations) != 3 || !edge.LocationsTruncated || edge.Target.Kind != "published" || edge.Target.Revision != target.Revision {
		t.Fatalf("bad duplicate edge %+v", edge)
	}
	wantLines := []int{1, 1, 2}
	for i, l := range edge.Locations {
		if l.Line != wantLines[i] || l.Precision != "line" {
			t.Fatalf("bad location %+v", l)
		}
	}
	if relationTo(result.Draft.Outgoing, private.ID, "private") == nil || relationTo(result.Draft.Outgoing, source.ID, "future_self") == nil {
		t.Fatal("private/self edges lost")
	}
	if len(result.Published.Outgoing) != 1 || result.Published.Outgoing[0].Count != 1 {
		t.Fatal("saved edits changed live links")
	}
	found := false
	for _, e := range result.IncomingPublished {
		if e.Source.ID == incoming.ID {
			found = true
			if e.Source.Kind != "published" || e.Source.Revision != incoming.PublishedRevision || e.Source.DraftRevision != incoming.Revision || e.Source.Status != "changed" {
				t.Fatalf("wrong source snapshot %+v", e)
			}
		}
	}
	if !found {
		t.Fatal("published incoming missing")
	}
	for _, e := range result.IncomingDraft {
		if e.Source.ID == incoming.ID {
			t.Fatal("published link incorrectly attributed to saved draft")
		}
	}
}

func TestRelationsActualRoutesRenameUnpublishDeleteAndReadOnly(t *testing.T) {
	s := testStore(t)
	target := create(t, s, "old-name")
	target = getPost(t, call(t, s, "posts.publish", action(target, true)))
	target = relationUpdate(t, s, target, target.Markdown, "new-name")
	target = getPost(t, call(t, s, "posts.publish", action(target, true)))
	source := create(t, s, "source")
	source = relationUpdate(t, s, source, "[old](/posts/old-name) [new](/posts/new-name) [encoded](/posts/%6eew-name)\n[prefix](/posts/new-name-more) [slash](/posts/new-name/)\n[external](https://other.example/posts/new-name)\n`[code](/posts/new-name)`\n<img src=\"/posts/new-name\">\n![image](/posts/new-name)", "source")
	before, _ := json.Marshal(s.State)
	result := relations(t, s, source)
	again := relations(t, s, source)
	after, _ := json.Marshal(s.State)
	if string(before) != string(after) || !reflect.DeepEqual(result, again) {
		t.Fatal("read changed state or output is nondeterministic")
	}
	edge := relationTo(result.Draft.Outgoing, target.ID, "public")
	if edge == nil || edge.Count != 3 || edge.Target.Slug != "new-name" || edge.Locations[0].Route != "redirect" || len(result.Draft.Outgoing) != 2 {
		t.Fatalf("route mismatch %+v", result.Draft.Outgoing)
	}
	if result.Draft.Counts["unchecked_not_relations"] != 1 || result.Draft.Counts["external_not_relations"] != 1 || result.Draft.Counts["images_not_relations"] != 1 {
		t.Fatalf("exclusion mismatch %+v", result.Draft.Counts)
	}
	call(t, s, "posts.unpublish", action(target, true))
	next := relations(t, s, source)
	if next.InstanceRevision <= result.InstanceRevision || relationTo(next.Draft.Outgoing, target.ID, "public") != nil || relationTo(next.Draft.Outgoing, target.ID, "private") == nil {
		t.Fatal("unpublish not reflected immediately")
	}
	call(t, s, "posts.delete", action(target, true))
	next = relations(t, s, source)
	for _, e := range next.Draft.Outgoing {
		if e.Target != nil && e.Target.ID == target.ID {
			t.Fatal("deleted target still resolved")
		}
	}
	callError(t, s, "posts.relations", map[string]any{"id": target.ID, "revision": target.Revision}, "not_found")
	for _, args := range []map[string]any{{"id": source.ID}, {"id": source.ID, "revision": 0}, {"id": source.ID, "revision": source.Revision, "markdown": "probe"}, {"id": source.ID, "revision": source.Revision, "url": "file:///etc/passwd"}} {
		callError(t, s, "posts.relations", args, "validation")
	}
	callError(t, s, "posts.relations", map[string]any{"id": source.ID, "revision": source.Revision + 1}, "conflict")
	spec := Specs["posts.relations"]
	if !spec.ReadOnly || spec.Scope != "read" {
		t.Fatal("relation operation expanded authority")
	}
}

func TestRelationsAmbiguousAliasAndPublicRoutePrecedence(t *testing.T) {
	state := freshState()
	source := Post{ID: "source", Slug: "source", Revision: 1, Markdown: "[alias](/posts/shared)"}
	a, b := Post{ID: "a", Slug: "a", Revision: 1}, Post{ID: "b", Slug: "b", Revision: 1}
	state.Posts = map[string]*Record{"source": {Draft: source}, "a": {Draft: a, Live: &a, OldSlugs: []string{"shared", "shared"}}, "b": {Draft: b, Live: &b, OldSlugs: []string{"shared"}}}
	out := analyzeRelations(state.Posts[source.ID], state, "instance")
	if len(out.Draft.Outgoing) != 1 || out.Draft.Outgoing[0].Target != nil || out.Draft.Outgoing[0].Availability != "ambiguous" {
		t.Fatal("ambiguous redirect chose a target")
	}
	delete(state.Posts, "b")
	out = analyzeRelations(state.Posts[source.ID], state, "instance")
	if relationTo(out.Draft.Outgoing, "a", "public") == nil {
		t.Fatal("duplicate same-owner alias became ambiguous")
	}
	state.Posts["b"] = &Record{Draft: b, Live: &b, OldSlugs: []string{"shared"}}
	live := Post{ID: "direct", Slug: "shared", Revision: 1}
	state.Posts[live.ID] = &Record{Draft: live, Live: &live}
	out = analyzeRelations(state.Posts[source.ID], state, "instance")
	if relationTo(out.Draft.Outgoing, "direct", "public") == nil {
		t.Fatal("direct public route did not win over aliases")
	}
}

func TestRelationsBoundsIncomingNotDependentOnOutgoingLimit(t *testing.T) {
	state := freshState()
	target := Post{ID: "target", Slug: "target", Revision: 1}
	source := Post{ID: "source", Slug: "source", Revision: 1}
	for i := 0; i < relationEdgeLimit; i++ {
		source.Markdown += fmt.Sprintf("[missing](/posts/missing-%d)\n", i)
	}
	source.Markdown += "[target](/posts/target)"
	state.Posts = map[string]*Record{target.ID: {Draft: target, Live: &target}, source.ID: {Draft: source}}
	out := analyzeRelations(state.Posts[source.ID], state, "i")
	if len(out.Draft.Outgoing) != relationEdgeLimit || !out.Truncated || out.Draft.Counts["outgoing_groups"] != 101 {
		t.Fatal("outgoing bound incorrect")
	}
	incoming := analyzeRelations(state.Posts[target.ID], state, "i")
	if len(incoming.IncomingDraft) != 1 || incoming.IncomingDraft[0].Source.ID != source.ID {
		t.Fatal("incoming relied on truncated outgoing response")
	}
	source.Markdown = strings.Repeat("[target](/posts/target)\n", checkReferenceLimit+1)
	state.Posts[source.ID].Draft = source
	out = analyzeRelations(state.Posts[source.ID], state, "i")
	edge := relationTo(out.Draft.Outgoing, target.ID, "public")
	if edge == nil || edge.Count != checkReferenceLimit || len(edge.Locations) != relationLocationLimit || !out.Truncated {
		t.Fatal("reference/location bound incorrect")
	}
	state = freshState()
	for i := 0; i < relationSnapshotLimit+1; i++ {
		id := fmt.Sprintf("p-%04d", i)
		state.Posts[id] = &Record{Draft: Post{ID: id, Slug: id, Revision: 1}}
	}
	out = analyzeRelations(state.Posts["p-1000"], state, "i")
	if out.Counts["scanned_snapshots"] != relationSnapshotLimit || out.Counts["omitted_snapshots"] != 1 || !out.Truncated || out.Draft.Article.ID != "p-1000" {
		t.Fatal("source bound or requested priority incorrect")
	}
	state = freshState()
	for i := 0; i < 9; i++ {
		id := fmt.Sprintf("p-%d", i)
		state.Posts[id] = &Record{Draft: Post{ID: id, Slug: id, Revision: 1, Markdown: strings.Repeat("x", 1048576)}}
	}
	out = analyzeRelations(state.Posts["p-8"], state, "i")
	if out.Counts["scanned_markdown_bytes"] != relationByteLimit || out.Counts["omitted_snapshots"] != 1 || !out.Truncated {
		t.Fatal("byte bound incorrect")
	}
}

func TestRelationsGlobalReferenceAndIncomingLimits(t *testing.T) {
	state := freshState()
	for i := 0; i < 11; i++ {
		id := fmt.Sprintf("p-%02d", i)
		state.Posts[id] = &Record{Draft: Post{ID: id, Slug: id, Revision: 1, Markdown: strings.Repeat("[self](#part)\n", checkReferenceLimit)}}
	}
	out := analyzeRelations(state.Posts["p-10"], state, "i")
	if out.Counts["scanned_references"] != relationReferenceLimit || out.Counts["omitted_snapshots"] != 1 || !out.Truncated || out.Draft.Truncated || out.Draft.Outgoing[0].Count != checkReferenceLimit {
		t.Fatal("global reference budget or inclusive per-source boundary incorrect")
	}
	state = freshState()
	state.Posts["target"] = &Record{Draft: Post{ID: "target", Slug: "target", Revision: 1}}
	for i := 0; i <= relationEdgeLimit; i++ {
		id := fmt.Sprintf("source-%03d", i)
		p := Post{ID: id, Slug: id, Revision: 1, Markdown: "[target](/posts/target) [again](/posts/target)"}
		state.Posts[id] = &Record{Draft: p, Live: &p}
	}
	out = analyzeRelations(state.Posts["target"], state, "i")
	if len(out.IncomingDraft) != relationEdgeLimit || len(out.IncomingPublished) != relationEdgeLimit || out.Counts["incoming_saved_draft_groups"] != relationEdgeLimit+1 || out.Counts["incoming_published_occurrences"] != 2*(relationEdgeLimit+1) || !out.Truncated {
		t.Fatal("incoming caps must retain full scanned counts for each snapshot kind")
	}
}
