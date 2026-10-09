package core

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func getSchedule(t *testing.T, v any) Schedule {
	t.Helper()
	raw, _ := json.Marshal(v)
	var result struct {
		Schedule Schedule `json:"schedule"`
	}
	if e := json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	return result.Schedule
}
func scheduleArgs(p Post, at time.Time) map[string]any {
	return map[string]any{"post_id": p.ID, "expected_revision": p.Revision, "publish_at": at.Format(time.RFC3339Nano), "confirm": true}
}
func scheduleAction(p Schedule) map[string]any {
	return map[string]any{"id": p.ID, "expected_revision": p.Revision, "confirm": true}
}
func runDue(t *testing.T, s *Store, at time.Time) DueResult {
	t.Helper()
	v, e := s.RunDue(context.Background(), at)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func scheduleAuditCount(s *Store, postID string) int {
	n := 0
	for _, e := range s.State.Audit {
		if e.Operation == "posts.publish" && e.Target == postID && e.Actor == "scheduler" {
			n++
		}
	}
	return n
}

func TestScheduledPinnedRevisionSurvivesRestartAndNewDraft(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	p := create(t, s, "restart-schedule")
	proposal := getProposal(t, workflowCall(t, s, "proposal", "proposals.create", proposalArgs(p, "已审阅 approved bilingual body")))
	approved := getPost(t, workflowCall(t, s, "admin", "proposals.approve", proposalAction(proposal)))
	due := time.Now().Add(2 * time.Hour).UTC()
	scheduled := getSchedule(t, call(t, s, "schedules.create", scheduleArgs(approved, due)))
	if scheduled.PostRevision != approved.Revision || !reflectContentEqual(proposalContent(scheduled.Snapshot), proposalContent(approved)) {
		t.Fatal("schedule did not pin reviewed revision")
	}
	newer := getPost(t, call(t, s, "posts.update", edited(approved)))
	if result := runDue(t, s, due.Add(-time.Nanosecond)); len(result.Published) != 0 {
		t.Fatal("published before exact due time")
	}
	_, public := s.Public()
	if len(public) != 0 {
		t.Fatal("pending snapshot leaked publicly")
	}
	s.Close()
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	result := runDue(t, s, due.Add(time.Hour))
	if len(result.Published) != 1 || len(result.Blocked) != 0 {
		t.Fatalf("restart did not catch up: %+v", result)
	}
	_, public = s.Public()
	if len(public) != 1 || public[0].Revision != approved.Revision || public[0].Markdown != approved.Markdown || public[0].Title != approved.Title {
		t.Fatalf("wrong revision published: %+v", public)
	}
	current := getPost(t, call(t, s, "posts.get", map[string]any{"id": p.ID}))
	if !reflectContentEqual(proposalContent(current), proposalContent(newer)) || current.Revision != newer.Revision || current.Status != "changed" || current.PublishedRevision != approved.Revision {
		t.Fatal("scheduled activation changed newer private draft")
	}
	if len(s.State.Posts[p.ID].Revisions) != 3 {
		t.Fatal("scheduled publication appended a content revision")
	}
	for i := 0; i < 3; i++ {
		if len(runDue(t, s, due.Add(24*time.Hour)).Published) != 0 {
			t.Fatal("schedule published twice")
		}
	}
	if scheduleAuditCount(s, p.ID) != 1 {
		t.Fatal("duplicate publication audit")
	}
	terminal := getSchedule(t, call(t, s, "schedules.get", map[string]any{"id": scheduled.ID}))
	if terminal.Status != "published" || terminal.ExecutedAt == "" || terminal.Revision != 2 {
		t.Fatalf("missing durable outcome: %+v", terminal)
	}
}

func TestScheduledDoubleWorkerExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	a, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	p := create(t, a, "double-worker")
	due := time.Now().Add(time.Hour)
	call(t, a, "schedules.create", scheduleArgs(p, due))
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan DueResult, 2)
	errs := make(chan error, 2)
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			<-start
			v, e := s.RunDue(context.Background(), due)
			results <- v
			errs <- e
		}(s)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	count := 0
	for r := range results {
		count += len(r.Published)
	}
	if count != 1 {
		t.Fatalf("workers published %d times", count)
	}
	call(t, a, "schedules.list", map[string]any{})
	if scheduleAuditCount(a, p.ID) != 1 {
		t.Fatal("committed duplicate publication")
	}
}

