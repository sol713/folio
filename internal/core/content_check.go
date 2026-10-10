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

const checkFindingLimit = 100
const checkReferenceLimit = 2000

// Findings describe an authorized snapshot, never a fetch of a referenced URL.
type ContentFinding struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Field     string `json:"field"`
	Line      int    `json:"line,omitempty"`
	Precision string `json:"location_precision"`
	Reference string `json:"reference,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type ContentCheck struct {
	ID               string           `json:"id"`
	Revision         int              `json:"revision"`
	InstanceID       string           `json:"instance_id"`
	InstanceRevision int              `json:"instance_revision"`
	CanonicalURL     string           `json:"canonical_url"`
	Findings         []ContentFinding `json:"findings"`
	Counts           map[string]int   `json:"counts"`
	Limits           []string         `json:"limitations"`
	Truncated        bool             `json:"truncated"`
}

var checkMessages = map[string]string{
	"article_missing":     "No public article or current private draft matches this article URL.",
	"article_private":     "This URL points to a private draft, not a public article. Publish the target or use its live URL.",
	"media_missing":       "This local media object is missing from this instance.",
	"excerpt_missing":     "The article summary is empty. Consider a short plain-text introduction.",
	"reference_unchecked": "This reference is outside the supported route or encoding range; existence was not checked.",
}

func (s *Store) checkContent(raw []byte) (any, error) {
	var a struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	if e := decode(raw, &a); e != nil {
		return nil, e
	}
	if a.ID == "" || len(a.ID) > 128 || a.Revision < 1 {
		return nil, Err("validation", "Content check requires a post ID and positive current draft revision")
	}
	r, e := s.record(a.ID)
	if e != nil {
		return nil, e
	}
	if e = checkRevision(r, a.Revision); e != nil {
		return nil, e
	}
	return analyzeContent(r.Draft, s.State, s.instanceID), nil
}

// analyzeContent reads only the in-memory catalog refreshed by Store.Call's
// transaction. It performs no network calls, filesystem probes or HTML execution.
func analyzeContent(p Post, state State, instance string) ContentCheck {
	base, _ := url.Parse(strings.TrimRight(state.Settings.BaseURL, "/") + "/posts/" + p.Slug)
	out := ContentCheck{ID: p.ID, Revision: p.Revision, InstanceID: instance, InstanceRevision: state.Revision,
		CanonicalURL: base.String(), Findings: []ContentFinding{}, Counts: map[string]int{},
		Limits: []string{"saved_current_revision_only", "catalog_point_in_time", "canonical_origin_and_routes_only", "anchors_not_checked", "external_urls_not_fetched", "raw_html_and_code_not_checked", "self_url_assumed_after_publish", "bounded_references_and_findings"}}
	public, drafts := map[string]bool{}, map[string]string{}
	for id, r := range state.Posts {
		if r.Deleted {
			continue
		}
		drafts[r.Draft.Slug] = id
		if r.Live != nil {
			public[r.Live.Slug] = true
			for _, old := range r.OldSlugs {
				public[old] = true // Matches Redirect: public current routes take precedence.
			}
		}
	}
	add := func(code, field, ref, reason, precision string, line int) {
		severity := "warning"
		if code == "excerpt_missing" || code == "reference_unchecked" {
			severity = "info"
		}
		out.Counts[severity]++
		if len(out.Findings) >= checkFindingLimit {
			out.Truncated = true
			return
		}
		// A diagnostic is bounded even when a supplied destination is enormous.
		if len(ref) > 512 {
			ref = string(bytes.ToValidUTF8([]byte(ref[:512]), nil)) + "…"
		}
		out.Findings = append(out.Findings, ContentFinding{code, severity, checkMessages[code], field, line, precision, ref, reason})
	}
	checkURL := func(destination []byte, image bool, field, precision string, line int) {
		out.Counts["references"]++
		if out.Counts["references"] > checkReferenceLimit {
			out.Truncated = true
			return
		}
		rendered := string(util.URLEscape(destination, true)) // Same decoding as Goldmark's link/image renderer.
		if rendered == "" {
			return
		}
		u, err := url.Parse(rendered)
		if err != nil || len(rendered) > 4096 || strings.Contains(rendered, "\\") {
			add("reference_unchecked", field, rendered, "ambiguous_or_oversized_url", precision, line)
			return
		}
		if u.Opaque != "" || u.User != nil || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
			out.Counts["other_urls_not_checked"]++
			return
		}
		resolved := base.ResolveReference(u)
		if !sameCheckOrigin(base, resolved) {
			out.Counts["external_urls_not_checked"]++
			return
		}
		if resolved.Fragment != "" {
			out.Counts["anchors_not_checked"]++
		}
		if u.Path == "" && u.Host == "" { // Fragment/query-only links refer to this future article.
			out.Counts["self_urls_assumed_after_publish"]++
			return
		}
		escaped := strings.ToLower(u.EscapedPath())
		ambiguous := strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c")
		for _, segment := range strings.Split(u.Path, "/") {
			if (segment == "." || segment == "..") && strings.Contains(escaped, "%2e") {
				ambiguous = true // Browsers normalize encoded dot segments differently from net/url.
			}
		}
		path := resolved.Path
		if strings.HasPrefix(path, "/posts/") && !image {
			slug := strings.TrimPrefix(path, "/posts/")
			if ambiguous || len(slug) > 160 || !slugRE.MatchString(slug) {
				add("reference_unchecked", field, rendered, "noncanonical_article_path", precision, line)
				return
			}
			out.Counts["article_urls_checked"]++
			if public[slug] {
				return
			}
			if drafts[slug] == p.ID {
				out.Counts["self_urls_assumed_after_publish"]++
				return
			}
			if drafts[slug] != "" {
				add("article_private", field, rendered, "draft_only_url", precision, line)
			} else {
				add("article_missing", field, rendered, "no_matching_article", precision, line)
			}
			return
		}
		if strings.HasPrefix(path, "/media/") {
			key := strings.TrimPrefix(path, "/media/")
			if ambiguous || key == "" || strings.ContainsAny(key, "/\\") || strings.Contains(key, "..") {
				add("reference_unchecked", field, rendered, "noncanonical_media_path", precision, line)
				return
			}
			out.Counts["media_urls_checked"]++
			if _, ok := state.Media[key]; !ok {
				add("media_missing", field, rendered, "no_matching_media_object", precision, line)
			}
			return
		}
		out.Counts["other_urls_not_checked"]++
	}
	if strings.TrimSpace(p.Excerpt) == "" {
		add("excerpt_missing", "excerpt", "", "empty_summary", "field", 0)
	}
	if p.Cover != "" {
		checkURL([]byte(p.Cover), true, "cover", "field", 0)
	}
	source := []byte(p.Markdown)
	document := md.Parser().Parse(text.NewReader(source))
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
			if out.Counts["references"] >= checkReferenceLimit {
				out.Truncated = true
				return ast.WalkStop, nil
			}
			line, precision := contentLocation(n, source)
			checkURL(destination, image, "markdown", precision, line)
		}
		if image {
			// Image labels become plain alt text, not navigable nested links.
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	sort.SliceStable(out.Findings, func(i, j int) bool {
		return out.Findings[i].Severity == "warning" && out.Findings[j].Severity != "warning"
	})
	return out
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
