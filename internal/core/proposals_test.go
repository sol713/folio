package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func workflowCall(t *testing.T, s *Store, actor, op string, args any) any {
	t.Helper()
	raw, e := json.Marshal(args)
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.Call(context.Background(), op, raw, actor)
	if e != nil {
		t.Fatalf("%s (%s): %v", op, actor, e)
	}
	return v
}
func workflowError(t *testing.T, s *Store, actor, op string, args any, code string) {
	t.Helper()
	raw, _ := json.Marshal(args)
	_, e := s.Call(context.Background(), op, raw, actor)
	var ce *Error
	if !errors.As(e, &ce) || ce.Code != code {
		t.Fatalf("%s (%s): expected %s, got %v", op, actor, code, e)
	}
}
func getProposal(t *testing.T, v any) Proposal {
	t.Helper()
	raw, _ := json.Marshal(v)
	var result struct {
		Proposal Proposal `json:"proposal"`
	}
	if e := json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	return result.Proposal
}
func proposalArgs(p Post, body string) map[string]any {
	candidate := proposalContent(p)
	candidate.Markdown = body
	candidate.Title = "  提案 / candidate  "
	return map[string]any{"post_id": p.ID, "base_revision": p.Revision, "candidate": candidate}
}
func proposalAction(p Proposal) map[string]any {
	return map[string]any{"id": p.ID, "expected_revision": p.Revision, "confirm": true}
}

func TestProposalReviewIsImmutableAndPrivate(t *testing.T) {
	s := testStore(t)
	original := create(t, s, "proposal-review")
	call(t, s, "posts.publish", action(original, true))
	candidate := "一行中文\n\n<script>alert('unsafe')</script>\n\nA **careful** change.\n"
	p := getProposal(t, workflowCall(t, s, "proposal", "proposals.create", proposalArgs(original, candidate)))
	if p.Status != "pending" || p.Base == nil || p.Base.Markdown != original.Markdown || p.BaseRevision != original.Revision {
		t.Fatalf("bad proposal: %+v", p)
	}
	current := getPost(t, call(t, s, "posts.get", map[string]any{"id": original.ID}))
	_, public := s.Public()
	if current.Markdown != original.Markdown || len(public) != 1 || public[0].Markdown != original.Markdown || len(s.State.Posts[original.ID].Revisions) != 1 {
		t.Fatal("proposal submission changed draft, history or live content")
	}
	result := workflowCall(t, s, "admin", "proposals.get", map[string]any{"id": p.ID})
	encoded, _ := json.Marshal(result)
	var read struct {
		Review ProposalReview `json:"review"`
	}
	json.Unmarshal(encoded, &read)
	if len(read.Review.ChangedFields) != 2 || read.Review.ChangedFields[0].Field != "title" || read.Review.ChangedFields[1].Field != "markdown" || len(read.Review.MarkdownDiff) != 2 {
		t.Fatalf("unexpected deterministic diff: %+v", read.Review)
	}
	if strings.Contains(string(encoded), "<script>") {
		t.Fatal("JSON diff failed to escape HTML characters")
	}
	approve := proposalAction(p)
	approve["confirm"] = false
	workflowError(t, s, "admin", "proposals.approve", approve, "confirmation_required")
	approved := workflowCall(t, s, "admin", "proposals.approve", proposalAction(p))
	applied := getPost(t, approved)
	if applied.Title != "  提案 / candidate  " || applied.Markdown != candidate || applied.Revision != 2 || applied.Status != "changed" || strings.Contains(applied.HTML, "<script") {
		t.Fatalf("candidate changed or unsafe: %+v", applied)
	}
	_, public = s.Public()
	if public[0].Markdown != original.Markdown {
		t.Fatal("approval published candidate")
	}
	closed := getProposal(t, approved)
	if closed.Status != "approved" || closed.Revision != 2 || closed.AppliedPostID != original.ID || closed.AppliedRevision != 2 {
		t.Fatalf("bad approval: %+v", closed)
	}
	workflowError(t, s, "admin", "proposals.approve", proposalAction(closed), "conflict")
	stored := getProposal(t, workflowCall(t, s, "proposal", "proposals.get", map[string]any{"id": p.ID}))
	if !reflect.DeepEqual(stored.Candidate, p.Candidate) || !reflect.DeepEqual(stored.Base, p.Base) {
		t.Fatal("review mutated immutable proposal content")
	}
}

