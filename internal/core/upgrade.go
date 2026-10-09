package core

import (
	"context"
	"encoding/json"
)

// SchemaVersion 2 adds durable proposal/schedule metadata. Older binaries reject
// it instead of silently dropping these fields when rewriting the catalog.
const SchemaVersion = 2

func (s *Store) upgradeStore(ctx context.Context) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = s.refresh(ctx, tx, false); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS local_instance(id INTEGER PRIMARY KEY CHECK(id=1), identity TEXT NOT NULL)"); e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO local_instance(id,identity) VALUES(1,?)", id()); e != nil {
		return e
	}
	var identity string
	if e = tx.QueryRowContext(ctx, "SELECT identity FROM local_instance WHERE id=1").Scan(&identity); e != nil {
		return e
	}
	if s.State.Schema == 1 {
		before := s.State
		s.State.Schema = SchemaVersion
		s.State.Revision++
		s.audit("system.migrate", "schema:2", "system", s.State.Revision)
		if e = s.persistRows(ctx, tx, before); e != nil {
			s.State = before
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, "INSERT OR IGNORE INTO migrations VALUES(3,datetime('now'))"); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	s.instanceID = identity
	return nil
}
func (s *Store) InstanceID() string { return s.instanceID }

// CanonicalBackupState accepts legacy v1 and current v2 without weakening the
// checksum. Callers validate the original schema before upgrading it in memory.
func canonicalBackupState(b Backup) (State, error) {
	var st State
	if e := decode(b.State, &st); e != nil {
		return st, Err("validation", "Invalid backup state")
	}
	raw, e := json.Marshal(st)
	if e != nil {
		return st, Err("validation", "Invalid backup state")
	}
	if b.Format != "folio-backup" || (b.Version != 1 && b.Version != SchemaVersion) || b.Version != st.Schema || digest(raw) != b.SHA256 {
		return st, Err("validation", "Invalid backup format or checksum")
	}
	return st, nil
}
