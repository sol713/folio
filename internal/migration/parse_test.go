package migration

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func parseOK(t *testing.T, path, body string) Document {
	t.Helper()
	d, e := Parse(path, []byte(body))
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestFixtures(t *testing.T) {
	for _, name := range []string{"hugo-yaml.md", "hugo-toml.md", "obsidian-yaml.md"} {
		t.Run(name, func(t *testing.T) {
			b, e := os.ReadFile("testdata/" + name)
			if e != nil {
				t.Fatal(e)
			}
			d, e := Parse(name, b)
			if e != nil {
				t.Fatal(e)
			}
			if d.Title == "" || d.Slug == "" || d.ContentHash != HashDocument(d) {
				t.Fatal(d)
			}
			if strings.HasPrefix(name, "hugo") {
				if d.Date == "" {
					t.Fatal("lost date")
				}
			}
		})
	}
}
func TestParseBOMCRLFChinese(t *testing.T) {
	d := parseOK(t, "中文.md", "\ufeff---\r\ntitle: '中文标题'\r\ntags: [\"#不是注释\", 'a,b', 'it''s']\r\n---\r\n# 标题\r\n正文\r\n")
	if d.Title != "中文标题" || strings.Contains(d.Markdown, "\r") || d.Tags[2] != "it's" || !strings.HasPrefix(d.Slug, "post-") {
		t.Fatal(d)
	}
}
func TestFilenameAndHeadingInference(t *testing.T) {
	d := parseOK(t, "folder/Hello World.md", "```md\n# Wrong\n```\n# 正确标题 ##\nText")
	if d.Title != "正确标题" || d.Slug != "hello-world" {
		t.Fatal(d)
	}
	x := parseOK(t, "essays/index.md", "body")
	if x.Slug != "essays" {
		t.Fatal(x)
	}
}
func TestDateNormalizesUTCAndDoesNotPublish(t *testing.T) {
	d := parseOK(t, "x.md", "---\ntitle: X\ndate: 2024-02-02T04:14:54-08:00\npublishDate: 2024-02-04\ndraft: false\n---\nBody")
	if d.Date != "2024-02-02T12:14:54Z" || d.PublishDate != "2024-02-04T00:00:00Z" || d.Draft || len(d.Warnings) < 3 {
		t.Fatal(d)
	}
}
func TestUnknownMetadataRetained(t *testing.T) {
	d := parseOK(t, "x.md", "---\ntitle: X\naliases: ['/old/', '/before/']\nweight: 10\n---\nBody")
	if d.Extra["weight"] != "10" || d.Extra["aliases"] != "['/old/', '/before/']" {
		t.Fatal(d)
	}
}
func TestArraysQuotesAndComments(t *testing.T) {
	d := parseOK(t, "x.md", "---\ntitle: \"Hello # world\" # outside\ntags: [\"a,b\", '中文', tag] # list\n---\nBody")
	if d.Title != "Hello # world" || len(d.Tags) != 3 || d.Tags[0] != "a,b" {
		t.Fatal(d)
	}
}
func TestTOMLMultilineArray(t *testing.T) {
	d := parseOK(t, "x.md", "+++\ntitle = '中文'\ntags = [\n  'one', # first\n  \"two\",\n]\n+++\nBody")
	if len(d.Tags) != 2 {
		t.Fatal(d)
	}
}
func TestDraftDefaultsTrue(t *testing.T) {
	if !parseOK(t, "x.md", "# Hello\nBody").Draft {
		t.Fatal("unsafe default")
	}
}

func TestIndentedDelimiterInsideBlockScalar(t *testing.T) {
	d := parseOK(t, "x.md", "---\ntitle: X\ndescription: |-\n  before\n  ---\n  after\n---\nBody")
	if d.Excerpt != "before\n---\nafter" || d.Markdown != "Body" {
		t.Fatal(d)
	}
}

func TestCategoryAliasesDisagree(t *testing.T) {
	if _, e := Parse("x.md", []byte("---\ntitle: X\ncategory: one\ncategories: [two]\n---\nBody")); e == nil {
		t.Fatal("category replacement was silently accepted")
	}
}

func TestInvalidSingleQuoteRejected(t *testing.T) {
	if _, e := Parse("x.md", []byte("---\ntitle: 'one' trailing '\n---\nBody")); e == nil {
		t.Fatal("malformed single quote accepted")
	}
}
func TestMissingFinalNewlinePreserved(t *testing.T) {
	d := parseOK(t, "x.md", "---\ntitle: X\n---\nBody")
	if d.Markdown != "Body" {
		t.Fatal(d.Markdown)
	}
}
func TestHashIgnoresPathWarningsButNotMetadata(t *testing.T) {
	a := parseOK(t, "x.md", "---\nslug: fixed\ntitle: X\n---\nBody")
	b := a
	b.SourcePath = "other.md"
	b.Warnings = []string{"warning"}
	if HashDocument(a) != HashDocument(b) {
		t.Fatal("presentation changed hash")
	}
	b.Draft = false
	if HashDocument(a) == HashDocument(b) {
		t.Fatal("source intent not fingerprinted")
	}
}
func TestParserRejects(t *testing.T) {
	cases := []struct{ name, body string }{
		{"duplicate", "---\ntitle: X\nTitle: Y\n---\nbody"},
		{"nested", "---\nparams:\n  author: Bob\n---\nbody"},
		{"anchor", "---\ntitle: &value X\n---\nbody"},
		{"alias", "---\ntitle: *value\n---\nbody"},
		{"unclosed", "---\ntitle: X\nbody"},
		{"badDate", "---\ntitle: X\ndate: yesterday\n---\nbody"},
		{"badDraft", "---\ntitle: X\ndraft: 'true'\n---\nbody"},
		{"tagBool", "---\ntitle: X\ntags: [false]\n---\nbody"},
		{"tagScalar", "---\ntitle: X\ntags: tag\n---\nbody"},
		{"categoryMany", "---\ntitle: X\ncategories: [one,two]\n---\nbody"},
		{"excerptConflict", "---\ntitle: X\ndescription: first\nexcerpt: second\n---\nbody"},
		{"remoteCover", "---\ntitle: X\ncover: https://example.invalid/image.png\n---\nbody"},
		{"slugTraversal", "---\ntitle: X\nslug: ../../bad\n---\nbody"},
		{"slugUnicode", "---\ntitle: X\nslug: 中文\n---\nbody"},
		{"nullByte", "body\x00secret"},
		{"invalidUTF8", string([]byte{0xff})},
		{"tomlTable", "+++\ntitle='X'\n[params]\nauthor='Bob'\n+++\nbody"},
		{"unquotedTOML", "+++\ntitle = Hello world\n+++\nbody"},
		{"badQuote", "---\ntitle: \"X\n---\nbody"},
		{"nestedArray", "---\ntitle: X\ntags: [[one]]\n---\nbody"},
		{"overTitle", "---\ntitle: " + strings.Repeat("中", 201) + "\n---\nbody"},
		{"overMarkdown", strings.Repeat("x", MaxMarkdownBytes+1)},
		{"overFrontmatter", "---\ntitle: X\nunknown: " + strings.Repeat("x", 64<<10) + "\n---\nbody"},
		{"emptyArrayItem", "---\ntitle: X\ntags: [a,,b]\n---\nbody"},
		{"invalidBool", "---\ntitle: X\nfeatured: yes\n---\nbody"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, e := Parse("x.md", []byte(c.body)); e == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}
func TestUnsafeSourcePaths(t *testing.T) {
	for _, p := range []string{"../x.md", "/x.md", "a/../x.md", "a\\x.md", "C:x.md", "a//x.md", "./x.md", "x\x00.md", "a. /x.md", "."} {
		t.Run(p, func(t *testing.T) {
			if _, e := Parse(p, []byte("Body")); e == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}
func FuzzParse(f *testing.F) {
	f.Add([]byte("---\ntitle: 你好\n---\n# hello\n"))
	f.Add([]byte("plain body"))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxFileBytes {
			return
		}
		d, e := Parse("fuzz.md", b)
		if e != nil {
			return
		}
		out, e := Export(d)
		if e != nil {
			return
		}
		parsed, e := Parse("fuzz.md", out)
		if e != nil {
			t.Fatalf("export not parseable: %v", e)
		}
		if !bytes.Equal([]byte(d.Markdown), []byte(parsed.Markdown)) {
			t.Fatal("body changed")
		}
	})
}
