package core

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	portable "folio/internal/migration"
)

const migrationChineseBody = "# 双语原文\n\nMIGRATION_PRIVATE 中文原文。\n\nOriginal English body.\n"
const migrationChineseFile = "---\ntitle: 双语原文 / Bilingual baseline\nslug: migration-bilingual\ndescription: 原始摘录 / Original excerpt\ntags: [\"中文\", \"English\"]\ncategory: 工作流 / Workflows\ndate: 2020-01-02\npublishDate: 2020-02-03T09:00:00+08:00\ndraft: false\nlegacy_id: PRIVATE_SOURCE_17\n---\n" + migrationChineseBody

type migrationPlanResult struct {
	Plan             portable.ImportPlan   `json:"plan"`
	TargetInstanceID string                `json:"target_instance_id"`
	CanApply         bool                  `json:"can_apply"`
	ValidationIssues []portable.Diagnostic `json:"validation_issues"`
	Summary          map[string]int        `json:"summary"`
}
type migrationApplyResult struct {
	Report           portable.Report `json:"report"`
	TargetInstanceID string          `json:"target_instance_id"`
}
type migrationExportResult struct {
	Files []struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		SHA256  string `json:"sha256"`
		Bytes   int    `json:"bytes"`
	} `json:"files"`
	Manifest portable.ExportManifest `json:"manifest"`
}

func migrationResult[T any](t *testing.T, result any) T {
	t.Helper()
	var value T
	raw, e := json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &value); e != nil {
		t.Fatal(e)
	}
	return value
}
func planMigration(t *testing.T, s *Store, files []migrationFile, conflict string) migrationPlanResult {
	t.Helper()
	return migrationResult[migrationPlanResult](t, workflowCall(t, s, "admin", "migration.plan", map[string]any{"files": files, "conflict": conflict}))
}
func applyMigrationArgs(p migrationPlanResult) map[string]any {
	return map[string]any{"plan": p.Plan, "target_instance_id": p.TargetInstanceID, "confirm": true}
}
func applyMigration(t *testing.T, s *Store, p migrationPlanResult, more map[string]any) migrationApplyResult {
	t.Helper()
	args := applyMigrationArgs(p)
	for k, v := range more {
		args[k] = v
	}
	return migrationResult[migrationApplyResult](t, workflowCall(t, s, "admin", "migration.apply", args))
}
func migrationState(t *testing.T, s *Store) string {
	t.Helper()
	raw, e := json.Marshal(s.State)
	if e != nil {
		t.Fatal(e)
	}
	return string(raw)
}
func simpleMigrationFile(path, slug, body string) migrationFile {
	return migrationFile{path, "---\ntitle: Imported " + slug + "\nslug: " + slug + "\n---\n" + body}
}

func TestMigrationPlanApplyExportPrivateProvenance(t *testing.T) {
	s := testStore(t)
	before := migrationState(t, s)
	p := planMigration(t, s, []migrationFile{{"content/中文.md", migrationChineseFile}}, "error")
	if !p.CanApply || p.Summary["create"] != 1 || p.TargetInstanceID != s.InstanceID() || migrationState(t, s) != before {
		t.Fatal("planning changed state or returned invalid target/summary")
	}
	repeated := planMigration(t, s, []migrationFile{{"content/中文.md", migrationChineseFile}}, "error")
	if !reflect.DeepEqual(p, repeated) {
		t.Fatal("identical planning was nondeterministic")
	}
	a := applyMigration(t, s, p, nil)
	if !a.Report.Complete || len(a.Report.Outcomes) != 1 || a.Report.Outcomes[0].Status != "applied" {
		t.Fatalf("import incomplete: %+v", a)
	}
	id := a.Report.Outcomes[0].PostID
	post := getPost(t, call(t, s, "posts.get", map[string]any{"id": id}))
	if post.Markdown != migrationChineseBody || post.Status != "draft" || post.Revision != 1 || post.PublishedAt != "" || strings.HasPrefix(post.CreatedAt, "2020-") {
		t.Fatalf("source publication metadata altered private creation: %+v", post)
	}
	source := s.State.ImportSources[id]
	if source.SourcePath != "content/中文.md" || source.Date != "2020-01-02T00:00:00Z" || source.PublishDate != "2020-02-03T01:00:00Z" || source.SourceDraft || source.Extra["legacy_id"] != "PRIVATE_SOURCE_17" || len(source.Warnings) == 0 {
		t.Fatalf("source provenance lost: %+v", source)
	}
	_, public := s.Public()
	if len(public) != 0 {
		t.Fatal("draft:false source was published")
	}
	after := migrationState(t, s)
	if retry := applyMigration(t, s, p, nil); !reflect.DeepEqual(a, retry) || migrationState(t, s) != after {
		t.Fatal("same frozen plan duplicated or changed a write")
	}
	out := migrationResult[migrationExportResult](t, workflowCall(t, s, "admin", "migration.export", map[string]any{}))
	if len(out.Files) != 1 || len(out.Manifest.Items) != 1 {
		t.Fatal("missing export file/manifest")
	}
	file := out.Files[0]
	if file.Bytes != len([]byte(file.Content)) || file.SHA256 != digest([]byte(file.Content)) || !strings.Contains(file.Content, "draft: true\n") || !strings.HasSuffix(file.Content, migrationChineseBody) || strings.Contains(file.Content, "PRIVATE_SOURCE_17") {
		t.Fatal("portable Markdown changed body, emitted unsafe metadata, or checksum/byte size differs")
	}
	if out.Manifest.Items[0].Source.Extra["legacy_id"] != "PRIVATE_SOURCE_17" || out.Manifest.Items[0].Source.Draft {
		t.Fatal("manifest lost private original metadata")
	}
	if migrationState(t, s) != after {
		t.Fatal("export mutated instance")
	}
	if len(s.State.Audit) != 1 || s.State.Audit[0].Operation != "posts.create" || s.State.Audit[0].Actor != "admin" || s.State.Audit[0].Target != id {
		t.Fatal("migration bypassed ordinary audited write operation")
	}
}

