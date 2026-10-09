package migration

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
	"unicode/utf8"
)

func validPath(p string) error {
	if p == "" || !utf8.ValidString(p) || !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\:") {
		return fmt.Errorf("unsafe relative source path")
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return fmt.Errorf("control character in path")
		}
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return fmt.Errorf("nonportable path segment")
		}
	}
	return nil
}

// rejectAncestorLinks accepts only the two fixed macOS filesystem aliases.
// Caller-supplied symbolic links at or beneath the root are always rejected.
func rejectAncestorLinks(p string) (string, error) {
	var err error
	p, err = filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" && (strings.HasPrefix(p, "/tmp/") || p == "/tmp") {
		p = "/private" + p
	}
	if runtime.GOOS == "darwin" && (strings.HasPrefix(p, "/var/") || p == "/var") {
		p = "/private" + p
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(p, current), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink ancestor refused")
		}
	}
	return p, nil
}

func readSafe(root *os.Root, p string) ([]byte, error) {
	if e := validPath(p); e != nil {
		return nil, e
	}
	for i := range strings.Split(p, "/") {
		prefix := strings.Join(strings.Split(p, "/")[:i+1], "/")
		info, e := root.Lstat(prefix)
		if e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink refused")
		}
	}
	before, e := root.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("non-regular file refused")
	}
	if before.Size() > MaxFileBytes {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	f, e := root.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil {
		return nil, e
	}
	after, e := root.Lstat(p)
	if e != nil {
		return nil, e
	}
	if after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
		return nil, fmt.Errorf("file changed during safe open")
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if e != nil {
		return nil, e
	}
	if len(b) > MaxFileBytes {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	return b, nil
}

// ScanDirectory returns every per-file parse error, allowing users to fix and re-plan.
// A root error is fatal. Walk errors and unsafe entries block Apply via diagnostics.
func ScanDirectory(directory string) ([]Document, []Diagnostic, error) {
	dir, e := rejectAncestorLinks(directory)
	if e != nil {
		return nil, nil, e
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, nil, e
	}
	defer r.Close()
	docs := []Document{}
	diagnostics := []Diagnostic{}
	total := 0
	visited := 0
	e = fs.WalkDir(r.FS(), ".", func(p string, entry fs.DirEntry, walkErr error) error {
		visited++
		if visited > MaxDocuments*10 || len(diagnostics) >= MaxDocuments {
			return fmt.Errorf("directory scan entry limit exceeded")
		}
		if walkErr != nil {
			diagnostics = append(diagnostics, Diagnostic{p, "read_error", "Unable to read entry"})
			return nil
		}
		if p == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			diagnostics = append(diagnostics, Diagnostic{p, "unsafe_path", "Symlink refused"})
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if strings.ToLower(path.Ext(p)) != ".md" && strings.ToLower(path.Ext(p)) != ".markdown" {
			return nil
		}
		if len(docs)+len(diagnostics) >= MaxDocuments {
			return fmt.Errorf("directory exceeds %d document/diagnostic entries", MaxDocuments)
		}
		b, err := readSafe(r, p)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{p, "read_error", err.Error()})
			return nil
		}
		total += len(b)
		if total > MaxBatchBytes {
			return fmt.Errorf("directory exceeds 64 MiB input limit")
		}
		d, err := Parse(p, b)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{p, "parse_error", err.Error()})
			return nil
		}
		docs = append(docs, d)
		return nil
	})
	if e != nil {
		return nil, nil, e
	}
	return docs, diagnostics, nil
}

func PlanDirectory(directory string, existing []Existing, options Options) (ImportPlan, error) {
	docs, diagnostics, e := ScanDirectory(directory)
	if e != nil {
		return ImportPlan{}, e
	}
	p, e := Plan(docs, existing, options)
	if e != nil {
		return p, e
	}
	p.Diagnostics = append(p.Diagnostics, diagnostics...)
	sealPlan(&p)
	return p, nil
}
