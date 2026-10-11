package core

import (
	"encoding/json"
	"sort"
	"time"
)

// A history page carries navigation metadata only. Fetch selected content with
// posts.preview/posts.compare; never copy Markdown into the pagination response.
type RevisionSummary struct {
	Revision  int    `json:"revision"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Current   bool   `json:"current"`
	Published bool   `json:"published"`
}

type RevisionHistory struct {
	ID                 string            `json:"id"`
	Revision           int               `json:"revision"`
	PublishedRevision  int               `json:"published_revision"`
	InstanceID         string            `json:"instance_id"`
	InstanceRevision   int               `json:"instance_revision"`
	Total              int               `json:"total"`
	Limit              int               `json:"limit"`
	BeforeRevision     int               `json:"before_revision"`
	NextBeforeRevision int               `json:"next_before_revision"`
	HasMore            bool              `json:"has_more"`
	Revisions          []RevisionSummary `json:"revisions"`
}

// Legacy backups can retain arbitrary timestamp strings. Metadata pages expose
// only bounded valid dates; the stored snapshots and legacy reads stay intact.
func historyDate(value string) string {
	if len(value) > 64 {
		return ""
	}
	if _, e := time.Parse(time.RFC3339Nano, value); e != nil {
		return ""
	}
	return value
}

func (s *Store) revisionHistory(raw json.RawMessage) (any, error) {
	var a struct {
		ID       string `json:"id"`
		Revision int    `json:"revision"`
		Limit    *int   `json:"limit"`
		Before   *int   `json:"before_revision"`
	}
	if e := decode(raw, &a); e != nil {
		return nil, e
	}
	limit, before := 8, 0
	if a.Limit != nil {
		limit = *a.Limit
	}
	if a.Before != nil {
		before = *a.Before
	}
	var supplied map[string]json.RawMessage
	_ = json.Unmarshal(raw, &supplied)
	if a.ID == "" || len(a.ID) > 128 || a.Revision < 1 || limit < 1 || limit > 50 ||
		(a.Before != nil && before < 1) || string(supplied["limit"]) == "null" || string(supplied["before_revision"]) == "null" {
		return nil, Err("validation", "Revision history requires a post ID, positive current revision, limit 1–50 and an optional positive before_revision")
	}
	r, e := s.record(a.ID)
	if e != nil {
		return nil, e
	}
	if e = checkRevision(r, a.Revision); e != nil {
		return nil, e
	}
	if before > a.Revision {
		return nil, Err("validation", "History cursor cannot exceed the current saved revision")
	}
	// Sort references rather than copy content. Sparse imported histories need
	// revision ordering, independent of JSON array order and equal timestamps.
	rows := make([]*Post, 0, len(r.Revisions))
	for i := range r.Revisions {
		p := &r.Revisions[i]
		if before == 0 || p.Revision < before {
			rows = append(rows, p)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Revision > rows[j].Revision })
	live := 0
	if r.Live != nil {
		live = r.Live.Revision
	}
	result := RevisionHistory{ID: a.ID, Revision: r.Draft.Revision, PublishedRevision: live,
		InstanceID: s.InstanceID(), InstanceRevision: s.State.Revision, Total: len(r.Revisions),
		Limit: limit, BeforeRevision: before, HasMore: len(rows) > limit, Revisions: []RevisionSummary{}}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for _, p := range rows {
		result.Revisions = append(result.Revisions, RevisionSummary{p.Revision, historyDate(p.CreatedAt), historyDate(p.UpdatedAt),
			p.Revision == r.Draft.Revision, p.Revision == live})
	}
	if result.HasMore {
		result.NextBeforeRevision = result.Revisions[len(result.Revisions)-1].Revision
	}
	return result, nil
}