func TestMigrationScopeConfirmationTargetAndFrozenPlan(t *testing.T) {
	s := testStore(t)
	p := planMigration(t, s, []migrationFile{{"中文.md", migrationChineseFile}}, "error")
	before := migrationState(t, s)
	for _, actor := range []string{"read", "draft", "proposal", "other-agent"} {
		for _, op := range []string{"migration.plan", "migration.apply", "migration.export"} {
			workflowError(t, s, actor, op, map[string]any{}, "forbidden")
		}
	}
	args := applyMigrationArgs(p)
	args["confirm"] = false
	workflowError(t, s, "admin", "migration.apply", args, "confirmation_required")
	for _, target := range []string{"", testStore(t).InstanceID()} {
		args = applyMigrationArgs(p)
		args["target_instance_id"] = target
		workflowError(t, s, "admin", "migration.apply", args, "conflict")
	}
	args = applyMigrationArgs(p)
	args["unexpected"] = true
	workflowError(t, s, "admin", "migration.apply", args, "validation")
	mutated := migrationResult[migrationPlanResult](t, p)
	mutated.Plan.Entries[0].Document.Markdown += "unreviewed"
	workflowError(t, s, "admin", "migration.apply", applyMigrationArgs(mutated), "validation")
	if migrationState(t, s) != before {
		t.Fatal("rejected migration changed state")
	}
}

func TestMigrationInvalidBatchCannotApplyValidSubset(t *testing.T) {
	for _, invalid := range []migrationFile{{"../outside.md", "# Escape"}, {"invalid.md", "---\ntitle: broken\n"}} {
		t.Run(invalid.Path, func(t *testing.T) {
			s := testStore(t)
			before := migrationState(t, s)
			p := planMigration(t, s, []migrationFile{{"good.md", migrationChineseFile}, invalid}, "error")
			if p.CanApply || len(p.ValidationIssues) == 0 || p.Summary["blocked"] == 0 {
				t.Fatal("invalid batch was advertised as applicable")
			}
			workflowError(t, s, "admin", "migration.apply", applyMigrationArgs(p), "validation")
			if migrationState(t, s) != before {
				t.Fatal("invalid preview plan imported its valid subset")
			}
		})
	}
}

