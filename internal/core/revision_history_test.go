package core

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRevisionHistoryPagesAndCompatibility(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "paged-history")
	for n := 2; n <= 19; n++ {
		p = getPost(t, call(t, s, "posts.update", map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": p.Title, "slug": p.Slug, "markdown": strings.Repeat("PRIVATE_SAVED_BODY ", 1000)}))
	}
	call(t, s, "posts.publish", action(p, true))
	before, _ := json.Marshal(s.State)
	legacy := call(t, s, "posts.get", map[string]any{"id": p.ID}).(map[string]any)
	if len(legacy["revisions"].([]Post)) != 19 || legacy["revision_count"] != nil {
		t.Fatal("default legacy response changed")
	}
	light := call(t, s, "posts.get", map[string]any{"id": p.ID, "include_revisions": false}).(map[string]any)
	if len(light["revisions"].([]Post)) != 0 || light["revision_count"] != 19 || !reflect.DeepEqual(light["post"], legacy["post"]) || !reflect.DeepEqual(light["live"], legacy["live"]) || light["html"] != legacy["html"] {
		t.Fatal("light read changed current/live content or omitted count")
	}
	seen := []int{}
	cursor := 0
	for {
		args := map[string]any{"id": p.ID, "revision": 19}
		if cursor > 0 {
			args["before_revision"] = cursor
		}
		page := call(t, s, "posts.history", args).(RevisionHistory)
		if page.ID != p.ID || page.Total != 19 || page.Limit != 8 || page.Revision != 19 || page.PublishedRevision != 19 || page.InstanceID != s.InstanceID() || len(page.Revisions) > 8 {
			t.Fatal(page)
		}
		encoded, _ := json.Marshal(page)
		if strings.Contains(string(encoded), "PRIVATE_SAVED_BODY") || strings.Contains(string(encoded), "markdown") || len(encoded) > 4096 {
			t.Fatal("history transmitted content")
		}
		for _, row := range page.Revisions {
			seen = append(seen, row.Revision)
			if row.Current != (row.Revision == 19) || row.Published != (row.Revision == 19) {
				t.Fatal(row)
			}
		}
		if !page.HasMore {
			if page.NextBeforeRevision != 0 {
				t.Fatal(page)
			}
			break
		}
		if page.NextBeforeRevision != page.Revisions[len(page.Revisions)-1].Revision {
			t.Fatal("wrong cursor")
		}
		cursor = page.NextBeforeRevision
	}
	for i, v := range seen {
		if v != 19-i {
			t.Fatal("duplicate/missing/unstable row", seen)
		}
	}
	if len(seen) != 19 {
		t.Fatal(seen)
	}
	after, _ := json.Marshal(s.State)
	if string(before) != string(after) {
		t.Fatal("read wrote state")
	}
	call(t, s, "posts.restore", map[string]any{"id": p.ID, "expected_revision": 19, "revision": 1})
	callError(t, s, "posts.history", map[string]any{"id": p.ID, "revision": 19, "before_revision": 12}, "conflict")
}

func TestRevisionHistorySparseOrderAndValidation(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "sparse-history")
	// Valid sparse/unsorted legacy snapshots have no offset or timestamp ordering.
	r := s.State.Posts[p.ID]
	r.Draft.Revision = 12
	a, b, c := r.Draft, r.Draft, r.Draft
	a.Revision = 2
	b.Revision = 11
	c.Revision = 7
	a.UpdatedAt = strings.Repeat("bad-date", 1000)
	r.Revisions = []Post{c, a, b}
	page := call(t, s, "posts.history", map[string]any{"id": p.ID, "revision": 12, "limit": 2}).(RevisionHistory)
	if page.Revisions[0].Revision != 11 || page.Revisions[1].Revision != 7 || page.NextBeforeRevision != 7 || page.Total != 3 {
		t.Fatal(page)
	}
	last := call(t, s, "posts.history", map[string]any{"id": p.ID, "revision": 12, "limit": 2, "before_revision": 7}).(RevisionHistory)
	if len(last.Revisions) != 1 || last.Revisions[0].Revision != 2 || last.Revisions[0].UpdatedAt != "" || last.HasMore {
		t.Fatal(last)
	}
	for _, extra := range []map[string]any{{"limit": 0}, {"limit": 51}, {"limit": nil}, {"limit": 1.5}, {"before_revision": 0}, {"before_revision": -1}, {"before_revision": 13}, {"before_revision": nil}, {"markdown": "injected"}, {"revision": 0}} {
		args := map[string]any{"id": p.ID, "revision": 12}
		for key, value := range extra {
			args[key] = value
		}
		callError(t, s, "posts.history", args, "validation")
	}
	for _, op := range []string{"posts.get", "posts.preview"} {
		callError(t, s, op, map[string]any{"id": p.ID, "include_revisions": nil}, "validation")
	}
}
