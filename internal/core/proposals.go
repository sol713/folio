package core

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
)

const maxPendingWorkflows = 1000

// ProposalContent contains only author-controlled content. No status, rendered
// HTML, publication timestamp, or revision can be supplied by an agent.
type ProposalContent struct {
	Title    string   `json:"title"`
	Slug     string   `json:"slug"`
	Excerpt  string   `json:"excerpt"`
	Markdown string   `json:"markdown"`
	Tags     []string `json:"tags"`
	Category string   `json:"category"`
	Cover    string   `json:"cover"`
	Featured bool     `json:"featured"`
}

type Proposal struct {
	ID              string           `json:"id"`
	PostID          string           `json:"post_id,omitempty"`
	BaseRevision    int              `json:"base_revision"`
	Base            *ProposalContent `json:"base,omitempty"`
	Candidate       ProposalContent  `json:"candidate"`
	CreatedBy       string           `json:"created_by"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
	Status          string           `json:"status"`
	Revision        int              `json:"revision"`
	ReviewedBy      string           `json:"reviewed_by,omitempty"`
	ReviewedAt      string           `json:"reviewed_at,omitempty"`
	AppliedPostID   string           `json:"applied_post_id,omitempty"`
	AppliedRevision int              `json:"applied_revision,omitempty"`
}

type ProposalFieldChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

// MarkdownChange is plain text, not HTML. A deterministic single changed range
// avoids quadratic diff algorithms on hostile or very large Markdown input.
type MarkdownChange struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type ProposalReview struct {
	ChangedFields []ProposalFieldChange `json:"changed_fields"`
	MarkdownDiff  []MarkdownChange      `json:"markdown_diff"`
}

func proposalContent(p Post) ProposalContent {
	return ProposalContent{p.Title, p.Slug, p.Excerpt, p.Markdown, append([]string{}, p.Tags...), p.Category, p.Cover, p.Featured}
}
func (p ProposalContent) post() Post {
	return Post{Title: p.Title, Slug: p.Slug, Excerpt: p.Excerpt, Markdown: p.Markdown, Tags: append([]string{}, p.Tags...), Category: p.Category, Cover: p.Cover, Featured: p.Featured}
}
func reviewProposal(p Proposal) ProposalReview {
	before := ProposalContent{Tags: []string{}}
	if p.Base != nil {
		before = *p.Base
	}
	after := p.Candidate
	review := ProposalReview{ChangedFields: []ProposalFieldChange{}, MarkdownDiff: []MarkdownChange{}}
	fields := []struct {
		name          string
		before, after any
	}{
		{"title", before.Title, after.Title}, {"slug", before.Slug, after.Slug}, {"excerpt", before.Excerpt, after.Excerpt}, {"markdown", before.Markdown, after.Markdown}, {"tags", before.Tags, after.Tags}, {"category", before.Category, after.Category}, {"cover", before.Cover, after.Cover}, {"featured", before.Featured, after.Featured},
	}
	for _, f := range fields {
		if !reflect.DeepEqual(f.before, f.after) {
			review.ChangedFields = append(review.ChangedFields, ProposalFieldChange{f.name, f.before, f.after})
		}
	}
	if before.Markdown == after.Markdown {
		return review
	}
	a, b := strings.SplitAfter(before.Markdown, "\n"), strings.SplitAfter(after.Markdown, "\n")
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	add := func(kind, text string) {
		if text != "" {
			review.MarkdownDiff = append(review.MarkdownDiff, MarkdownChange{kind, text})
		}
	}
	add("equal", strings.Join(a[:prefix], ""))
	add("remove", strings.Join(a[prefix:len(a)-suffix], ""))
	add("add", strings.Join(b[prefix:len(b)-suffix], ""))
	add("equal", strings.Join(a[len(a)-suffix:], ""))
	return review
}
func proposalResult(p Proposal) map[string]any {
	return map[string]any{"proposal": p, "review": reviewProposal(p)}
}

func init() {
	contentFields := fields(postFields, nil)
	delete(contentFields, "idempotency_key")
	candidate := obj(contentFields, "title", "slug", "markdown")
	add := func(n, d, scope string, ro bool, p map[string]any, req ...string) {
		Specs[n] = Spec{n, d, obj(p, req...), ro, scope}
	}
	add("proposals.create", "Submit immutable candidate content for owner review; never changes a draft or publication", "proposal", false, map[string]any{"post_id": str("Existing post ID, or omit to propose a new post"), "base_revision": integer("Required current draft revision for an update; zero for a new post"), "candidate": candidate, "idempotency_key": str("Unique retry key")}, "candidate")
	add("proposals.list", "List your proposals; an administrator can review all proposals", "proposal", true, map[string]any{"status": str("Optional pending, approved, rejected, or cancelled"), "post_id": str("Optional post ID filter")})
	add("proposals.get", "Read an immutable candidate, exact base snapshot and plain-text field/Markdown changes", "proposal", true, map[string]any{"id": str("Proposal ID")}, "id")
	action := map[string]any{"id": str("Proposal ID"), "expected_revision": integer("Current proposal revision, not the post revision"), "confirm": boolean("Explicit confirmation of this review decision"), "idempotency_key": str("Unique retry key")}
	add("proposals.approve", "Owner approval applies this exact candidate to a private draft only if base_revision is unchanged", "admin", false, action, "id", "expected_revision", "confirm")
	add("proposals.reject", "Owner rejection closes a pending proposal without changing post content", "admin", false, action, "id", "expected_revision", "confirm")
	add("proposals.cancel", "Withdraw your pending proposal; administrators may cancel any proposal", "proposal", false, action, "id", "expected_revision", "confirm")
}

func (s *Store) executeProposal(op string, raw json.RawMessage, actor string) (any, error) {
	if actor == "" || len(actor) > 128 {
		return nil, Err("forbidden", "A proposal actor is required")
	}
	switch op {
	case "proposals.create":
		var a struct {
			PostID         string          `json:"post_id"`
			BaseRevision   int             `json:"base_revision"`
			Candidate      ProposalContent `json:"candidate"`
			IdempotencyKey string          `json:"idempotency_key"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if a.Candidate.Tags == nil {
			a.Candidate.Tags = []string{}
		}
		if e := ValidatePost(a.Candidate.post()); e != nil {
			return nil, e
		}
		p := Proposal{ID: id(), PostID: a.PostID, BaseRevision: a.BaseRevision, Candidate: a.Candidate, CreatedBy: actor, CreatedAt: now(), UpdatedAt: now(), Status: "pending", Revision: 1}
		if a.PostID == "" {
			if a.BaseRevision != 0 {
				return nil, Err("validation", "New proposals require base_revision:0")
			}
		} else {
			r, e := s.record(a.PostID)
			if e != nil {
				return nil, e
			}
			if e = checkRevision(r, a.BaseRevision); e != nil {
				return nil, e
			}
			base := proposalContent(r.Draft)
			p.Base = &base
		}
		count := 0
		for _, v := range s.State.Proposals {
			if v.Status == "pending" {
				count++
			}
		}
		if count >= maxPendingWorkflows {
			return nil, Err("validation", "Pending proposal limit reached")
		}
		if s.State.Proposals == nil {
			s.State.Proposals = map[string]Proposal{}
		}
		s.State.Proposals[p.ID] = p
		s.audit(op, p.ID, actor, p.Revision)
		return proposalResult(p), nil
	case "proposals.list":
		var a struct {
			Status string `json:"status"`
			PostID string `json:"post_id"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if a.Status != "" && !proposalStatus(a.Status) {
			return nil, Err("validation", "Invalid proposal status")
		}
		list := []Proposal{}
		for _, p := range s.State.Proposals {
			if actor != "admin" && actor != p.CreatedBy {
				continue
			}
			if a.Status != "" && p.Status != a.Status {
				continue
			}
			if a.PostID != "" && p.PostID != a.PostID {
				continue
			}
			list = append(list, p)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].CreatedAt == list[j].CreatedAt {
				return list[i].ID < list[j].ID
			}
			return list[i].CreatedAt > list[j].CreatedAt
		})
		return map[string]any{"proposals": list}, nil
	case "proposals.get":
		var a struct {
			ID string `json:"id"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		p, ok := s.State.Proposals[a.ID]
		if !ok || (actor != "admin" && actor != p.CreatedBy) {
			return nil, Err("not_found", "Proposal not found")
		}
		return proposalResult(p), nil
	case "proposals.approve", "proposals.reject", "proposals.cancel":
		var a struct {
			ID             string `json:"id"`
			Expected       int    `json:"expected_revision"`
			Confirm        bool   `json:"confirm"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if op != "proposals.cancel" && actor != "admin" {
			return nil, Err("forbidden", "Only the owner can review proposals")
		}
		p, ok := s.State.Proposals[a.ID]
		if !ok || (actor != "admin" && actor != p.CreatedBy) {
			return nil, Err("not_found", "Proposal not found")
		}
		if p.Revision != a.Expected {
			return nil, Err("conflict", "Proposal revision conflict")
		}
		if p.Status != "pending" {
			return nil, Err("conflict", "Proposal is already closed")
		}
		if !a.Confirm {
			return nil, Err("confirmation_required", "Set confirm:true for this review decision")
		}
		var applied *Post
		switch op {
		case "proposals.approve":
			post := p.Candidate.post()
			if e := ValidatePost(post); e != nil {
				return nil, e
			}
			if !s.unique(post.Slug, p.PostID) {
				return nil, Err("conflict", "Slug already exists")
			}
			timestamp := now()
			post.CreatedAt = timestamp
			post.UpdatedAt = timestamp
			post.Status = "draft"
			post.Revision = 1
			var r *Record
			if p.PostID == "" {
				post.ID = id()
				r = &Record{}
			} else {
				existing, e := s.record(p.PostID)
				if e != nil {
					return nil, e
				}
				if e = checkRevision(existing, p.BaseRevision); e != nil {
					return nil, e
				}
				r = cloneWorkflowRecord(existing)
				post.ID = p.PostID
				post.CreatedAt = r.Draft.CreatedAt
				post.Revision = r.Draft.Revision + 1
				post.PublishedRevision = r.Draft.PublishedRevision
				post.PublishedAt = r.Draft.PublishedAt
				if r.Live != nil {
					post.Status = "changed"
				}
			}
			post.HTML = Render(post.Markdown)
			r.Draft = post
			r.Revisions = append(r.Revisions, post)
			s.State.Posts[post.ID] = r
			p.Status = "approved"
			p.AppliedPostID = post.ID
			p.AppliedRevision = post.Revision
			applied = &post
		case "proposals.reject":
			p.Status = "rejected"
		case "proposals.cancel":
			p.Status = "cancelled"
		}
		p.Revision++
		p.ReviewedBy = actor
		p.ReviewedAt = now()
		p.UpdatedAt = p.ReviewedAt
		s.State.Proposals[p.ID] = p
		s.audit(op, p.ID, actor, p.Revision)
		if applied != nil {
			s.audit("posts.apply_proposal", applied.ID, actor, applied.Revision)
		}
		result := proposalResult(p)
		if applied != nil {
			result["post"] = *applied
		}
		return result, nil
	}
	return nil, Err("not_found", "Unknown proposal operation")
}

func proposalStatus(status string) bool {
	return status == "pending" || status == "approved" || status == "rejected" || status == "cancelled"
}
func cloneWorkflowRecord(r *Record) *Record {
	out := *r
	out.Revisions = append([]Post{}, r.Revisions...)
	out.OldSlugs = append([]string{}, r.OldSlugs...)
	if r.Live != nil {
		live := *r.Live
		out.Live = &live
	}
	return &out
}
func cloneWorkflows(st State) State {
	proposals := make(map[string]Proposal, len(st.Proposals))
	for k, p := range st.Proposals {
		proposals[k] = p
	}
	st.Proposals = proposals
	schedules := make(map[string]Schedule, len(st.Schedules))
	for k, p := range st.Schedules {
		schedules[k] = p
	}
	st.Schedules = schedules
	return st
}
func workflowSize(st State) int {
	// Exact JSON sizing covers nested plain-text candidate/base snapshots. No
	// proposal diff is persisted. Scheduling metadata is small and bounded.
	p, _ := json.Marshal(st.Proposals)
	s, _ := json.Marshal(st.Schedules)
	return len(p) + len(s) + 128
}
func validWorkflowTime(v string) bool {
	_, e := time.Parse(time.RFC3339Nano, v)
	return len(v) <= 64 && e == nil
}
func validateWorkflows(st State) error {
	pending := 0
	for key, p := range st.Proposals {
		if key == "" || key != p.ID || len(key) > 128 || len(p.PostID) > 128 || p.CreatedBy == "" || len(p.CreatedBy) > 128 || !validWorkflowTime(p.CreatedAt) || !validWorkflowTime(p.UpdatedAt) || !proposalStatus(p.Status) || p.Revision < 1 {
			return Err("validation", "Invalid backup proposal")
		}
		if e := ValidatePost(p.Candidate.post()); e != nil {
			return e
		}
		if p.PostID == "" {
			if p.BaseRevision != 0 || p.Base != nil {
				return Err("validation", "Invalid proposal base revision")
			}
		} else {
			r := st.Posts[p.PostID]
			if p.BaseRevision < 1 || p.Base == nil || r == nil || p.BaseRevision > r.Draft.Revision {
				return Err("validation", "Invalid proposal base revision")
			}
			if e := ValidatePost(p.Base.post()); e != nil {
				return e
			}
			if !matchesWorkflowRevision(r, p.BaseRevision, *p.Base) {
				return Err("validation", "Invalid proposal base revision")
			}
		}
		if p.Status == "pending" {
			pending++
			if p.Revision != 1 || p.ReviewedBy != "" || p.ReviewedAt != "" || p.AppliedPostID != "" || p.AppliedRevision != 0 {
				return Err("validation", "Invalid pending proposal")
			}
		} else {
			if p.Revision != 2 || p.ReviewedBy == "" || len(p.ReviewedBy) > 128 || !validWorkflowTime(p.ReviewedAt) {
				return Err("validation", "Invalid reviewed proposal")
			}
			if p.Status == "approved" {
				r := st.Posts[p.AppliedPostID]
				if r == nil || p.AppliedRevision < 1 || p.AppliedRevision > r.Draft.Revision || (p.PostID != "" && p.AppliedPostID != p.PostID) {
					return Err("validation", "Invalid applied proposal revision")
				}
				if !matchesWorkflowRevision(r, p.AppliedRevision, p.Candidate) {
					return Err("validation", "Invalid applied proposal revision")
				}
			} else if p.AppliedPostID != "" || p.AppliedRevision != 0 {
				return Err("validation", "Invalid reviewed proposal")
			}
		}
	}
	if pending > maxPendingWorkflows {
		return Err("validation", "Pending proposal limit reached")
	}
	return validateSchedules(st)
}

func matchesWorkflowRevision(r *Record, revision int, content ProposalContent) bool {
	for _, p := range r.Revisions {
		if p.Revision == revision {
			return reflectContentEqual(proposalContent(p), content)
		}
	}
	return false
}
