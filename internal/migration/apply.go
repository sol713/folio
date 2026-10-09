package migration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

var ErrPartialApply = errors.New("migration incomplete; committed items are retained; retry the same frozen plan")

// Apply executes at most one existing draft operation per non-skipped entry.
// It is not a transaction across entries and never compensates by deleting data.
// On retry, replay the exact plan against the same instance and actor so the core's
// durable idempotency cache returns prior receipts before CAS validation.
func Apply(ctx context.Context, caller Caller, p ImportPlan, options ApplyOptions) (Report, error) {
	r := Report{PlanID: p.ID, Outcomes: []Outcome{}}
	if err := ValidatePlan(p); err != nil {
		return r, err
	}
	if caller == nil {
		return r, errors.New("authorized operation adapter required")
	}
	stopped := false
	failed := false
	for _, entry := range p.Entries {
		o := Outcome{SourcePath: entry.Document.SourcePath, Action: entry.Action, Status: "pending", IdempotencyKey: entry.IdempotencyKey}
		if stopped {
			r.Outcomes = append(r.Outcomes, o)
			continue
		}
		if entry.Action == "skip" {
			o.Status = "skipped"
			o.PostID = entry.TargetID
			o.Revision = entry.ExpectedRevision
			r.Outcomes = append(r.Outcomes, o)
			continue
		}
		if ctx.Err() != nil {
			o.Status = "failed"
			o.Error = "Cancelled before operation"
			stopped = true
			failed = true
			r.Outcomes = append(r.Outcomes, o)
			continue
		}
		args, _ := json.Marshal(requestFor(entry, true))
		op := "posts.create"
		if entry.Action == "update" {
			op = "posts.update"
		}
		result, err := caller.Call(ctx, op, args)
		if err != nil {
			o.Status = "failed"
			o.Error = "Operation failed or receipt was lost; retry the same plan and key"
			failed = true
			stopped = !options.ContinueOnError
			r.Outcomes = append(r.Outcomes, o)
			continue
		}
		var reply struct {
			Post struct {
				ID       string `json:"id"`
				Slug     string `json:"slug"`
				Revision int    `json:"revision"`
			} `json:"post"`
		}
		err = json.Unmarshal(result, &reply)
		expected := 1
		if entry.Action == "update" {
			expected = entry.ExpectedRevision + 1
		}
		if err != nil || reply.Post.ID == "" || len(reply.Post.ID) > 128 || strings.ContainsAny(reply.Post.ID, "\x00\r\n") || reply.Post.Slug != entry.Document.Slug || reply.Post.Revision != expected || (entry.Action == "update" && reply.Post.ID != entry.TargetID) {
			o.Status = "failed"
			o.Error = "Invalid operation receipt; write may have committed; retry the same plan and key"
			failed = true
			stopped = !options.ContinueOnError
		} else {
			o.Status = "applied"
			o.PostID = reply.Post.ID
			o.Revision = reply.Post.Revision
		}
		r.Outcomes = append(r.Outcomes, o)
	}
	r.Complete = !failed
	if failed {
		return r, ErrPartialApply
	}
	return r, nil
}
