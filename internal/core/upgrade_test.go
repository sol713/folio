package core

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This mirrors v0.1's exact JSON field order and omits v0.2 workflow fields.
// Its checksum is generated independently from the current State type.
type legacyStateV1 struct {
	Schema           int                   `json:"schema"`
	Revision         int                   `json:"revision"`
	Settings         Settings              `json:"settings"`
	SettingsRevision int                   `json:"settings_revision"`
	Posts            map[string]*Record    `json:"posts"`
	Media            map[string]Media      `json:"media"`
	Audit            []Event               `json:"audit"`
	Idempotency      map[string]Idempotent `json:"idempotency"`
}

func legacyFixture(t *testing.T) (legacyStateV1, Backup, string) {
	t.Helper()
	src := testStore(t)
	p := create(t, src, "legacy-published")
	call(t, src, "posts.publish", action(p, true))
	call(t, src, "posts.update", edited(p))
	call(t, src, "media.upload", map[string]any{"name": "legacy.png", "base64": tinyPNG})
	snapshot := exported(t, src)
	var st State
	json.Unmarshal(snapshot.State, &st)
	legacy := legacyStateV1{1, st.Revision, st.Settings, st.SettingsRevision, st.Posts, st.Media, st.Audit, st.Idempotency}
	raw, e := json.Marshal(legacy)
	if e != nil {
		t.Fatal(e)
	}
	return legacy, Backup{Format: "folio-backup", Version: 1, CreatedAt: now(), SHA256: digest(raw), State: raw}, p.ID
}
func writeLegacyDatabase(t *testing.T, dir string, legacy legacyStateV1, splitRows bool) {
	t.Helper()
	db, e := sql.Open("sqlite", filepath.Join(dir, "folio.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, q := range []string{"CREATE TABLE migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)", "INSERT INTO migrations VALUES(1,'2025-01-01T00:00:00Z')", "CREATE TABLE state(id INTEGER PRIMARY KEY CHECK(id=1), document TEXT NOT NULL)"} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if splitRows {
		for _, q := range []string{"CREATE TABLE posts(id TEXT PRIMARY KEY, document TEXT NOT NULL)", "CREATE TABLE media(id TEXT PRIMARY KEY, metadata TEXT NOT NULL, bytes BLOB NOT NULL)", "INSERT INTO migrations VALUES(2,'2025-01-01T00:00:00Z')"} {
			if _, e = db.Exec(q); e != nil {
				t.Fatal(e)
			}
		}
		for id, r := range legacy.Posts {
			raw, _ := json.Marshal(r)
			if _, e = db.Exec("INSERT INTO posts VALUES(?,?)", id, string(raw)); e != nil {
				t.Fatal(e)
			}
		}
		for id, m := range legacy.Media {
			binary, e := base64.StdEncoding.DecodeString(m.Data)
			if e != nil {
				t.Fatal(e)
			}
			m.Data = ""
			raw, _ := json.Marshal(m)
			if _, e = db.Exec("INSERT INTO media VALUES(?,?,?)", id, string(raw), binary); e != nil {
				t.Fatal(e)
			}
		}
		legacy.Posts = nil
		legacy.Media = nil
	}
	raw, _ := json.Marshal(legacy)
	if _, e = db.Exec("INSERT INTO state VALUES(1,?)", string(raw)); e != nil {
		t.Fatal(e)
	}
}

func TestOpenUpgradesBothLegacyStorageLayoutsWithoutContentLoss(t *testing.T) {
	legacy, _, postID := legacyFixture(t)
	for _, split := range []bool{false, true} {
		name := "snapshot-row"
		if split {
			name = "split-post-media-rows"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeLegacyDatabase(t, dir, legacy, split)
			s, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			if s.State.Schema != SchemaVersion {
				t.Fatalf("schema not upgraded: %d", s.State.Schema)
			}
			if !reflect.DeepEqual(s.State.Posts, legacy.Posts) || !reflect.DeepEqual(s.State.Media, legacy.Media) || s.State.Settings != legacy.Settings {
				t.Fatal("schema upgrade altered authored content, history, settings or assets")
			}
			id := s.InstanceID()
			if id == "" {
				t.Fatal("upgraded instance lacks local identity")
			}
			_, public := s.Public()
			if len(public) != 1 || public[0].ID != postID || public[0].Slug != "legacy-published" || strings.Contains(public[0].Markdown, "PRIVATE_SECRET") {
				t.Fatal("schema migration changed private/public boundary")
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			reopened, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			if reopened.InstanceID() != id {
				t.Fatal("local instance identity changed on reopen")
			}
			if !reflect.DeepEqual(reopened.State.Posts, legacy.Posts) || !reflect.DeepEqual(reopened.State.Media, legacy.Media) {
				t.Fatal("migrated content lost on reopen")
			}
			migrations := 0
			for _, event := range reopened.State.Audit {
				if event.Operation == "system.migrate" {
					migrations++
				}
			}
			if migrations != 1 {
				t.Fatalf("migration should be applied once, got %d audit records", migrations)
			}
		})
	}
}