func TestScheduleCancellationAndRescheduleCAS(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "reschedule")
	due := time.Now().Add(time.Hour)
	createArgs := scheduleArgs(p, due)
	createArgs["confirm"] = false
	callError(t, s, "schedules.create", createArgs, "confirmation_required")
	createArgs = scheduleArgs(p, time.Now().Add(-time.Hour))
	callError(t, s, "schedules.create", createArgs, "validation")
	createArgs = scheduleArgs(p, due)
	createArgs["publish_at"] = "2030-02-01T12:00:00"
	callError(t, s, "schedules.create", createArgs, "validation")
	first := getSchedule(t, call(t, s, "schedules.create", scheduleArgs(p, due)))
	callError(t, s, "schedules.create", scheduleArgs(p, due), "conflict")
	newer := getPost(t, call(t, s, "posts.update", edited(p)))
	move := scheduleAction(first)
	move["publish_at"] = due.Add(time.Hour).Format(time.RFC3339Nano)
	move["expected_post_revision"] = p.Revision
	callError(t, s, "schedules.reschedule", move, "conflict")
	move["expected_post_revision"] = newer.Revision
	moved := getSchedule(t, call(t, s, "schedules.reschedule", move))
	if moved.PostRevision != newer.Revision || moved.Revision != 2 {
		t.Fatal("reschedule did not explicitly re-pin reviewed draft")
	}
	callError(t, s, "schedules.cancel", scheduleAction(first), "conflict")
	if len(runDue(t, s, due).Published) != 0 {
		t.Fatal("old due time still fired")
	}
	cancelled := getSchedule(t, call(t, s, "schedules.cancel", scheduleAction(moved)))
	if cancelled.Status != "cancelled" {
		t.Fatal("cancel did not persist")
	}
	if len(runDue(t, s, due.Add(5*time.Hour)).Published) != 0 {
		t.Fatal("cancelled schedule published")
	}
	callError(t, s, "schedules.reschedule", map[string]any{"id": cancelled.ID, "expected_revision": cancelled.Revision, "expected_post_revision": newer.Revision, "publish_at": due.Add(6 * time.Hour).Format(time.RFC3339Nano), "confirm": true}, "conflict")
}

func TestScheduleVisibilityIntentCancelsJobs(t *testing.T) {
	for _, op := range []string{"posts.publish", "posts.unpublish", "posts.delete"} {
		t.Run(op, func(t *testing.T) {
			s := testStore(t)
			p := create(t, s, "manual-intent")
			due := time.Now().Add(time.Hour)
			scheduled := getSchedule(t, call(t, s, "schedules.create", scheduleArgs(p, due)))
			call(t, s, op, action(p, true))
			status := getSchedule(t, call(t, s, "schedules.get", map[string]any{"id": scheduled.ID}))
			if status.Status != "cancelled" || status.Reason != op {
				t.Fatal("manual intent did not cancel future job")
			}
			if len(runDue(t, s, due).Published) != 0 {
				t.Fatal("job undid manual intent")
			}
			_, public := s.Public()
			want := 0
			if op == "posts.publish" {
				want = 1
			}
			if len(public) != want {
				t.Fatal("incorrect manual publication outcome")
			}
			if op == "posts.delete" {
				call(t, s, "posts.recover", action(p, true))
				if len(runDue(t, s, due.Add(time.Hour)).Published) != 0 {
					t.Fatal("recovery revived cancelled job")
				}
			}
		})
	}
}

func TestScheduledSlugConflictBlocksUntilExplicitReschedule(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "pinned-slug")
	due := time.Now().Add(time.Hour)
	schedule := getSchedule(t, call(t, s, "schedules.create", scheduleArgs(p, due)))
	newer := getPost(t, call(t, s, "posts.update", edited(p)))
	other := create(t, s, p.Slug)
	result := runDue(t, s, due)
	if len(result.Published) != 0 || len(result.Blocked) != 1 || result.Blocked[0].Reason != "slug_conflict" {
		t.Fatalf("collision not blocked: %+v", result)
	}
	_, public := s.Public()
	if len(public) != 0 {
		t.Fatal("blocked schedule leaked publicly")
	}
	blocked := getSchedule(t, call(t, s, "schedules.get", map[string]any{"id": schedule.ID}))
	call(t, s, "posts.delete", action(other, true))
	if len(runDue(t, s, due.Add(time.Hour)).Published) != 0 {
		t.Fatal("blocked job retried without explicit approval")
	}
	move := scheduleAction(blocked)
	move["expected_post_revision"] = newer.Revision
	move["publish_at"] = due.Add(2 * time.Hour).Format(time.RFC3339Nano)
	call(t, s, "schedules.reschedule", move)
	published := runDue(t, s, due.Add(2*time.Hour))
	if len(published.Published) != 1 || published.Published[0].PostRevision != newer.Revision {
		t.Fatal("explicit reschedule failed")
	}
}