func TestProposalStaleBaseAndOwnership(t *testing.T) {
	s := testStore(t)
	original := create(t, s, "proposal-stale")
	p := getProposal(t, workflowCall(t, s, "agent-a", "proposals.create", proposalArgs(original, "candidate")))
	workflowError(t, s, "agent-b", "proposals.get", map[string]any{"id": p.ID}, "not_found")
	workflowError(t, s, "agent-b", "proposals.cancel", proposalAction(p), "not_found")
	workflowError(t, s, "proposal", "proposals.approve", proposalAction(p), "forbidden")
	workflowError(t, s, "draft", "proposals.reject", proposalAction(p), "forbidden")
	other := workflowCall(t, s, "agent-b", "proposals.list", map[string]any{})
	raw, _ := json.Marshal(other)
	if string(raw) != "{\"proposals\":[]}" {
		t.Fatalf("foreign proposals disclosed: %s", raw)
	}
	newer := getPost(t, call(t, s, "posts.update", edited(original)))
	before, _ := json.Marshal(s.State)
	workflowError(t, s, "admin", "proposals.approve", proposalAction(p), "conflict")
	after, _ := json.Marshal(s.State)
	if string(before) != string(after) {
		t.Fatal("stale approval changed state")
	}
	current := getPost(t, call(t, s, "posts.get", map[string]any{"id": original.ID}))
	if current.Revision != newer.Revision || current.Markdown != newer.Markdown {
		t.Fatal("stale approval clobbered newer draft")
	}
	workflowError(t, s, "agent-a", "proposals.create", proposalArgs(original, "stale submission"), "conflict")
	cancelled := getProposal(t, workflowCall(t, s, "agent-a", "proposals.cancel", proposalAction(p)))
	if cancelled.Status != "cancelled" {
		t.Fatal("proposal not cancelled")
	}
	workflowError(t, s, "admin", "proposals.approve", proposalAction(cancelled), "conflict")
}

func TestNewProposalRejectAndApprovalRetry(t *testing.T) {
	s := testStore(t)
	args := map[string]any{"candidate": ProposalContent{Title: "中文 Title", Slug: "new-candidate", Markdown: "中文 **body**", Tags: []string{}}, "idempotency_key": "shared"}
	p := getProposal(t, workflowCall(t, s, "agent-a", "proposals.create", args))
	again := getProposal(t, workflowCall(t, s, "agent-a", "proposals.create", args))
	if p.ID != again.ID {
		t.Fatal("proposal retry duplicated proposal")
	}
	other := getProposal(t, workflowCall(t, s, "agent-b", "proposals.create", args))
	if p.ID == other.ID || other.CreatedBy != "agent-b" {
		t.Fatal("idempotency cache leaked across actors")
	}
	if len(s.State.Posts) != 0 {
		t.Fatal("new proposal created a draft before review")
	}
	rejected := getProposal(t, workflowCall(t, s, "admin", "proposals.reject", proposalAction(other)))
	if rejected.Status != "rejected" || len(s.State.Posts) != 0 {
		t.Fatal("rejection changed posts")
	}
	approval := proposalAction(p)
	approval["idempotency_key"] = "approve-once"
	first := getPost(t, workflowCall(t, s, "admin", "proposals.approve", approval))
	second := getPost(t, workflowCall(t, s, "admin", "proposals.approve", approval))
	if first.ID != second.ID || len(s.State.Posts) != 1 || first.Status != "draft" || first.Revision != 1 {
		t.Fatal("approval retry created duplicate drafts")
	}
	_, public := s.Public()
	if len(public) != 0 {
		t.Fatal("approved new proposal was published")
	}
}

func TestProposalBackupAndValidation(t *testing.T) {
	s := testStore(t)
	original := create(t, s, "backup-proposal")
	p := getProposal(t, workflowCall(t, s, "proposal", "proposals.create", proposalArgs(original, "Backup candidate 中文")))
	backup := exported(t, s)
	dst := testStore(t)
	call(t, dst, "backup.restore", restoreArgs(backup))
	restored := getProposal(t, workflowCall(t, dst, "proposal", "proposals.get", map[string]any{"id": p.ID}))
	if !reflect.DeepEqual(restored, p) {
		t.Fatal("backup lost proposal")
	}
	applied := getPost(t, workflowCall(t, dst, "admin", "proposals.approve", proposalAction(restored)))
	if applied.Markdown != p.Candidate.Markdown {
		t.Fatal("restored proposal changed content")
	}
	for _, mutate := range []func(*State){
		func(st *State) { p := st.Proposals[p.ID]; p.BaseRevision = 999; st.Proposals[p.ID] = p },
		func(st *State) { p := st.Proposals[p.ID]; p.Status = "approved"; st.Proposals[p.ID] = p },
		func(st *State) { p := st.Proposals[p.ID]; p.Candidate.Title = ""; st.Proposals[p.ID] = p },
	} {
		bad := testStore(t)
		callError(t, bad, "backup.restore", restoreArgs(mutatedBackup(t, backup, mutate)), "validation")
	}
}

func TestProposalMarkdownDiffReconstructsBothInputs(t *testing.T) {
	for _, pair := range [][2]string{{"", "new"}, {"old", ""}, {"a\nb\nc\n", "a\nx\nc\n"}, {"同じ\n尾", "同じ\n尾\n"}, {"x\n", "x\n"}} {
		p := Proposal{Base: &ProposalContent{Markdown: pair[0]}, Candidate: ProposalContent{Markdown: pair[1]}}
		diff := reviewProposal(p).MarkdownDiff
		if pair[0] == pair[1] {
			if len(diff) != 0 {
				t.Fatal("unchanged diff not empty")
			}
			continue
		}
		var before, after strings.Builder
		for _, d := range diff {
			if d.Kind != "add" {
				before.WriteString(d.Text)
			}
			if d.Kind != "remove" {
				after.WriteString(d.Text)
			}
		}
		if before.String() != pair[0] || after.String() != pair[1] {
			t.Fatalf("diff lost text: %#v", diff)
		}
	}
}