func TestMigrationConflictPoliciesAndReservedPublishedSlug(t *testing.T) {
	s := testStore(t)
	post := create(t, s, "existing-migration")
	file := simpleMigrationFile("existing.md", post.Slug, "Replacement 中文\n")
	blocked := planMigration(t, s, []migrationFile{file}, "error")
	if blocked.CanApply || blocked.Summary["blocked"] != 1 {
		t.Fatal("default conflict did not block")
	}
	workflowError(t, s, "admin", "migration.apply", applyMigrationArgs(blocked), "validation")
	skipped := planMigration(t, s, []migrationFile{file}, "skip")
	before := migrationState(t, s)
	r := applyMigration(t, s, skipped, nil)
	if !r.Report.Complete || r.Report.Outcomes[0].Status != "skipped" || r.Report.Outcomes[0].PostID != post.ID || migrationState(t, s) != before {
		t.Fatal("skip policy wrote content or lost target identity")
	}
	update := planMigration(t, s, []migrationFile{file}, "update")
	if update.Plan.Entries[0].TargetID != post.ID || update.Plan.Entries[0].ExpectedRevision != post.Revision {
		t.Fatal("update lacks exact identity/revision")
	}
	newer := getPost(t, call(t, s, "posts.update", map[string]any{"id": post.ID, "expected_revision": post.Revision, "title": "Owner edit", "slug": post.Slug, "markdown": "Keep owner work"}))
	before = migrationState(t, s)
	failed := applyMigration(t, s, update, nil)
	if failed.Report.Complete || failed.Report.Outcomes[0].Status != "failed" || migrationState(t, s) != before {
		t.Fatal("stale migration overwrote owner edit or claimed success")
	}
	fresh := planMigration(t, s, []migrationFile{file}, "update")
	r = applyMigration(t, s, fresh, nil)
	if !r.Report.Complete || r.Report.Outcomes[0].PostID != post.ID || r.Report.Outcomes[0].Revision != newer.Revision+1 {
		t.Fatal("reviewed fresh update did not advance exact target")
	}
	updated := getPost(t, call(t, s, "posts.get", map[string]any{"id": post.ID}))
	call(t, s, "posts.publish", action(updated, true))
	call(t, s, "posts.update", edited(updated))
	reserved := planMigration(t, s, []migrationFile{file}, "error")
	if reserved.CanApply || len(reserved.ValidationIssues) != 1 || reserved.ValidationIssues[0].Code != "published_slug_reserved" {
		t.Fatal("planner ignored published slug reserved by a different draft slug")
	}
	workflowError(t, s, "admin", "migration.apply", applyMigrationArgs(reserved), "validation")
}

func TestMigrationPartialApplyRetryAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	p := planMigration(t, s, []migrationFile{
		simpleMigrationFile("a.md", "migration-a", "First 中文\n"),
		simpleMigrationFile("b.md", "migration-b", "Second 中文\n"),
		simpleMigrationFile("c.md", "migration-c", "Third 中文\n"),
	}, "error")
	collision := create(t, s, "migration-b")
	first := applyMigration(t, s, p, nil)
	if first.Report.Complete || first.Report.Outcomes[0].Status != "applied" || first.Report.Outcomes[1].Status != "failed" || first.Report.Outcomes[2].Status != "pending" || len(s.State.Posts) != 2 {
		t.Fatalf("incorrect stop-on-error report: %+v", first)
	}
	firstID := first.Report.Outcomes[0].PostID
	continuing := applyMigration(t, s, p, map[string]any{"continue_on_error": true})
	if continuing.Report.Complete || continuing.Report.Outcomes[0].PostID != firstID || continuing.Report.Outcomes[1].Status != "failed" || continuing.Report.Outcomes[2].Status != "applied" || len(s.State.Posts) != 3 {
		t.Fatal("continue-on-error failed to preserve receipt and apply later row")
	}
	thirdID := continuing.Report.Outcomes[2].PostID
	call(t, s, "posts.delete", action(collision, true))
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if s.InstanceID() != p.TargetInstanceID {
		t.Fatal("restart changed migration target")
	}
	complete := applyMigration(t, s, p, nil)
	if !complete.Report.Complete || complete.Report.Outcomes[0].PostID != firstID || complete.Report.Outcomes[2].PostID != thirdID || len(s.State.Posts) != 4 {
		t.Fatalf("restart retry lost successful receipts: %+v", complete)
	}
	for _, outcome := range complete.Report.Outcomes {
		if outcome.Status != "applied" || outcome.Revision != 1 || len(s.State.Posts[outcome.PostID].Revisions) != 1 {
			t.Fatal("retry duplicated content revision")
		}
	}
	before := migrationState(t, s)
	applyMigration(t, s, p, nil)
	if migrationState(t, s) != before {
		t.Fatal("completed retry changed state")
	}
}

