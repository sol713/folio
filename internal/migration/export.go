package migration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Export writes a deterministic, safe YAML frontmatter subset followed by body text.
// draft is always true. Source publication intent is retained only in the manifest.
// Unknown fields are retained in the manifest, not interpolated into YAML syntax.
func Export(d Document) ([]byte, error) {
	if err := ValidateDocument(d); err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString("---\n")
	write := func(k, v string) {
		if v != "" {
			s, _ := json.Marshal(v)
			b.WriteString(k + ": " + string(s) + "\n")
		}
	}
	write("title", d.Title)
	write("slug", d.Slug)
	write("description", d.Excerpt)
	b.WriteString("draft: true\n")
	write("date", d.Date)
	write("publishDate", d.PublishDate)
	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	s, _ := json.Marshal(tags)
	b.WriteString("tags: " + string(s) + "\n")
	write("category", d.Category)
	write("cover", d.Cover)
	if d.Featured {
		b.WriteString("featured: true\n")
	}
	b.WriteString("---\n")
	b.WriteString(d.Markdown)
	if b.Len() > MaxFileBytes {
		return nil, fmt.Errorf("export exceeds file limit")
	}
	return []byte(b.String()), nil
}

type ExportFile struct {
	Path   string `json:"path"`
	Data   []byte `json:"-"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
}
type ManifestItem struct {
	Path   string   `json:"path"`
	SHA256 string   `json:"sha256"`
	Bytes  int      `json:"bytes"`
	Source Document `json:"source"`
}
type ExportManifest struct {
	Format  string         `json:"format"`
	Version int            `json:"version"`
	Items   []ManifestItem `json:"items"`
}
type ExportBundle struct {
	Files    []ExportFile
	Manifest []byte
}

func BuildExport(documents []Document) (ExportBundle, error) {
	bundle := ExportBundle{Files: []ExportFile{}}
	if len(documents) > MaxDocuments {
		return bundle, fmt.Errorf("too many export documents")
	}
	docs := append([]Document(nil), documents...)
	sort.Slice(docs, func(i, j int) bool { return docs[i].Slug < docs[j].Slug })
	m := ExportManifest{Format: "folio-markdown-export", Version: Version, Items: []ManifestItem{}}
	seen := map[string]bool{}
	total := 0
	for _, d := range docs {
		if seen[d.Slug] {
			return bundle, fmt.Errorf("duplicate export slug")
		}
		seen[d.Slug] = true
		b, e := Export(d)
		if e != nil {
			return bundle, e
		}
		total += len(b)
		if total > MaxBatchBytes {
			return bundle, fmt.Errorf("export exceeds 64 MiB")
		}
		name := d.Slug + ".md"
		f := ExportFile{name, b, digest(b), len(b)}
		bundle.Files = append(bundle.Files, f)
		if d.ContentHash == "" {
			d.ContentHash = HashDocument(d)
		}
		m.Items = append(m.Items, ManifestItem{name, f.SHA256, f.Bytes, d})
	}
	var e error
	bundle.Manifest, e = json.MarshalIndent(m, "", "  ")
	bundle.Manifest = append(bundle.Manifest, '\n')
	if len(bundle.Manifest) > MaxBatchBytes {
		return ExportBundle{}, fmt.Errorf("manifest exceeds 64 MiB")
	}
	return bundle, e
}

// WriteExportNewDir validates the bundle in memory before creating a new 0700 directory.
// It refuses existing destinations and writes only 0600 files with O_EXCL. On I/O
// failure it leaves a recognizable partial directory for inspection; no recursive delete.
func WriteExportNewDir(directory string, bundle ExportBundle) error {
	if _, e := DocumentsFromExport(bundle); e != nil {
		return e
	}
	abs, e := filepath.Abs(directory)
	if e != nil {
		return e
	}
	parent, e := rejectAncestorLinks(filepath.Dir(abs))
	if e != nil {
		return e
	}
	base := filepath.Base(abs)
	if validPath(base) != nil {
		return fmt.Errorf("unsafe destination name")
	}
	r, e := os.OpenRoot(parent)
	if e != nil {
		return e
	}
	defer r.Close()
	if e = r.Mkdir(base, 0700); e != nil {
		return fmt.Errorf("new destination required: %w", e)
	}
	sub, e := r.OpenRoot(base)
	if e != nil {
		return e
	}
	defer sub.Close()
	write := func(name string, b []byte) error {
		f, e := sub.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		return closeErr
	}
	for _, f := range bundle.Files {
		if e = write(f.Path, f.Data); e != nil {
			return e
		}
	}
	return write("manifest.json", bundle.Manifest)
}

// DocumentsFromExport verifies a memory bundle and recovers full retained metadata.
// Plain Markdown re-import intentionally cannot recover fields held only in manifest.
func DocumentsFromExport(bundle ExportBundle) ([]Document, error) {
	if len(bundle.Files) > MaxDocuments || len(bundle.Manifest) > MaxBatchBytes {
		return nil, fmt.Errorf("export bundle too large")
	}
	var m ExportManifest
	if e := json.Unmarshal(bundle.Manifest, &m); e != nil {
		return nil, fmt.Errorf("invalid manifest")
	}
	if m.Format != "folio-markdown-export" || m.Version != Version || len(m.Items) != len(bundle.Files) {
		return nil, fmt.Errorf("manifest does not match files")
	}
	seen := map[string]bool{}
	total := len(bundle.Manifest)
	documents := []Document{}
	for i, f := range bundle.Files {
		if validPath(f.Path) != nil || strings.Contains(f.Path, "/") || f.Path == "manifest.json" || seen[strings.ToLower(f.Path)] || f.SHA256 != digest(f.Data) || f.Bytes != len(f.Data) {
			return nil, fmt.Errorf("invalid export file")
		}
		seen[strings.ToLower(f.Path)] = true
		item := m.Items[i]
		if item.Path != f.Path || item.Path != item.Source.Slug+".md" || item.SHA256 != f.SHA256 || item.Bytes != f.Bytes {
			return nil, fmt.Errorf("manifest/file mismatch")
		}
		if e := ValidateDocument(item.Source); e != nil {
			return nil, e
		}
		expected, e := Export(item.Source)
		if e != nil || string(expected) != string(f.Data) {
			return nil, fmt.Errorf("manifest source/file mismatch")
		}
		total += len(f.Data)
		if total > 2*MaxBatchBytes {
			return nil, fmt.Errorf("export bundle too large")
		}
		documents = append(documents, item.Source)
	}
	return documents, nil
}