func TestLegacyLogicalBackupRestoreReportsUpgradeAndKeepsTargetIdentity(t *testing.T) {
	legacy, backup, postID := legacyFixture(t)
	dst := testStore(t)
	beforeID := dst.InstanceID()
	result := call(t, dst, "backup.restore", restoreArgs(backup))
	raw, _ := json.Marshal(result)
	var meta struct {
		Restored bool `json:"restored"`
		Source   int  `json:"source_schema"`
		Schema   int  `json:"schema"`
		Upgraded bool `json:"schema_upgraded"`
	}
	json.Unmarshal(raw, &meta)
	if !meta.Restored || meta.Source != 1 || meta.Schema != 2 || !meta.Upgraded {
		t.Fatalf("legacy restore metadata wrong: %s", raw)
	}
	if dst.InstanceID() != beforeID || beforeID == "" {
		t.Fatal("logical restore replaced local identity")
	}
	if dst.State.Schema != 2 || !reflect.DeepEqual(dst.State.Posts, legacy.Posts) || !reflect.DeepEqual(dst.State.Media, legacy.Media) {
		t.Fatal("legacy restore lost content or upgrade")
	}
	if draft := getPost(t, call(t, dst, "posts.get", map[string]any{"id": postID})); draft.Revision != 2 || draft.Markdown != "PRIVATE_SECRET_137 body" {
		t.Fatal("legacy private revision lost")
	}
	current := exported(t, dst)
	if current.Version != 2 {
		t.Fatalf("export did not advance backup format: %d", current.Version)
	}
	var state State
	json.Unmarshal(current.State, &state)
	if state.Schema != 2 || strings.Contains(string(current.State), beforeID) {
		t.Fatal("backup schema wrong or local identity leaked into portable content")
	}
	other := testStore(t)
	otherID := other.InstanceID()
	if otherID == beforeID || otherID == "" {
		t.Fatal("separate instances reused identity")
	}
	v := call(t, other, "backup.restore", restoreArgs(current))
	encoded, _ := json.Marshal(v)
	json.Unmarshal(encoded, &meta)
	if meta.Source != 2 || meta.Schema != 2 || meta.Upgraded || other.InstanceID() != otherID {
		t.Fatalf("current restore metadata or identity wrong: %s", encoded)
	}
}

func TestBackupVersionAndSchemaMustAgree(t *testing.T) {
	_, v1, _ := legacyFixture(t)
	src := testStore(t)
	create(t, src, "v2-source")
	v2 := exported(t, src)
	for _, tt := range []struct {
		name   string
		backup Backup
	}{{"v1 envelope v2 state", func() Backup { b := v2; b.Version = 1; return b }()}, {"v2 envelope v1 state", func() Backup { b := v1; b.Version = 2; return b }()}, {"future envelope", func() Backup { b := v2; b.Version = 3; return b }()}, {"future state", mutatedBackup(t, v2, func(st *State) { st.Schema = 3 })}} {
		t.Run(tt.name, func(t *testing.T) {
			dst := testStore(t)
			callError(t, dst, "backup.restore", restoreArgs(tt.backup), "validation")
			if len(dst.State.Posts) != 0 || dst.State.Schema != 2 {
				t.Fatal("invalid backup partially modified instance")
			}
		})
	}
}

func TestLegacySchemaCannotHideWorkflowRecords(t *testing.T) {
	src := testStore(t)
	p := create(t, src, "workflow-source")
	call(t, src, "proposals.create", map[string]any{"candidate": map[string]any{"title": "Proposed", "slug": "workflow-proposal", "markdown": "Private candidate"}})
	call(t, src, "schedules.create", map[string]any{"post_id": p.ID, "expected_revision": p.Revision, "publish_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "confirm": true})
	valid := exported(t, src)
	for _, kind := range []string{"proposal", "schedule", "both"} {
		t.Run(kind, func(t *testing.T) {
			bad := mutatedBackup(t, valid, func(st *State) {
				st.Schema = 1
				if kind == "proposal" {
					st.Schedules = nil
				}
				if kind == "schedule" {
					st.Proposals = nil
				}
			})
			bad.Version = 1
			dst := testStore(t)
			callError(t, dst, "backup.restore", restoreArgs(bad), "validation")
			if len(dst.State.Proposals) != 0 || len(dst.State.Schedules) != 0 || len(dst.State.Posts) != 0 {
				t.Fatal("legacy schema smuggled workflow content")
			}
		})
	}
}
