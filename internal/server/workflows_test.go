package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"folio/internal/core"
)

const proposalToken = "secret-proposal-regression-token"

func workflowServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	srv, _ := testServer(t)
	srv.ProposalToken = proposalToken
	return srv, srv.Handler()
}
func proposalFrom(t *testing.T, data map[string]json.RawMessage) core.Proposal {
	t.Helper()
	var p core.Proposal
	if e := json.Unmarshal(data["proposal"], &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func scheduleFrom(t *testing.T, data map[string]json.RawMessage) core.Schedule {
	t.Helper()
	var p core.Schedule
	if e := json.Unmarshal(data["schedule"], &p); e != nil {
		t.Fatal(e)
	}
	return p
}
func proposalArgs(slug string) map[string]any {
	return map[string]any{"candidate": map[string]any{"title": "Candidate title", "slug": slug, "markdown": "PROPOSED_PRIVATE_CONTENT"}, "idempotency_key": "shared-actor-retry-key"}
}
func errorCode(t *testing.T, h http.Handler, op, token string, args any, status int, code string) {
	t.Helper()
	raw, _ := json.Marshal(args)
	w := request(h, "POST", "/api/op/"+op, token, string(raw), nil)
	if w.Code != status {
		t.Fatalf("%s status=%d, want %d: %s", op, w.Code, status, w.Body.String())
	}
	var env struct {
		Error core.Error `json:"error"`
	}
	json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error.Code != code {
		t.Fatalf("%s code=%q, want %q", op, env.Error.Code, code)
	}
}

func TestProposalOwnershipAndIdempotencyActorIsolationHTTP(t *testing.T) {
	_, h := workflowServer(t)
	args := proposalArgs("actor-isolation")
	admin := proposalFrom(t, operation(t, h, "proposals.create", adminToken, args))
	agent := proposalFrom(t, operation(t, h, "proposals.create", proposalToken, args))
	if admin.ID == agent.ID || admin.CreatedBy != "admin" || agent.CreatedBy != "proposal" {
		t.Fatalf("actor retry key leaked cached proposal: admin=%+v agent=%+v", admin, agent)
	}
	again := proposalFrom(t, operation(t, h, "proposals.create", proposalToken, args))
	if !reflect.DeepEqual(again, agent) {
		t.Fatal("same-actor retry was not idempotent")
	}
	errorCode(t, h, "proposals.get", proposalToken, map[string]any{"id": admin.ID}, 404, "not_found")
	errorCode(t, h, "proposals.cancel", proposalToken, map[string]any{"id": admin.ID, "expected_revision": 1, "confirm": true}, 404, "not_found")
	owned := operation(t, h, "proposals.list", proposalToken, map[string]any{})
	var list []core.Proposal
	json.Unmarshal(owned["proposals"], &list)
	if len(list) != 1 || list[0].ID != agent.ID {
		t.Fatalf("proposal list leaked other actor records: %+v", list)
	}
	all := operation(t, h, "proposals.list", adminToken, map[string]any{})
	json.Unmarshal(all["proposals"], &list)
	if len(list) != 2 {
		t.Fatalf("admin could not see complete review queue: %+v", list)
	}
	// A privileged cached cancellation must not bypass proposal ownership.
	cancel := map[string]any{"id": admin.ID, "expected_revision": 1, "confirm": true, "idempotency_key": "shared-cancel"}
	operation(t, h, "proposals.cancel", adminToken, cancel)
	errorCode(t, h, "proposals.cancel", proposalToken, cancel, 404, "not_found")
	for _, path := range []string{"/api/public/site", "/rss.xml", "/sitemap.xml", "/api/public/search?q=PROPOSED_PRIVATE_CONTENT"} {
		w := request(h, "GET", path, "", "", nil)
		if strings.Contains(w.Body.String(), "actor-isolation") || strings.Contains(w.Body.String(), "PROPOSED_PRIVATE_CONTENT") {
			t.Fatalf("unapproved proposal leaked at %s", path)
		}
	}
}

func TestPermissionsMetadataMatchesHTTPEndpoints(t *testing.T) {
	_, h := workflowServer(t)
	for _, role := range []struct{ name, token string }{{"admin", adminToken}, {"draft", draftToken}, {"read", readToken}, {"proposal", proposalToken}} {
		t.Run(role.name, func(t *testing.T) {
			info := operation(t, h, "system.info", role.token, map[string]any{})
			var actor string
			var names []string
			json.Unmarshal(info["actor"], &actor)
			json.Unmarshal(info["permissions"], &names)
			if actor != role.name || !sort.StringsAreSorted(names) {
				t.Fatalf("invalid identity/permissions metadata: %s %v", actor, names)
			}
			allowed := map[string]bool{}
			for _, name := range names {
				if allowed[name] {
					t.Errorf("duplicate permission %s", name)
				}
				allowed[name] = true
			}
			for name, spec := range core.Specs {
				want := role.name == "admin" || (spec.Scope == "read" && spec.ReadOnly) || (role.name == "draft" && spec.Scope == "draft") || ((role.name == "draft" || role.name == "proposal") && spec.Scope == "proposal")
				if allowed[name] != want {
					t.Errorf("metadata permission %s=%v, want %v", name, allowed[name], want)
				}
				w := request(h, "POST", "/api/op/"+name, role.token, `{}`, nil)
				if want && (w.Code == 401 || w.Code == 403) {
					t.Errorf("advertised permission rejected: %s %d %s", name, w.Code, w.Body.String())
				}
				if !want && w.Code != 403 {
					t.Errorf("unpermitted endpoint not forbidden: %s %d %s", name, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestProposalApprovalRequiresExplicitReviewAndPreservesPublication(t *testing.T) {
	_, h := workflowServer(t)
	p := newPost(t, h)
	operation(t, h, "posts.publish", adminToken, lifecycleAction(p))
	args := proposalArgs("approved-new-slug")
	args["post_id"] = p.ID
	args["base_revision"] = p.Revision
	proposal := proposalFrom(t, operation(t, h, "proposals.create", proposalToken, args))
	decision := map[string]any{"id": proposal.ID, "expected_revision": 1, "confirm": false}
	errorCode(t, h, "proposals.approve", proposalToken, decision, 403, "forbidden")
	errorCode(t, h, "proposals.approve", adminToken, decision, 400, "confirmation_required")
	decision["confirm"] = true
	approved := operation(t, h, "proposals.approve", adminToken, decision)
	updated := postResult(t, approved)
	if updated.ID != p.ID || updated.Revision != 2 || updated.Status != "changed" || updated.Markdown != "PROPOSED_PRIVATE_CONTENT" {
		t.Fatalf("approval did not create expected private draft: %+v", updated)
	}
	for _, path := range []string{"/api/public/site", "/api/public/posts/public-original", "/rss.xml", "/sitemap.xml", "/posts/public-original"} {
		w := request(h, "GET", path, "", "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "public-original") || strings.Contains(w.Body.String(), "PROPOSED_PRIVATE_CONTENT") || strings.Contains(w.Body.String(), "approved-new-slug") {
			t.Fatalf("approval altered live content at %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	// A proposal against an older base may not overwrite a newer owner draft.
	second := proposalArgs("second-candidate")
	second["idempotency_key"] = "second-proposal"
	second["post_id"] = p.ID
	second["base_revision"] = updated.Revision
	pending := proposalFrom(t, operation(t, h, "proposals.create", proposalToken, second))
	newer := postResult(t, operation(t, h, "posts.update", adminToken, map[string]any{"id": p.ID, "expected_revision": updated.Revision, "title": "Owner newer draft", "slug": "owner-newer", "markdown": "OWNER_NEWER_PRIVATE"}))
	errorCode(t, h, "proposals.approve", adminToken, map[string]any{"id": pending.ID, "expected_revision": 1, "confirm": true}, 409, "conflict")
	after := postResult(t, operation(t, h, "posts.get", adminToken, map[string]any{"id": p.ID}))
	if !reflect.DeepEqual(after, newer) {
		t.Fatal("stale proposal approval overwrote owner draft")
	}
	still := proposalFrom(t, operation(t, h, "proposals.get", adminToken, map[string]any{"id": pending.ID}))
	if still.Status != "pending" || still.Revision != 1 {
		t.Fatal("failed approval closed or mutated proposal")
	}
	// Approval of a new-post proposal is also private.
	newArgs := proposalArgs("brand-new-private")
	newArgs["idempotency_key"] = "new-post-proposal"
	newProposal := proposalFrom(t, operation(t, h, "proposals.create", proposalToken, newArgs))
	created := postResult(t, operation(t, h, "proposals.approve", adminToken, map[string]any{"id": newProposal.ID, "expected_revision": 1, "confirm": true}))
	if created.Status != "draft" {
		t.Fatal("new proposal approval published implicitly")
	}
	if w := request(h, "GET", "/api/public/posts/brand-new-private", "", "", nil); w.Code != 404 {
		t.Fatal("new approved draft visible publicly")
	}
}

func TestHTTPSchedulePinsReviewedRevisionAndExecutesOnce(t *testing.T) {
	srv, h := workflowServer(t)
	p := newPost(t, h)
	due := time.Now().UTC().Add(time.Hour)
	args := map[string]any{"post_id": p.ID, "expected_revision": p.Revision, "publish_at": due.Format(time.RFC3339Nano), "confirm": true, "idempotency_key": "scheduled-pin"}
	for _, token := range []string{proposalToken, draftToken, readToken} {
		errorCode(t, h, "schedules.create", token, args, 403, "forbidden")
	}
	errorCode(t, h, "schedules.create", "", args, 401, "unauthorized")
	args["confirm"] = false
	errorCode(t, h, "schedules.create", adminToken, args, 400, "confirmation_required")
	args["confirm"] = true
	schedule := scheduleFrom(t, operation(t, h, "schedules.create", adminToken, args))
	retry := scheduleFrom(t, operation(t, h, "schedules.create", adminToken, args))
	if !reflect.DeepEqual(schedule, retry) {
		t.Fatal("schedule retry duplicated or mutated job")
	}
	newer := postResult(t, operation(t, h, "posts.update", draftToken, map[string]any{"id": p.ID, "expected_revision": p.Revision, "title": "Newer private title", "slug": "newer-private-slug", "markdown": "NEWER_PRIVATE_SCHEDULE_CONTENT"}))
	before, e := srv.Store.RunDue(context.Background(), due.Add(-time.Nanosecond))
	if e != nil || len(before.Published) != 0 || len(before.Blocked) != 0 {
		t.Fatalf("ran before due time: %+v %v", before, e)
	}
	for _, path := range []string{"/api/public/site", "/rss.xml", "/sitemap.xml", "/api/public/search?q=PUBLIC_ORIGINAL_BODY"} {
		w := request(h, "GET", path, "", "", nil)
		if strings.Contains(w.Body.String(), "public-original") || strings.Contains(w.Body.String(), "PUBLIC_ORIGINAL_BODY") || strings.Contains(w.Body.String(), "NEWER_PRIVATE_SCHEDULE_CONTENT") {
			t.Fatalf("schedule exposed content before due time: %s", path)
		}
	}
	result, e := srv.Store.RunDue(context.Background(), due)
	if e != nil || len(result.Published) != 1 || len(result.Blocked) != 0 {
		t.Fatalf("due execution failed: %+v %v", result, e)
	}
	for _, path := range []string{"/api/public/site", "/api/public/posts/public-original", "/rss.xml", "/posts/public-original"} {
		w := request(h, "GET", path, "", "", nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "PUBLIC_ORIGINAL_BODY") || strings.Contains(w.Body.String(), "NEWER_PRIVATE_SCHEDULE_CONTENT") || strings.Contains(w.Body.String(), "newer-private-slug") {
			t.Fatalf("pinned publication incorrect at %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	draft := postResult(t, operation(t, h, "posts.get", adminToken, map[string]any{"id": p.ID}))
	if draft.Revision != newer.Revision || draft.Markdown != newer.Markdown || draft.Status != "changed" || draft.PublishedRevision != p.Revision {
		t.Fatalf("scheduler modified latest private content: %+v", draft)
	}
	again, e := srv.Store.RunDue(context.Background(), due.Add(time.Hour))
	if e != nil || len(again.Published) != 0 || len(again.Blocked) != 0 {
		t.Fatalf("terminal schedule ran twice: %+v %v", again, e)
	}
	terminal := scheduleFrom(t, operation(t, h, "schedules.get", adminToken, map[string]any{"id": schedule.ID}))
	if terminal.Status != "published" || terminal.Revision != 2 || terminal.ExecutedAt == "" {
		t.Fatalf("terminal outcome missing: %+v", terminal)
	}
	audit := operation(t, h, "audit.list", adminToken, map[string]any{})
	var events []core.Event
	json.Unmarshal(audit["events"], &events)
	count := 0
	for _, event := range events {
		if event.Operation == "schedules.publish" && event.Target == schedule.ID {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("schedule executed %d times according to audit", count)
	}
}
