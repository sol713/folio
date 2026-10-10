package core

import (
	"bytes"
	"net/url"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

type markdownReference struct {
	Destination []byte
	Image       bool
	Line        int
	Precision   string
}

// Walk the same Goldmark AST used by rendering. Labels inside images are plain
// alt text, while code and raw HTML never become references in this walker.
func walkContentReferences(markdown string, limit int, visit func(markdownReference)) (int, bool) {
	source := []byte(markdown)
	document := md.Parser().Parse(text.NewReader(source))
	used, truncated := 0, false
	_ = ast.Walk(document, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var destination []byte
		image := false
		switch node := n.(type) {
		case *ast.Link:
			destination = node.Destination
		case *ast.Image:
			destination, image = node.Destination, true
		case *ast.AutoLink:
			if node.AutoLinkType == ast.AutoLinkURL {
				destination = node.URL(source)
			}
		}
		if destination != nil {
			if used >= limit {
				truncated = true
				return ast.WalkStop, nil
			}
			used++
			line, precision := contentLocation(n, source)
			visit(markdownReference{destination, image, line, precision})
		}
		if image {
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return used, truncated
}

type contentDestination struct {
	Kind      string
	Reference string
	Slug      string
	MediaKey  string
	Reason    string
	Anchor    bool
}

func contentCanonicalURL(baseURL string, p Post) *url.URL {
	u, _ := url.Parse(strings.TrimRight(baseURL, "/") + "/posts/" + p.Slug)
	return u
}

// This classifies a rendered destination; it never fetches or probes it.
func resolveContentDestination(base *url.URL, destination []byte, image bool) contentDestination {
	rendered := string(util.URLEscape(destination, true))
	out := contentDestination{Reference: rendered}
	if rendered == "" {
		out.Kind = "empty"
		return out
	}
	u, err := url.Parse(rendered)
	if err != nil || len(rendered) > 4096 || strings.Contains(rendered, "\\") {
		out.Kind, out.Reason = "unchecked", "ambiguous_or_oversized_url"
		return out
	}
	if u.Opaque != "" || u.User != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
		out.Kind = "other"
		return out
	}
	resolved := base.ResolveReference(u)
	if !sameCheckOrigin(base, resolved) {
		out.Kind = "external"
		return out
	}
	out.Anchor = resolved.Fragment != ""
	if u.Path == "" && u.Host == "" {
		out.Kind, out.Slug = "self", strings.TrimPrefix(base.Path, "/posts/")
		return out
	}
	escaped := strings.ToLower(u.EscapedPath())
	ambiguous := strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c")
	for _, segment := range strings.Split(u.Path, "/") {
		if (segment == "." || segment == "..") && strings.Contains(escaped, "%2e") {
			ambiguous = true
		}
	}
	path := resolved.Path
	if strings.HasPrefix(path, "/posts/") && !image {
		slug := strings.TrimPrefix(path, "/posts/")
		if ambiguous || len(slug) > 160 || !slugRE.MatchString(slug) {
			out.Kind, out.Reason = "unchecked", "noncanonical_article_path"
			return out
		}
		out.Kind, out.Slug = "article", slug
		return out
	}
	if strings.HasPrefix(path, "/media/") {
		key := strings.TrimPrefix(path, "/media/")
		if ambiguous || key == "" || strings.ContainsAny(key, "/\\") || strings.Contains(key, "..") {
			out.Kind, out.Reason = "unchecked", "noncanonical_media_path"
			return out
		}
		out.Kind, out.MediaKey = "media", key
		return out
	}
	out.Kind = "other"
	return out
}

type contentCatalog struct {
	IDs     []string
	Live    map[string]string
	Aliases map[string][]string
	Drafts  map[string]string
}

func indexContentCatalog(state State) contentCatalog {
	c := contentCatalog{IDs: []string{}, Live: map[string]string{}, Aliases: map[string][]string{}, Drafts: map[string]string{}}
	for id, r := range state.Posts {
		if !r.Deleted {
			c.IDs = append(c.IDs, id)
		}
	}
	sort.Strings(c.IDs)
	for _, id := range c.IDs {
		r := state.Posts[id]
		c.Drafts[r.Draft.Slug] = id
		if r.Live == nil {
			continue
		}
		c.Live[r.Live.Slug] = id
		for _, old := range r.OldSlugs {
			ids := c.Aliases[old]
			if len(ids) == 0 || ids[len(ids)-1] != id {
				c.Aliases[old] = append(ids, id)
			}
		}
	}
	return c
}
func (c contentCatalog) hasPublic(slug string) bool {
	return c.Live[slug] != "" || len(c.Aliases[slug]) > 0
}
func (c contentCatalog) publicTarget(slug string) (string, string) {
	if id := c.Live[slug]; id != "" {
		return id, "live"
	}
	ids := c.Aliases[slug]
	if len(ids) == 1 {
		return ids[0], "redirect"
	}
	if len(ids) > 1 {
		return "", "ambiguous_redirect"
	}
	return "", ""
}
func boundedContentReference(ref string) string {
	if len(ref) > 512 {
		return string(bytes.ToValidUTF8([]byte(ref[:512]), nil)) + "…"
	}
	return ref
}
func sameCheckOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
func contentLocation(n ast.Node, source []byte) (int, string) {
	precision := "line"
	for node := n; node != nil; node = node.Parent() {
		if pos := node.Pos(); pos >= 0 && pos <= len(source) {
			return bytes.Count(source[:pos], []byte{'\n'}) + 1, precision
		}
		precision = "block"
	}
	return 1, "block"
}
