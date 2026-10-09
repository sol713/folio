package migration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestDirectoryPlanCollectsParseErrors(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.md"), "# Good\nBody")
	write(t, filepath.Join(root, "b.md"), "---\ntitle: Bad\n")
	write(t, filepath.Join(root, "skip.txt"), "ignore")
	p, e := PlanDirectory(root, nil, Options{})
	if e != nil || len(p.Entries) != 1 || len(p.Diagnostics) != 1 || ValidatePlan(p) == nil {
		t.Fatal(p, e)
	}
}
func TestDirectoryRejectsSymlinksAndAncestor(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "real.md"), "Body")
	if e := os.Symlink("real.md", filepath.Join(root, "linked.md")); e != nil {
		t.Fatal(e)
	}
	p, e := PlanDirectory(root, nil, Options{})
	if e != nil || len(p.Diagnostics) != 1 || p.Diagnostics[0].Code != "unsafe_path" {
		t.Fatal(p, e)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if e = os.Symlink(root, alias); e != nil {
		t.Fatal(e)
	}
	if _, e = PlanDirectory(alias, nil, Options{}); e == nil {
		t.Fatal("symlink root accepted")
	}
}
func TestDirectoryRejectsEscapeSymlinkWithoutReading(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	write(t, outside, "PRIVATE")
	if e := os.Symlink(outside, filepath.Join(root, "escape.md")); e != nil {
		t.Fatal(e)
	}
	docs, diags, e := ScanDirectory(root)
	if e != nil || len(docs) != 0 || len(diags) != 1 || strings.Contains(diags[0].Message, "PRIVATE") {
		t.Fatal(docs, diags, e)
	}
}
func TestDirectoryOversizedFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "large.md"), strings.Repeat("a", MaxFileBytes+1))
	docs, diagnostics, e := ScanDirectory(root)
	if e != nil || len(docs) != 0 || len(diagnostics) != 1 {
		t.Fatal(docs, diagnostics, e)
	}
}
func TestExportRoundtripBodyAndSafeDraft(t *testing.T) {
	b, e := os.ReadFile("testdata/hugo-yaml.md")
	if e != nil {
		t.Fatal(e)
	}
	d, e := Parse("hugo-yaml.md", b)
	if e != nil {
		t.Fatal(e)
	}
	out, e := Export(d)
	if e != nil {
		t.Fatal(e)
	}
	round, e := Parse("slow-notes.md", out)
	if e != nil {
		t.Fatal(e)
	}
	if round.Markdown != d.Markdown || round.Title != d.Title || round.Slug != d.Slug || round.Date != d.Date || !round.Draft || !reflect.DeepEqual(round.Tags, d.Tags) {
		t.Fatal(round)
	}
	if strings.Contains(string(out), "aliases:") {
		t.Fatal("unsupported fields injected into YAML")
	}
}
func TestExportDeterministicWithManifestMetadata(t *testing.T) {
	a, b := doc(t, "a"), doc(t, "b")
	a.Extra = map[string]string{"weight": "10"}
	a.ContentHash = HashDocument(a)
	x, e := BuildExport([]Document{b, a})
	if e != nil {
		t.Fatal(e)
	}
	y, e := BuildExport([]Document{a, b})
	if e != nil || string(x.Manifest) != string(y.Manifest) || string(x.Files[0].Data) != string(y.Files[0].Data) {
		t.Fatal("unstable export")
	}
	var m ExportManifest
	if json.Unmarshal(x.Manifest, &m) != nil || m.Items[0].Source.Extra["weight"] != "10" {
		t.Fatal("metadata not portable")
	}
}
func TestExportWritesOnlyNewPrivateDirectory(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "export")
	bundle, e := BuildExport([]Document{doc(t, "a")})
	if e != nil {
		t.Fatal(e)
	}
	if e = WriteExportNewDir(dest, bundle); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(dest)
	if e != nil || info.Mode().Perm() != 0700 {
		t.Fatal(info, e)
	}
	for _, name := range []string{"a.md", "manifest.json"} {
		info, e = os.Stat(filepath.Join(dest, name))
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal(info, e)
		}
	}
	write(t, filepath.Join(dest, "sentinel"), "keep")
	if WriteExportNewDir(dest, bundle) == nil {
		t.Fatal("existing target overwritten")
	}
	b, _ := os.ReadFile(filepath.Join(dest, "sentinel"))
	if string(b) != "keep" {
		t.Fatal("existing data damaged")
	}
}
func TestExportRejectsDuplicateAndTraversal(t *testing.T) {
	d := doc(t, "a")
	if _, e := BuildExport([]Document{d, d}); e == nil {
		t.Fatal("duplicate accepted")
	}
	for _, name := range []string{"../escape.md", "/escape.md", "folder/a.md", "A\\x.md"} {
		t.Run(name, func(t *testing.T) {
			bundle, _ := BuildExport([]Document{d})
			bundle.Files[0].Path = name
			dest := filepath.Join(t.TempDir(), "new")
			if WriteExportNewDir(dest, bundle) == nil {
				t.Fatal("unsafe export path")
			}
			if _, e := os.Stat(dest); !os.IsNotExist(e) {
				t.Fatal("invalid export created target")
			}
		})
	}
}
func TestExportManifestHashMismatchAndSourceMismatch(t *testing.T) {
	for _, what := range []string{"body", "manifest", "source"} {
		t.Run(what, func(t *testing.T) {
			bundle, _ := BuildExport([]Document{doc(t, "a")})
			switch what {
			case "body":
				bundle.Files[0].Data = []byte("tamper")
			case "manifest":
				bundle.Manifest = []byte(`{}`)
			case "source":
				var m ExportManifest
				json.Unmarshal(bundle.Manifest, &m)
				m.Items[0].Source.Title = "changed"
				m.Items[0].Source.ContentHash = HashDocument(m.Items[0].Source)
				bundle.Manifest, _ = json.Marshal(m)
			}
			if WriteExportNewDir(filepath.Join(t.TempDir(), "out"), bundle) == nil {
				t.Fatal("tamper accepted")
			}
		})
	}
}
func TestExportRejectsSymlinkParent(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(root, alias); e != nil {
		t.Fatal(e)
	}
	bundle, _ := BuildExport([]Document{doc(t, "a")})
	if WriteExportNewDir(filepath.Join(alias, "new"), bundle) == nil {
		t.Fatal("symlink parent accepted")
	}
}

func TestManifestRoundtripRetainsSourceMetadata(t *testing.T) {
	d := doc(t, "a")
	d.Extra = map[string]string{"weight": "10", "aliases": "[old]"}
	d.Draft = false
	d.ContentHash = HashDocument(d)
	bundle, e := BuildExport([]Document{d})
	if e != nil {
		t.Fatal(e)
	}
	docs, e := DocumentsFromExport(bundle)
	if e != nil || len(docs) != 1 || docs[0].Draft || !reflect.DeepEqual(docs[0].Extra, d.Extra) {
		t.Fatal(docs, e)
	}
	p, e := Plan(docs, nil, Options{})
	if e != nil || ValidatePlan(p) != nil {
		t.Fatal(p, e)
	}
	if strings.Contains(string(bundle.Files[0].Data), "draft: false") {
		t.Fatal("unsafe export draft intent")
	}
}
