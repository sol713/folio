package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDataPathWithURLReservedCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes ? # & 中文")
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := create(t, s, "path-test")
	s.Close()
	if _, err := os.Stat(filepath.Join(dir, "folio.db")); err != nil {
		t.Fatal("database not in requested directory", err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := getPost(t, call(t, s, "posts.get", map[string]any{"id": p.ID}))
	if got.ID != p.ID {
		t.Fatal("record did not persist in requested directory")
	}
}
