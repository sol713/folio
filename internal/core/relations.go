package core

import "sort"

const relationEdgeLimit = 100
const relationLocationLimit = 3
const relationSnapshotLimit = 1000
const relationByteLimit = 8 * 1024 * 1024
const relationReferenceLimit = 20000

type RelationArticle struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	Slug              string `json:"slug"`
	Kind              string `json:"kind"`
	Revision          int    `json:"revision"`
	DraftRevision     int    `json:"draft_revision"`
	PublishedRevision int    `json:"published_revision"`
	Status            string `json:"status"`
}
type RelationLocation struct {
	Line      int    `json:"line"`
	Precision string `json:"location_precision"`
	Reference string `json:"reference"`
	Route     string `json:"route"`
}
type ArticleRelation struct {
	Source             RelationArticle    `json:"source"`
	Target             *RelationArticle   `json:"target"`
	Slug               string             `json:"referenced_slug"`
	Availability       string             `json:"availability"`
	Count              int                `json:"count"`
	Locations          []RelationLocation `json:"locations"`
	LocationsTruncated bool               `json:"locations_truncated"`
	Self               bool               `json:"self"`
}
type RelationSnapshot struct {
	Article      RelationArticle   `json:"article"`
	CanonicalURL string            `json:"canonical_url"`
	Outgoing     []ArticleRelation `json:"outgoing"`
	Counts       map[string]int    `json:"counts"`
	Truncated    bool              `json:"truncated"`
}
type ArticleRelations struct {
	ID                string            `json:"id"`
	Revision          int               `json:"revision"`
	PublishedRevision int               `json:"published_revision"`
	InstanceID        string            `json:"instance_id"`
	InstanceRevision  int               `json:"instance_revision"`
	Draft             RelationSnapshot  `json:"saved_draft"`
	Published         *RelationSnapshot `json:"published"`
	IncomingDraft     []ArticleRelation `json:"incoming_saved_drafts"`
	IncomingPublished []ArticleRelation `json:"incoming_published"`
	Counts            map[string]int    `json:"counts"`
	Limits            []string          `json:"limitations"`
	Truncated         bool              `json:"truncated"`
}

func (s *Store) articleRelations(raw []byte) (any, error) {
	var a struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
	}
	if e := decode(raw, &a); e != nil {
		return nil, e
	}
	if a.ID == "" || len(a.ID) > 128 || a.Revision < 1 {
		return nil, Err("validation", "Article relations require a post ID and positive current draft revision")
	}
	r, e := s.record(a.ID)
	if e != nil {
		return nil, e
	}
	if e = checkRevision(r, a.Revision); e != nil {
		return nil, e
	}
	return analyzeRelations(r, s.State, s.instanceID), nil
}
func relationArticle(p Post, r *Record, kind string) RelationArticle {
	live, status := 0, "draft"
	if r.Live != nil {
		live = r.Live.Revision
		status = "published"
		if r.Draft.Revision != live {
			status = "changed"
		}
	}
	return RelationArticle{p.ID, p.Title, p.Slug, kind, p.Revision, r.Draft.Revision, live, status}
}
func relationTarget(d contentDestination, source RelationArticle, c contentCatalog, state State) (*RelationArticle, string, string) {
	if d.Kind == "self" {
		r := state.Posts[source.ID]
		if source.Kind == "published" {
			target := relationArticle(*r.Live, r, "published")
			return &target, "public", "self"
		}
		target := relationArticle(r.Draft, r, "saved_draft")
		return &target, "future_self", "self_future"
	}
	if id, route := c.publicTarget(d.Slug); id != "" {
		r := state.Posts[id]
		target := relationArticle(*r.Live, r, "published")
		return &target, "public", route
	} else if route == "ambiguous_redirect" {
		return nil, "ambiguous", route
	}
	if id := c.Drafts[d.Slug]; id != "" {
		r := state.Posts[id]
		target := relationArticle(r.Draft, r, "saved_draft")
		if id == source.ID && source.Kind == "saved_draft" {
			return &target, "future_self", "self_future"
		}
		return &target, "private", "draft_only"
	}
	return nil, "missing", "missing"
}

type relationBudget struct{ Snapshots, Bytes, References int }