func TestMigrationBackupMetadataAndExportSnapshotSelection(t *testing.T) {
	s := testStore(t)
	p := planMigration(t, s, []migrationFile{{"content/中文.md", migrationChineseFile}}, "error")
	a := applyMigration(t, s, p, nil)
	id := a.Report.Outcomes[0].PostID
	post := getPost(t, call(t, s, "posts.get", map[string]any{"id": id}))
	call(t, s, "posts.publish", action(post, true))
	call(t, s, "posts.update", edited(post))
	for _, status := range []string{"all", "draft", "published"} {
		out := migrationResult[migrationExportResult](t, workflowCall(t, s, "admin", "migration.export", map[string]any{"status": status, "ids": []string{id}}))
		body := "PRIVATE_SECRET_137 body"
		if status == "published" {
			body = migrationChineseBody
		}
		if len(out.Files) != 1 || !strings.HasSuffix(out.Files[0].Content, body) {
			t.Fatal("export selected wrong private/live snapshot")
		}
	}
	backup := exported(t, s)
	dst := testStore(t)
	call(t, dst, "backup.restore", restoreArgs(backup))
	if dst.InstanceID() == s.InstanceID() || !reflect.DeepEqual(dst.State.ImportSources, s.State.ImportSources) {
		t.Fatal("backup lost migration provenance or overwrote destination identity")
	}
	workflowError(t, dst, "admin", "migration.apply", applyMigrationArgs(p), "conflict")
	for _, mutate := range []func(*State){
		func(st *State) { x := st.ImportSources[id]; x.SourcePath = "../escape.md"; st.ImportSources[id] = x },
		func(st *State) { x := st.ImportSources[id]; x.ContentHash = "invalid"; st.ImportSources[id] = x },
		func(st *State) { st.ImportSources["missing"] = st.ImportSources[id] },
	} {
		callError(t, testStore(t), "backup.restore", restoreArgs(mutatedBackup(t, backup, mutate)), "validation")
	}
	_, public := dst.Public()
	raw, _ := json.Marshal(public)
	for _, marker := range []string{"PRIVATE_SOURCE_17", "source_path", "source_draft", "PRIVATE_SECRET_137"} {
		if strings.Contains(string(raw), marker) {
			t.Fatal("public snapshots expose private migration data")
		}
	}
}

func TestMigrationProposalAndScheduledPinnedPublication(t *testing.T) {
	dir := t.TempDir()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.Close() }()
	plan := planMigration(t, s, []migrationFile{{"中文.md", migrationChineseFile}}, "error")
	applied := applyMigration(t, s, plan, nil)
	original := getPost(t, call(t, s, "posts.get", map[string]any{"id": applied.Report.Outcomes[0].PostID}))
	stale := getProposal(t, workflowCall(t, s, "proposal", "proposals.create", proposalArgs(original, "Old candidate 中文")))
	owner := getPost(t, call(t, s, "posts.update", edited(original)))
	before := migrationState(t, s)
	workflowError(t, s, "admin", "proposals.approve", proposalAction(stale), "conflict")
	if migrationState(t, s) != before {
		t.Fatal("stale migrated proposal changed state")
	}
	candidate := getProposal(t, workflowCall(t, s, "proposal", "proposals.create", proposalArgs(owner, "确切审阅稿 Exact reviewed text\n")))
	approved := getPost(t, workflowCall(t, s, "admin", "proposals.approve", proposalAction(candidate)))
	if approved.Status != "draft" {
		t.Fatal("migration proposal approval published content")
	}
	due := time.Now().Add(time.Hour)
	schedule := getSchedule(t, workflowCall(t, s, "admin", "schedules.create", scheduleArgs(approved, due)))
	newer := getPost(t, call(t, s, "posts.update", edited(approved)))
	if _, public := s.Public(); len(public) != 0 {
		t.Fatal("scheduled import became public early")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	result := runDue(t, s, due.Add(time.Minute))
	if len(result.Published) != 1 || result.Published[0].ID != schedule.ID {
		t.Fatal("restart missed scheduled import")
	}
	_, public := s.Public()
	if len(public) != 1 || !reflectContentEqual(proposalContent(public[0]), proposalContent(approved)) || public[0].Revision != approved.Revision {
		t.Fatal("restart did not publish exact reviewed import revision")
	}
	current := getPost(t, call(t, s, "posts.get", map[string]any{"id": original.ID}))
	if !reflectContentEqual(proposalContent(current), proposalContent(newer)) || current.Revision != newer.Revision || current.Status != "changed" {
		t.Fatal("publication changed newer private head")
	}
	if len(runDue(t, s, due.Add(time.Hour)).Published) != 0 || scheduleAuditCount(s, original.ID) != 1 {
		t.Fatal("schedule imported content twice")
	}
}

func TestMigrationStrictLimitsAndCancelledContext(t *testing.T) {
	s := testStore(t)
	for _, args := range []any{map[string]any{"files": []migrationFile{}}, map[string]any{"files": make([]migrationFile, 201)}, map[string]any{"files": []migrationFile{{"a.md", "# A"}}, "conflict": "replace-everything"}, map[string]any{"files": []migrationFile{{"a.md", "# A"}}, "unexpected": true}} {
		workflowError(t, s, "admin", "migration.plan", args, "validation")
	}
	for _, args := range []any{map[string]any{"status": "trash"}, map[string]any{"ids": make([]string, 201)}, map[string]any{"unexpected": true}} {
		workflowError(t, s, "admin", "migration.export", args, "validation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Call(ctx, "migration.plan", json.RawMessage(`{"files":[{"path":"a.md","content":"# A"}]}`), "admin"); e == nil {
		t.Fatal("cancelled migration planning succeeded")
	}
}
