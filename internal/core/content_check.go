package core

import (
	"sort"
	"strings"
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
	base := contentCanonicalURL(state.Settings.BaseURL, p)
	out := ContentCheck{ID: p.ID, Revision: p.Revision, InstanceID: instance, InstanceRevision: state.Revision,
		CanonicalURL: base.String(), Findings: []ContentFinding{}, Counts: map[string]int{},
		Limits: []string{"saved_current_revision_only", "catalog_point_in_time", "canonical_origin_and_routes_only", "anchors_not_checked", "external_urls_not_fetched", "raw_html_and_code_not_checked", "self_url_assumed_after_publish", "bounded_references_and_findings"}}
	catalog := indexContentCatalog(state)
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
		ref = boundedContentReference(ref)
		out.Findings = append(out.Findings, ContentFinding{code, severity, checkMessages[code], field, line, precision, ref, reason})
	}
	checkURL := func(destination []byte, image bool, field, precision string, line int) {
		out.Counts["references"]++
		if out.Counts["references"] > checkReferenceLimit {
			out.Truncated = true
			return
		}
		d := resolveContentDestination(base, destination, image)
		if d.Anchor {
			out.Counts["anchors_not_checked"]++
		}
		switch d.Kind {
		case "empty":
			return
		case "unchecked":
			add("reference_unchecked", field, d.Reference, d.Reason, precision, line)
		case "external":
			out.Counts["external_urls_not_checked"]++
		case "self":
			out.Counts["self_urls_assumed_after_publish"]++
		case "article":
			out.Counts["article_urls_checked"]++
			if catalog.hasPublic(d.Slug) {
				return
			}
			if catalog.Drafts[d.Slug] == p.ID {
				out.Counts["self_urls_assumed_after_publish"]++
				return
			}
			if catalog.Drafts[d.Slug] != "" {
				add("article_private", field, d.Reference, "draft_only_url", precision, line)
			} else {
				add("article_missing", field, d.Reference, "no_matching_article", precision, line)
			}
		case "media":
			out.Counts["media_urls_checked"]++
			if _, ok := state.Media[d.MediaKey]; !ok {
				add("media_missing", field, d.Reference, "no_matching_media_object", precision, line)
			}
		default:
			out.Counts["other_urls_not_checked"]++
		}
	}
	if strings.TrimSpace(p.Excerpt) == "" {
		add("excerpt_missing", "excerpt", "", "empty_summary", "field", 0)
	}
	if p.Cover != "" {
		checkURL([]byte(p.Cover), true, "cover", "field", 0)
	}
	_, truncated := walkContentReferences(p.Markdown, max(0, checkReferenceLimit-out.Counts["references"]), func(ref markdownReference) { checkURL(ref.Destination, ref.Image, "markdown", ref.Precision, ref.Line) })
	out.Truncated = out.Truncated || truncated
	sort.SliceStable(out.Findings, func(i, j int) bool {
		return out.Findings[i].Severity == "warning" && out.Findings[j].Severity != "warning"
	})
	return out
}
