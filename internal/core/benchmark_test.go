package core

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BenchmarkPersonalSite measures one small personal-blog fixture: 100 posts
// (90 live), roughly 4 KiB of Markdown per post, and 10 embedded PNGs. It is
// intentionally not a scalability benchmark. Run with -benchtime=10x because
// updates retain revision history and therefore grow the fixture over time.
func BenchmarkPersonalSite(b *testing.B) {
	dir := b.TempDir()
	s, e := Open(dir)
	if e != nil {
		b.Fatal(e)
	}
	defer s.Close()
	invoke := func(op string, args any) any {
		raw, e := json.Marshal(args)
		if e != nil {
			b.Fatal(e)
		}
		v, e := s.Call(context.Background(), op, raw, "benchmark")
		if e != nil {
			b.Fatalf("%s: %v", op, e)
		}
		return v
	}
	markdown := strings.Repeat("A considered observation about everyday life, with [a link](https://example.com), **emphasis**, and room to think.\n\n", 36)
	var target Post
	for i := range 100 {
		v := invoke("posts.create", map[string]any{"title": fmt.Sprintf("Personal journal entry %d", i), "slug": fmt.Sprintf("journal-entry-%d", i), "markdown": markdown, "excerpt": "A short summary", "tags": []string{"journal", "ideas"}})
		raw, _ := json.Marshal(v)
		var out struct {
			Post Post `json:"post"`
		}
		json.Unmarshal(raw, &out)
		if i < 90 {
			invoke("posts.publish", map[string]any{"id": out.Post.ID, "expected_revision": 1, "confirm": true})
		}
		if i == 99 {
			target = out.Post
		}
	}
	for i := range 10 {
		im := image.NewNRGBA(image.Rect(0, 0, 128, 128))
		seed := uint32(i + 1)
		for j := range im.Pix {
			seed = 1664525*seed + 1013904223
			im.Pix[j] = byte(seed >> 24)
		}
		var buf bytes.Buffer
		if e := png.Encode(&buf, im); e != nil {
			b.Fatal(e)
		}
		invoke("media.upload", map[string]any{"name": fmt.Sprintf("fixture-%d.png", i), "base64": base64.StdEncoding.EncodeToString(buf.Bytes())})
	}
	diskBytes := func() float64 {
		var n int64
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if info, e := os.Stat(filepath.Join(dir, "folio.db"+suffix)); e == nil {
				n += info.Size()
			}
		}
		return float64(n)
	}
	fixtureDisk := diskBytes()
	b.Logf("fixture: 100 posts (90 live), %d Markdown bytes each, 10 PNG assets; SQLite+WAL+SHM %.2f MiB", len(markdown), fixtureDisk/(1024*1024))
	b.Run("PublicSnapshot", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			_, posts := s.Public()
			if len(posts) != 90 {
				b.Fatal("wrong public count")
			}
		}
		b.ReportMetric(fixtureDisk, "fixture-bytes")
	})
	b.Run("PreviewOne", func(b *testing.B) {
		raw, _ := json.Marshal(map[string]any{"id": target.ID})
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, e := s.Call(context.Background(), "posts.preview", raw, "benchmark"); e != nil {
				b.Fatal(e)
			}
		}
	})
	b.Run("UpdateDraft", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			raw, _ := json.Marshal(map[string]any{"id": target.ID, "expected_revision": target.Revision, "title": target.Title, "slug": target.Slug, "markdown": markdown})
			if _, e := s.Call(context.Background(), "posts.update", raw, "benchmark"); e != nil {
				b.Fatal(e)
			}
			target.Revision++
		}
	})
	b.Run("ExportBackup", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			if _, e := s.Call(context.Background(), "backup.export", json.RawMessage(`{}`), "benchmark"); e != nil {
				b.Fatal(e)
			}
		}
	})
}
