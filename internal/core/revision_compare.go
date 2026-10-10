package core

import "reflect"

// Revision comparisons never render Markdown or write state. Snapshots are the
// same eight author-controlled fields used by proposal approval and draft saves.
type ComparedRevision struct {
	Revision  int             `json:"revision"`
	Kind      string          `json:"kind"`
	Current   bool            `json:"current"`
	Published bool            `json:"published"`
	Content   ProposalContent `json:"content"`
}

type RevisionComparison struct {
	ID                string           `json:"id"`
	Revision          int              `json:"revision"`
	PublishedRevision int              `json:"published_revision"`
	InstanceID        string           `json:"instance_id"`
	InstanceRevision  int              `json:"instance_revision"`
	From              ComparedRevision `json:"from"`
	To                ComparedRevision `json:"to"`
	Review            ProposalReview   `json:"review"`
	Restorable        bool             `json:"restorable"`
	RestoreReason     string           `json:"restore_reason"`
	Limits            []string         `json:"limitations"`
}

func comparedRevision(r *Record, revision int) (ComparedRevision, bool, error) {
	p, kind, inHistory := r.Draft, "saved_draft", false
	found := revision == r.Draft.Revision
	for _, item := range r.Revisions {
		if item.Revision == revision {
			inHistory = true
			if !found {
				p, kind, found = item, "historical_revision", true
			}
			break
		}
	}
	// Sparse imported history may retain the live snapshot outside the revision
	// list. It can be compared, but posts.restore cannot restore that missing row.
	if !found && r.Live != nil && revision == r.Live.Revision {
		p, kind, found = *r.Live, "published_snapshot", true
	}
	if !found {
		return ComparedRevision{}, false, Err("not_found", "Revision not found")
	}
	return ComparedRevision{revision, kind, revision == r.Draft.Revision, r.Live != nil && revision == r.Live.Revision, proposalContent(p)}, inHistory, nil
}

func (s *Store) compareRevisions(raw []byte) (any, error) {
	var a struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
		From     int    `json:"from_revision"`
		To       int    `json:"to_revision"`
	}
	if e := decode(raw, &a); e != nil {
		return nil, e
	}
	if a.ID == "" || len(a.ID) > 128 || a.Revision < 1 || a.From < 1 || a.To < 1 {
		return nil, Err("validation", "Revision comparison requires a post ID and positive current, source and destination revisions")
	}
	r, e := s.record(a.ID)
	if e != nil {
		return nil, e
	}
	if e = checkRevision(r, a.Revision); e != nil {
		return nil, e
	}
	from, _, e := comparedRevision(r, a.From)
	if e != nil {
		return nil, e
	}
	to, inHistory, e := comparedRevision(r, a.To)
	if e != nil {
		return nil, e
	}
	review := reviewContent(from.Content, to.Content)
	sameCurrentContent := reflect.DeepEqual(proposalContent(r.Draft), to.Content)
	reason := "ready"
	switch {
	case !inHistory:
		reason = "not_in_history"
	case to.Current:
		reason = "current_revision"
	case sameCurrentContent:
		reason = "no_content_changes"
	case !s.unique(to.Content.Slug, a.ID):
		reason = "historical_slug_in_use"
	}
	live := 0
	if r.Live != nil {
		live = r.Live.Revision
	}
	return RevisionComparison{a.ID, r.Draft.Revision, live, s.instanceID, s.State.Revision, from, to, review, reason == "ready", reason,
		[]string{"saved_snapshots_only", "all_eight_editable_fields", "single_changed_range_not_minimal_diff", "post_content_limits_apply", "restore_availability_is_advisory_not_authority", "restore_requires_current_revision_CAS", "no_render_execution_or_network"}}, nil
}