// One authorized snapshot and a bounded one-hop scan; no persistence, recursive
// traversal, external requests, arbitrary paths, or public-reader integration.
func analyzeRelations(current *Record, state State, instance string) ArticleRelations {
	c := indexContentCatalog(state)
	out := ArticleRelations{ID: current.Draft.ID, Revision: current.Draft.Revision, InstanceID: instance, InstanceRevision: state.Revision,
		IncomingDraft: []ArticleRelation{}, IncomingPublished: []ArticleRelation{}, Counts: map[string]int{},
		Limits: []string{"current_saved_and_published_snapshots_only", "catalog_point_in_time", "canonical_origin_and_routes_only", "anchors_not_checked", "external_urls_not_fetched", "raw_html_code_images_not_relations", "one_hop_only", "ambiguous_redirect_targets_not_chosen", "bounded_sources_edges_and_locations", "studio_opens_current_saved_draft"}}
	if current.Live != nil {
		out.PublishedRevision = current.Live.Revision
	}
	for _, id := range c.IDs {
		out.Counts["catalog_snapshots"]++
		if state.Posts[id].Live != nil {
			out.Counts["catalog_snapshots"]++
		}
	}
	budget := relationBudget{relationSnapshotLimit, relationByteLimit, relationReferenceLimit}
	incomingDraft, incomingPublished := []ArticleRelation{}, []ArticleRelation{}
	scan := func(p Post, r *Record, kind string, query bool) *RelationSnapshot {
		if budget.Snapshots <= 0 || budget.References <= 0 || len(p.Markdown) > budget.Bytes {
			return nil
		}
		budget.Snapshots--
		budget.Bytes -= len(p.Markdown)
		out.Counts["scanned_snapshots"]++
		out.Counts["scanned_markdown_bytes"] += len(p.Markdown)
		source := relationArticle(p, r, kind)
		base := contentCanonicalURL(state.Settings.BaseURL, p)
		snap := RelationSnapshot{Article: source, CanonicalURL: base.String(), Outgoing: []ArticleRelation{}, Counts: map[string]int{}}
		grouped := map[string]*ArticleRelation{}
		limit := min(checkReferenceLimit, budget.References)
		used, truncated := walkContentReferences(p.Markdown, limit, func(ref markdownReference) {
			if ref.Image {
				snap.Counts["images_not_relations"]++
				return
			}
			d := resolveContentDestination(base, ref.Destination, false)
			if d.Anchor {
				snap.Counts["anchors_not_checked"]++
			}
			if d.Kind != "article" && d.Kind != "self" {
				snap.Counts[d.Kind+"_not_relations"]++
				return
			}
			snap.Counts["article_references"]++
			target, availability, route := relationTarget(d, source, c, state)
			if target == nil {
				out.Counts["unresolved_references"]++
			}
			if !query && (target == nil || target.ID != out.ID) {
				return
			}
			key := availability + ":" + d.Slug
			if target != nil {
				key = availability + ":" + target.ID
			}
			edge := grouped[key]
			if edge == nil {
				edge = &ArticleRelation{Source: source, Target: target, Slug: d.Slug, Availability: availability, Locations: []RelationLocation{}, Self: target != nil && target.ID == source.ID}
				grouped[key] = edge
			}
			edge.Count++
			if len(edge.Locations) < relationLocationLimit {
				edge.Locations = append(edge.Locations, RelationLocation{ref.Line, ref.Precision, boundedContentReference(d.Reference), route})
			} else {
				edge.LocationsTruncated = true
			}
		})
		snap.Counts["references"] = used
		snap.Truncated = truncated
		out.Truncated = out.Truncated || truncated
		budget.References -= used
		out.Counts["scanned_references"] += used
		keys := make([]string, 0, len(grouped))
		for key := range grouped {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			edge := *grouped[key]
			if query {
				snap.Outgoing = append(snap.Outgoing, edge)
			}
			if edge.Target != nil && edge.Target.ID == out.ID {
				if kind == "saved_draft" {
					incomingDraft = append(incomingDraft, edge)
					out.Counts["incoming_saved_draft_occurrences"] += edge.Count
				} else {
					incomingPublished = append(incomingPublished, edge)
					out.Counts["incoming_published_occurrences"] += edge.Count
				}
			}
		}
		if query {
			snap.Counts["outgoing_groups"] = len(snap.Outgoing)
			if len(snap.Outgoing) > relationEdgeLimit {
				snap.Outgoing = snap.Outgoing[:relationEdgeLimit]
				snap.Truncated = true
				out.Truncated = true
			}
		}
		return &snap
	}
	// Always scan the requested saved/live versions before the incoming catalog.
	out.Draft = *scan(current.Draft, current, "saved_draft", true)
	if current.Live != nil {
		out.Published = scan(*current.Live, current, "published", true)
	}
	for _, id := range c.IDs {
		if id == out.ID {
			continue
		}
		r := state.Posts[id]
		scan(r.Draft, r, "saved_draft", false)
		if r.Live != nil {
			scan(*r.Live, r, "published", false)
		}
	}
	out.Counts["omitted_snapshots"] = out.Counts["catalog_snapshots"] - out.Counts["scanned_snapshots"]
	out.Truncated = out.Truncated || out.Counts["omitted_snapshots"] > 0
	out.Counts["incoming_saved_draft_groups"] = len(incomingDraft)
	out.Counts["incoming_published_groups"] = len(incomingPublished)
	if len(incomingDraft) > relationEdgeLimit {
		incomingDraft = incomingDraft[:relationEdgeLimit]
		out.Truncated = true
	}
	if len(incomingPublished) > relationEdgeLimit {
		incomingPublished = incomingPublished[:relationEdgeLimit]
		out.Truncated = true
	}
	out.IncomingDraft, out.IncomingPublished = incomingDraft, incomingPublished
	return out
}
