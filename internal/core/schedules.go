package core

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Schedule holds the exact reviewed content, not a pointer to the mutable draft.
// Revision guards edits to this schedule; PostRevision identifies its snapshot.
type Schedule struct {
	ID           string `json:"id"`
	PostID       string `json:"post_id"`
	PostRevision int    `json:"post_revision"`
	Snapshot     Post   `json:"snapshot"`
	PublishAt    string `json:"publish_at"`
	Status       string `json:"status"`
	Revision     int    `json:"revision"`
	CreatedBy    string `json:"created_by"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	ExecutedAt   string `json:"executed_at,omitempty"`
	CancelledBy  string `json:"cancelled_by,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

type DueResult struct {
	Published []Schedule `json:"published"`
	Blocked   []Schedule `json:"blocked"`
}

func init() {
	add := func(n, d string, ro bool, p map[string]any, req ...string) {
		Specs[n] = Spec{n, d, obj(p, req...), ro, "publish"}
	}
	add("schedules.create", "Confirm future publication of this exact current draft snapshot; later draft edits remain private", false, map[string]any{"post_id": str("Post ID"), "expected_revision": integer("Exact reviewed current draft revision"), "publish_at": str("Future RFC3339 timestamp with timezone"), "confirm": boolean("Explicit approval to publish this snapshot at that time"), "idempotency_key": str("Unique retry key")}, "post_id", "expected_revision", "publish_at", "confirm")
	add("schedules.list", "List durable publishing schedules and their terminal outcomes", true, map[string]any{"status": str("Optional pending, published, blocked, or cancelled"), "post_id": str("Optional post ID filter")})
	add("schedules.get", "Read scheduled timestamp, pinned snapshot, and publishing outcome", true, map[string]any{"id": str("Schedule ID")}, "id")
	add("schedules.cancel", "Cancel a pending or blocked schedule with a revision guard", false, map[string]any{"id": str("Schedule ID"), "expected_revision": integer("Current schedule revision"), "confirm": boolean("Explicit cancellation"), "idempotency_key": str("Unique retry key")}, "id", "expected_revision", "confirm")
	add("schedules.reschedule", "Explicitly approve a current draft snapshot and future time for a pending or blocked schedule", false, map[string]any{"id": str("Schedule ID"), "expected_revision": integer("Current schedule revision"), "expected_post_revision": integer("Exact newly reviewed current draft revision to pin"), "publish_at": str("Future RFC3339 timestamp with timezone"), "confirm": boolean("Explicit approval of the pinned revision and time"), "idempotency_key": str("Unique retry key")}, "id", "expected_revision", "expected_post_revision", "publish_at", "confirm")
}

func scheduleTime(value string, current time.Time) (string, error) {
	t, e := time.Parse(time.RFC3339Nano, value)
	if e != nil || len(value) > 64 || !t.After(current) {
		return "", Err("validation", "publish_at must be a future RFC3339 timestamp with timezone")
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}
func scheduleStatus(status string) bool {
	return status == "pending" || status == "published" || status == "blocked" || status == "cancelled"
}
func activeSchedule(status string) bool { return status == "pending" || status == "blocked" }

func (s *Store) executeSchedule(op string, raw json.RawMessage, actor string) (any, error) {
	switch op {
	case "schedules.list":
		var a struct {
			Status string `json:"status"`
			PostID string `json:"post_id"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if a.Status != "" && !scheduleStatus(a.Status) {
			return nil, Err("validation", "Invalid schedule status")
		}
		list := []Schedule{}
		for _, p := range s.State.Schedules {
			if a.Status != "" && p.Status != a.Status {
				continue
			}
			if a.PostID != "" && p.PostID != a.PostID {
				continue
			}
			list = append(list, p)
		}
		sort.Slice(list, func(i, j int) bool {
			a, _ := time.Parse(time.RFC3339Nano, list[i].PublishAt)
			b, _ := time.Parse(time.RFC3339Nano, list[j].PublishAt)
			if a.Equal(b) {
				return list[i].ID < list[j].ID
			}
			return a.Before(b)
		})
		return map[string]any{"schedules": list}, nil
	case "schedules.get":
		var a struct {
			ID string `json:"id"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		p, ok := s.State.Schedules[a.ID]
		if !ok {
			return nil, Err("not_found", "Schedule not found")
		}
		return map[string]any{"schedule": p}, nil
	case "schedules.create":
		var a struct {
			PostID         string `json:"post_id"`
			Expected       int    `json:"expected_revision"`
			PublishAt      string `json:"publish_at"`
			Confirm        bool   `json:"confirm"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if !a.Confirm {
			return nil, Err("confirmation_required", "Set confirm:true to schedule publication")
		}
		date, e := scheduleTime(a.PublishAt, time.Now())
		if e != nil {
			return nil, e
		}
		r, e := s.record(a.PostID)
		if e != nil {
			return nil, e
		}
		if e = checkRevision(r, a.Expected); e != nil {
			return nil, e
		}
		if strings.TrimSpace(r.Draft.Markdown) == "" {
			return nil, Err("validation", "Cannot publish an empty post")
		}
		count := 0
		for _, v := range s.State.Schedules {
			if activeSchedule(v.Status) {
				count++
				if v.PostID == a.PostID {
					return nil, Err("conflict", "Post already has an active schedule; cancel or reschedule it")
				}
			}
		}
		if count >= maxPendingWorkflows {
			return nil, Err("validation", "Active schedule limit reached")
		}
		snapshot := r.Draft
		snapshot.Tags = append([]string{}, r.Draft.Tags...)
		p := Schedule{ID: id(), PostID: a.PostID, PostRevision: a.Expected, Snapshot: snapshot, PublishAt: date, Status: "pending", Revision: 1, CreatedBy: actor, CreatedAt: now(), UpdatedAt: now()}
		if s.State.Schedules == nil {
			s.State.Schedules = map[string]Schedule{}
		}
		s.State.Schedules[p.ID] = p
		s.audit(op, p.ID, actor, p.Revision)
		return map[string]any{"schedule": p}, nil
	case "schedules.cancel", "schedules.reschedule":
		var a struct {
			ID             string `json:"id"`
			Expected       int    `json:"expected_revision"`
			ExpectedPost   int    `json:"expected_post_revision"`
			PublishAt      string `json:"publish_at"`
			Confirm        bool   `json:"confirm"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		if e := decode(raw, &a); e != nil {
			return nil, e
		}
		if op == "schedules.cancel" && (a.ExpectedPost != 0 || a.PublishAt != "") {
			return nil, Err("validation", "Cancel does not accept a publication time or post revision")
		}
		p, ok := s.State.Schedules[a.ID]
		if !ok {
			return nil, Err("not_found", "Schedule not found")
		}
		if p.Revision != a.Expected {
			return nil, Err("conflict", "Schedule revision conflict")
		}
		if !activeSchedule(p.Status) {
			return nil, Err("conflict", "Schedule is already closed")
		}
		if !a.Confirm {
			return nil, Err("confirmation_required", "Set confirm:true for this schedule change")
		}
		if op == "schedules.cancel" {
			p.Status = "cancelled"
			p.CancelledBy = actor
			p.Reason = "cancelled_by_publisher"
		} else {
			date, e := scheduleTime(a.PublishAt, time.Now())
			if e != nil {
				return nil, e
			}
			r, e := s.record(p.PostID)
			if e != nil {
				return nil, e
			}
			if e = checkRevision(r, a.ExpectedPost); e != nil {
				return nil, e
			}
			if strings.TrimSpace(r.Draft.Markdown) == "" {
				return nil, Err("validation", "Cannot publish an empty post")
			}
			p.Snapshot = r.Draft
			p.Snapshot.Tags = append([]string{}, r.Draft.Tags...)
			p.PostRevision = a.ExpectedPost
			p.PublishAt = date
			p.Status = "pending"
			p.Reason = ""
			p.CancelledBy = ""
			p.ExecutedAt = ""
		}
		p.Revision++
		p.UpdatedAt = now()
		s.State.Schedules[p.ID] = p
		s.audit(op, p.ID, actor, p.Revision)
		return map[string]any{"schedule": p}, nil
	}
	return nil, Err("not_found", "Unknown schedule operation")
}

// cancelPostSchedules is called in the transaction that explicitly publishes,
// unpublishes, or trashes a post. A queued job must never undo that newer intent.
func (s *Store) cancelPostSchedules(postID, operation, actor string) {
	for key, p := range s.State.Schedules {
		if p.PostID != postID || !activeSchedule(p.Status) {
			continue
		}
		p.Status = "cancelled"
		p.Revision++
		p.UpdatedAt = now()
		p.CancelledBy = actor
		p.Reason = operation
		s.State.Schedules[key] = p
		s.audit("schedules.cancel", p.ID, actor, p.Revision)
	}
}

// RunDue is safe to invoke at startup, on a ticker, and concurrently from more
// than one daemon. SQLite's immediate write transaction serializes workers. The
// live snapshot and terminal schedule status commit together or neither does.
func (s *Store) RunDue(ctx context.Context, current time.Time) (DueResult, error) {
	result := DueResult{Published: []Schedule{}, Blocked: []Schedule{}}
	if current.IsZero() {
		return result, Err("validation", "A scheduler time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return result, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	if e = s.refresh(ctx, tx, false); e != nil {
		return result, e
	}
	due := []Schedule{}
	for _, p := range s.State.Schedules {
		date, err := time.Parse(time.RFC3339Nano, p.PublishAt)
		if p.Status == "pending" && (err != nil || !date.After(current)) {
			due = append(due, p)
		}
	}
	if len(due) == 0 {
		return result, nil
	}
	sort.Slice(due, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, due[i].PublishAt)
		b, _ := time.Parse(time.RFC3339Nano, due[j].PublishAt)
		if a.Equal(b) {
			return due[i].ID < due[j].ID
		}
		return a.Before(b)
	})
	before := s.State
	s.State = cloneForWrite(s.State, json.RawMessage(`{}`))
	// Also clone here so the worker remains rollback-safe even for older callers
	// whose cloneForWrite integration has not yet been upgraded.
	s.State = cloneWorkflows(s.State)
	timestamp := current.UTC().Format(time.RFC3339Nano)
	for _, p := range due {
		reason := ""
		r := s.State.Posts[p.PostID]
		switch {
		case !validWorkflowTime(p.PublishAt):
			reason = "invalid_publish_at"
		case r == nil || r.Deleted:
			reason = "post_unavailable"
		case p.Snapshot.ID != p.PostID || p.Snapshot.Revision != p.PostRevision || p.PostRevision > r.Draft.Revision:
			reason = "invalid_snapshot"
		case !s.unique(p.Snapshot.Slug, p.PostID):
			reason = "slug_conflict"
		case strings.TrimSpace(p.Snapshot.Markdown) == "":
			reason = "empty_snapshot"
		}
		p.Revision++
		p.UpdatedAt = timestamp
		if reason != "" {
			p.Status = "blocked"
			p.Reason = reason
			s.State.Schedules[p.ID] = p
			s.audit("schedules.blocked", p.ID, "scheduler", p.PostRevision)
			result.Blocked = append(result.Blocked, p)
			continue
		}
		r = cloneWorkflowRecord(r)
		live := p.Snapshot
		live.Tags = append([]string{}, p.Snapshot.Tags...)
		live.HTML = Render(live.Markdown)
		live.Status = "published"
		live.PublishedRevision = p.PostRevision
		live.PublishedAt = timestamp
		if r.Live != nil && r.Live.Slug != live.Slug {
			r.OldSlugs = append(r.OldSlugs, r.Live.Slug)
		}
		r.Live = &live
		r.Draft.PublishedRevision = p.PostRevision
		r.Draft.PublishedAt = timestamp
		r.Draft.Status = "published"
		if r.Draft.Revision != p.PostRevision {
			r.Draft.Status = "changed"
		}
		s.State.Posts[p.PostID] = r
		p.Status = "published"
		p.ExecutedAt = timestamp
		p.Reason = ""
		s.State.Schedules[p.ID] = p
		s.audit("schedules.publish", p.ID, "scheduler", p.PostRevision)
		s.audit("posts.publish", p.PostID, "scheduler", p.PostRevision)
		result.Published = append(result.Published, p)
	}
	s.State.Revision++
	if logicalSize(s.State) > 64*1024*1024 {
		s.State = before
		return DueResult{}, Err("validation", "Instance exceeds v1 64 MiB logical limit; export and compact content")
	}
	if e = s.persistRows(ctx, tx, before); e != nil {
		s.State = before
		return DueResult{}, e
	}
	if e = tx.Commit(); e != nil {
		s.State = before
		return DueResult{}, e
	}
	return result, nil
}

func validateSchedules(st State) error {
	active := map[string]bool{}
	for key, p := range st.Schedules {
		r := st.Posts[p.PostID]
		if key == "" || key != p.ID || len(key) > 128 || r == nil || p.PostRevision < 1 || p.PostRevision > r.Draft.Revision || p.Snapshot.ID != p.PostID || p.Snapshot.Revision != p.PostRevision || p.Revision < 1 || !scheduleStatus(p.Status) || p.CreatedBy == "" || len(p.CreatedBy) > 128 || !validWorkflowTime(p.CreatedAt) || !validWorkflowTime(p.UpdatedAt) || !validWorkflowTime(p.PublishAt) || len(p.CancelledBy) > 128 || len(p.Reason) > 128 {
			return Err("validation", "Invalid backup schedule")
		}
		if e := ValidatePost(p.Snapshot); e != nil {
			return e
		}
		if strings.TrimSpace(p.Snapshot.Markdown) == "" {
			return Err("validation", "Invalid empty scheduled snapshot")
		}
		if !matchesWorkflowRevision(r, p.PostRevision, proposalContent(p.Snapshot)) {
			return Err("validation", "Scheduled snapshot does not match its immutable revision")
		}
		if activeSchedule(p.Status) {
			if active[p.PostID] || r.Deleted {
				return Err("validation", "Invalid active schedule")
			}
			active[p.PostID] = true
		}
		if (p.Status == "published" && !validWorkflowTime(p.ExecutedAt)) || (p.Status != "published" && p.ExecutedAt != "") {
			return Err("validation", "Invalid schedule execution time")
		}
		p.Snapshot.HTML = Render(p.Snapshot.Markdown)
		st.Schedules[key] = p
	}
	if len(active) > maxPendingWorkflows {
		return Err("validation", "Active schedule limit reached")
	}
	return nil
}
func reflectContentEqual(a, b ProposalContent) bool {
	// JSON gives nil and empty tags distinct representations. Normalize both so
	// older backups with absent tags remain valid.
	if a.Tags == nil {
		a.Tags = []string{}
	}
	if b.Tags == nil {
		b.Tags = []string{}
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
