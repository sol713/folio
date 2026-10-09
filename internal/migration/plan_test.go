package migration

import (
	"encoding/json"
	"strings"
	"testing"
)

func doc(t *testing.T, name string) Document {
	t.Helper()
	return parseOK(t, name+".md", "---\ntitle: "+name+"\nslug: "+name+"\n---\nBody "+name)
}
func planOK(t *testing.T, docs []Document, existing []Existing, opts Options) ImportPlan {
	t.Helper()
	p, e := Plan(docs, existing, opts)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestPlanDeterministicAndFrozen(t *testing.T) {
	a, b := doc(t, "a"), doc(t, "b")
	a.Extra = map[string]string{"x": "value"}
	a.ContentHash = HashDocument(a)
	p := planOK(t, []Document{b, a}, nil, Options{})
	q := planOK(t, []Document{a, b}, nil, Options{})
	x, _ := json.Marshal(p)
	y, _ := json.Marshal(q)
	if string(x) != string(y) {
		t.Fatal("input ordering changed plan")
	}
	a.Extra["x"] = "changed"
	if p.Entries[0].Document.Extra["x"] != "value" {
		t.Fatal("plan aliases caller memory")
	}
	if e := ValidatePlan(p); e != nil {
		t.Fatal(e)
	}
}
func TestPlanConflicts(t *testing.T) {
	d := doc(t, "hello")
	existing := []Existing{{"post-id", "hello", 7}}
	for _, policy := range []string{"", "error", "skip", "update"} {
		t.Run(policy, func(t *testing.T) {
			p := planOK(t, []Document{d}, existing, Options{Conflict: policy})
			action := p.Entries[0].Action
			if policy == "" || policy == "error" {
				if action != "blocked" || len(p.Diagnostics) != 1 || ValidatePlan(p) == nil {
					t.Fatal(p)
				}
			} else {
				if action != policy || p.Entries[0].ExpectedRevision != 7 || ValidatePlan(p) != nil {
					t.Fatal(p)
				}
			}
		})
	}
}
func TestDuplicateSourcesAllBlocked(t *testing.T) {
	for _, kind := range []string{"slug", "path"} {
		t.Run(kind, func(t *testing.T) {
			a, b := doc(t, "a"), doc(t, "b")
			if kind == "slug" {
				b.Slug = "a"
				b.ContentHash = HashDocument(b)
			} else {
				b.SourcePath = "a.md"
			}
			p := planOK(t, []Document{a, b}, nil, Options{})
			if len(p.Diagnostics) != 2 || p.Entries[0].Action != "blocked" || p.Entries[1].Action != "blocked" {
				t.Fatal(p)
			}
		})
	}
}
func TestPlanInvalidDocumentIsRecoverableDiagnostic(t *testing.T) {
	d := doc(t, "a")
	d.Title = ""
	p := planOK(t, []Document{d}, nil, Options{})
	if len(p.Diagnostics) != 1 || p.Diagnostics[0].SourcePath != "a.md" {
		t.Fatal(p)
	}
}
func TestPlanRejectsGlobalOptionsAndAmbiguousExisting(t *testing.T) {
	cases := []struct {
		name     string
		existing []Existing
		opts     Options
	}{
		{"policy", nil, Options{Conflict: "overwrite-everything"}},
		{"duplicateSlug", []Existing{{"a", "slug", 1}, {"b", "slug", 1}}, Options{}},
		{"duplicateID", []Existing{{"a", "one", 1}, {"a", "two", 1}}, Options{}},
		{"missingID", []Existing{{"", "one", 1}}, Options{}},
		{"badRevision", []Existing{{"a", "one", 0}}, Options{}},
		{"badSlug", []Existing{{"a", "../one", 1}}, Options{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, e := Plan(nil, c.existing, c.opts); e == nil {
				t.Fatal("accepted")
			}
		})
	}
}
func TestRequestIgnoresSourcePublicationAndDates(t *testing.T) {
	d := doc(t, "a")
	d.Draft = false
	d.Date = "2024-01-01T00:00:00Z"
	d.PublishDate = "2025-01-01T00:00:00Z"
	d.Extra = map[string]string{"token": "never-send"}
	d.ContentHash = HashDocument(d)
	p := planOK(t, []Document{d}, nil, Options{})
	b, _ := json.Marshal(requestFor(p.Entries[0], true))
	for _, s := range []string{"draft", "publish", "date", "token", "never-send", "expected_revision", "\"id\""} {
		if strings.Contains(string(b), s) {
			t.Fatalf("source metadata reached write request: %s", b)
		}
	}
}
func TestPlanTamperRejected(t *testing.T) {
	p := planOK(t, []Document{doc(t, "a")}, nil, Options{})
	p.Entries[0].Document.Markdown = "new"
	if ValidatePlan(p) == nil {
		t.Fatal("tamper accepted")
	}
}
func TestPlanStructuralTamperRejectedEvenIfResealed(t *testing.T) {
	mutations := map[string]func(*ImportPlan){
		"publish":        func(p *ImportPlan) { p.Entries[0].Action = "publish" },
		"arbitraryKey":   func(p *ImportPlan) { p.Entries[0].IdempotencyKey = "user-controlled" },
		"createID":       func(p *ImportPlan) { p.Entries[0].TargetID = "id" },
		"missingHash":    func(p *ImportPlan) { p.Entries[0].Document.ContentHash = "" },
		"fakeHash":       func(p *ImportPlan) { p.Entries[0].Document.ContentHash = strings.Repeat("0", 64) },
		"unknownVersion": func(p *ImportPlan) { p.Version = 2 },
	}
	for name, mut := range mutations {
		t.Run(name, func(t *testing.T) {
			p := planOK(t, []Document{doc(t, "a")}, nil, Options{})
			mut(&p)
			sealPlan(&p)
			if ValidatePlan(p) == nil {
				t.Fatal("invalid structure accepted")
			}
		})
	}
}
func TestPlanDuplicateRetryPayloadAcrossPaths(t *testing.T) {
	a := doc(t, "a")
	b := a
	b.SourcePath = "nested/a.md"
	p := planOK(t, []Document{a}, nil, Options{})
	q := planOK(t, []Document{b}, nil, Options{})
	if p.ID == q.ID || p.Entries[0].IdempotencyKey != q.Entries[0].IdempotencyKey {
		t.Fatal("same write should retain retry identity independently of presentation path")
	}
}
func TestMemoryLimits(t *testing.T) {
	docs := make([]Document, MaxDocuments+1)
	if _, e := Plan(docs, nil, Options{}); e == nil {
		t.Fatal("document count unbounded")
	}
}

func TestAdapterDiagnosticsAreFrozenAndBlockApplication(t *testing.T) {
	p := planOK(t, []Document{doc(t, "good")}, nil, Options{})
	diagnostics := []Diagnostic{{"z.md", "parse_error", "Invalid source"}, {"a.md", "reserved_slug", "Existing live slug"}}
	blocked := WithDiagnostics(p, diagnostics)
	if blocked.ID == p.ID || len(blocked.Diagnostics) != 2 || blocked.Diagnostics[0].SourcePath != "a.md" || ValidatePlan(blocked) == nil {
		t.Fatal("adapter failures were not sealed into a blocked plan")
	}
	if e := ValidatePlan(blocked); !strings.Contains(e.Error(), "plan contains diagnostics") {
		t.Fatalf("blocked plan lost its valid checksum: %v", e)
	}
	if len(p.Diagnostics) != 0 || ValidatePlan(p) != nil {
		t.Fatal("adding adapter diagnostics mutated the original plan")
	}
	diagnostics[0].Message = "changed after preview"
	if blocked.Diagnostics[1].Message != "Invalid source" {
		t.Fatal("blocked plan aliases adapter diagnostics")
	}
	reversed := WithDiagnostics(p, []Diagnostic{{"a.md", "reserved_slug", "Existing live slug"}, {"z.md", "parse_error", "Invalid source"}})
	if reversed.ID != blocked.ID {
		t.Fatal("adapter diagnostic order changes frozen plan identity")
	}
}