func TestScheduleRaceCancellationOrRescheduleWithDueWorker(t *testing.T) {
	for _, op := range []string{"schedules.cancel", "schedules.reschedule"} {
		t.Run(op, func(t *testing.T) {
			dir := t.TempDir()
			a, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			b, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer b.Close()
			p := create(t, a, "schedule-race")
			due := time.Now().Add(time.Hour)
			schedule := getSchedule(t, call(t, a, "schedules.create", scheduleArgs(p, due)))
			args := scheduleAction(schedule)
			if op == "schedules.reschedule" {
				args["expected_post_revision"] = p.Revision
				args["publish_at"] = due.Add(time.Hour).Format(time.RFC3339Nano)
			}
			raw, _ := json.Marshal(args)
			start := make(chan struct{})
			worker := make(chan struct {
				r DueResult
				e error
			}, 1)
			write := make(chan error, 1)
			go func() {
				<-start
				r, e := a.RunDue(context.Background(), due)
				worker <- struct {
					r DueResult
					e error
				}{r, e}
			}()
			go func() { <-start; _, e := b.Call(context.Background(), op, raw, "admin"); write <- e }()
			close(start)
			outcome := <-worker
			err := <-write
			if outcome.e != nil {
				t.Fatal(outcome.e)
			}
			if err != nil {
				var ce *Error
				if !errors.As(err, &ce) || ce.Code != "conflict" || len(outcome.r.Published) != 1 {
					t.Fatalf("invalid losing update: %v %+v", err, outcome.r)
				}
			} else if len(outcome.r.Published) != 0 {
				t.Fatal("both cancellation/reschedule and obsolete publication won")
			}
			call(t, a, "schedules.list", map[string]any{})
			if scheduleAuditCount(a, p.ID) > 1 {
				t.Fatal("race duplicated publication")
			}
		})
	}
}

func TestScheduleBackupRetainsPinnedSnapshotAndValidatesIt(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "schedule-backup")
	due := time.Now().Add(time.Hour)
	schedule := getSchedule(t, call(t, s, "schedules.create", scheduleArgs(p, due)))
	call(t, s, "posts.update", edited(p))
	backup := exported(t, s)
	dst := testStore(t)
	call(t, dst, "backup.restore", restoreArgs(backup))
	restored := getSchedule(t, call(t, dst, "schedules.get", map[string]any{"id": schedule.ID}))
	if !reflect.DeepEqual(restored, schedule) {
		t.Fatal("backup lost snapshot")
	}
	if len(runDue(t, dst, due).Published) != 1 {
		t.Fatal("restored due schedule did not run")
	}
	for _, mutate := range []func(*State){
		func(st *State) {
			v := st.Schedules[schedule.ID]
			v.Snapshot.Markdown = "tampered"
			st.Schedules[v.ID] = v
		},
		func(st *State) { v := st.Schedules[schedule.ID]; v.PublishAt = "not-a-date"; st.Schedules[v.ID] = v },
		func(st *State) { v := st.Schedules[schedule.ID]; v.ID = "duplicate"; st.Schedules[v.ID] = v },
		func(st *State) { v := st.Schedules[schedule.ID]; v.Status = "published"; st.Schedules[v.ID] = v },
	} {
		bad := testStore(t)
		callError(t, bad, "backup.restore", restoreArgs(mutatedBackup(t, backup, mutate)), "validation")
	}
}

func TestWorkflowRollbackAndLogicalSize(t *testing.T) {
	s := testStore(t)
	p := create(t, s, "rollback-proposal")
	proposal := getProposal(t, workflowCall(t, s, "proposal", "proposals.create", proposalArgs(p, "approved after storage rollback")))
	before, _ := json.Marshal(s.State)
	baseSize := workflowSize(s.State)
	if baseSize < 300 {
		t.Fatal("workflow size omitted proposal")
	}
	if _, e := s.db.Exec("CREATE TRIGGER reject_metadata BEFORE UPDATE ON state BEGIN SELECT RAISE(FAIL, 'injected persistence failure'); END"); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(proposalAction(proposal))
	if _, e := s.Call(context.Background(), "proposals.approve", raw, "admin"); e == nil {
		t.Fatal("expected persistence failure")
	}
	after, _ := json.Marshal(s.State)
	if string(before) != string(after) {
		t.Fatal("proposal failure modified in-memory state")
	}
	if _, e := s.db.Exec("DROP TRIGGER reject_metadata"); e != nil {
		t.Fatal(e)
	}
	approved := getPost(t, workflowCall(t, s, "admin", "proposals.approve", proposalAction(proposal)))
	due := time.Now().Add(time.Hour)
	call(t, s, "schedules.create", scheduleArgs(approved, due))
	before, _ = json.Marshal(s.State)
	if _, e := s.db.Exec("CREATE TRIGGER reject_metadata BEFORE UPDATE ON state BEGIN SELECT RAISE(FAIL, 'injected persistence failure'); END"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.RunDue(context.Background(), due); e == nil {
		t.Fatal("expected scheduler persistence failure")
	}
	after, _ = json.Marshal(s.State)
	if string(before) != string(after) {
		t.Fatal("failed scheduler mutated state")
	}
	if _, e := s.db.Exec("DROP TRIGGER reject_metadata"); e != nil {
		t.Fatal(e)
	}
	_, public := s.Public()
	if len(public) != 0 {
		t.Fatal("failed transaction published content")
	}
	if len(runDue(t, s, due).Published) != 1 {
		t.Fatal("failed job could not retry")
	}
}
