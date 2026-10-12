package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func openDatabase(path string, clock Clock) (db *sql.DB, err error) {
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	existing := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return nil, statErr
	}
	db, err = sql.Open("sqlite3", path+"?_busy_timeout=5000&_txlock=immediate&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			db.Close()
			db = nil
		}
	}()
	if err = migrateLifecycleV3(db, path, existing); err != nil {
		return db, err
	}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA synchronous=NORMAL"} {
		if _, err = db.Exec(q); err != nil {
			return db, err
		}
	}
	_, err = NewSQLiteStoreWithClock(db, clock).Maintain(clockOrReal(clock).Now(), nil)
	return db, err
}

// Each migration attempt creates and opens its own snapshot; an older backup is
// never trusted as proof that the current database has been backed up.
func verifiedBackup(db *sql.DB, path string) (string, error) {
	target := path
	if _, e := os.Stat(target); e == nil {
		f, e := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+"-*")
		if e != nil {
			return "", e
		}
		target = f.Name()
		if e = f.Close(); e != nil {
			return "", e
		}
		if e = os.Remove(target); e != nil {
			return "", e
		}
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if _, e := db.Exec("VACUUM INTO ?", target); e != nil {
		return "", fmt.Errorf("backup failed; refusing migration: %w", e)
	}
	b, e := sql.Open("sqlite3", target+"?mode=ro")
	if e != nil {
		return "", e
	}
	defer b.Close()
	var check string
	if e = b.QueryRow("PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
		return "", fmt.Errorf("backup verification failed: %s: %v", check, e)
	}
	var version int
	if e = b.QueryRow("PRAGMA user_version").Scan(&version); e != nil {
		return "", e
	}
	return target, nil
}
func migrateLifecycleV3(db *sql.DB, path string, existing bool) error {
	var version int
	if e := db.QueryRow("PRAGMA user_version").Scan(&version); e != nil {
		return e
	}
	if version > 3 {
		return fmt.Errorf("database schema version %d is newer than this Quicknote", version)
	}
	if version == 3 {
		return nil
	}
	var check string
	if e := db.QueryRow("PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
		return fmt.Errorf("database integrity: %s: %v", check, e)
	}
	if existing {
		suffix := ".pre-v3.bak"
		if version < 2 {
			suffix = ".pre-v2.bak"
		}
		if _, e := verifiedBackup(db, path+suffix); e != nil {
			return e
		}
	}
	tx, e := db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("CREATE TABLE IF NOT EXISTS notes(id INTEGER PRIMARY KEY AUTOINCREMENT,content TEXT)"); e != nil {
		return e
	}
	rows, e := tx.Query("PRAGMA table_info(notes)")
	if e != nil {
		return e
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, nn, pk int
		var name, kind string
		var def any
		if e = rows.Scan(&cid, &name, &kind, &nn, &def, &pk); e != nil {
			rows.Close()
			return e
		}
		columns[name] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	fields := [][2]string{{"kill_hash", "TEXT DEFAULT ''"}, {"kill_start", "TEXT DEFAULT ''"}, {"created_at", "TEXT"}, {"updated_at", "TEXT"}, {"deleted_at", "TEXT"}, {"revision", "INTEGER NOT NULL DEFAULT 1"}, {"expires_at", "TEXT"}, {"is_pinned", "INTEGER NOT NULL DEFAULT 0"}, {"expiration_source", "TEXT NOT NULL DEFAULT 'none'"}, {"expiration_needs_review", "INTEGER NOT NULL DEFAULT 0"}, {"delete_reason", "TEXT NOT NULL DEFAULT ''"}}
	for _, f := range fields {
		if !columns[f[0]] {
			if _, e = tx.Exec("ALTER TABLE notes ADD COLUMN " + f[0] + " " + f[1]); e != nil {
				return e
			}
		}
	}
	if _, e = tx.Exec("CREATE TABLE IF NOT EXISTS tags(note_id INTEGER REFERENCES notes(id) ON DELETE CASCADE,tag TEXT)"); e != nil {
		return e
	}
	// Legacy tags tables may have no FK. Preserve anomalous data and fail safely,
	// rather than dropping orphans to make foreign_key_check appear clean.
	var orphans int
	if e = tx.QueryRow("SELECT count(*) FROM tags WHERE note_id IS NULL OR NOT EXISTS(SELECT 1 FROM notes WHERE id=tags.note_id)").Scan(&orphans); e != nil {
		return e
	}
	if orphans > 0 {
		return fmt.Errorf("legacy tags contain %d orphan rows; preserve backup and repair before migration", orphans)
	}
	if _, e = tx.Exec("CREATE TABLE tags_lifecycle_v3(note_id INTEGER REFERENCES notes(id) ON DELETE CASCADE,tag TEXT)"); e != nil {
		return e
	}
	for _, q := range []string{"INSERT INTO tags_lifecycle_v3 SELECT note_id,tag FROM tags", "DROP TABLE tags", "ALTER TABLE tags_lifecycle_v3 RENAME TO tags", "CREATE INDEX tags_note_idx ON tags(note_id)", "CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(content)", "DROP TRIGGER IF EXISTS notes_fts_insert", "DROP TRIGGER IF EXISTS notes_fts_update", "DROP TRIGGER IF EXISTS notes_fts_delete", "CREATE TRIGGER notes_fts_insert AFTER INSERT ON notes BEGIN INSERT INTO notes_fts(rowid,content) VALUES(NEW.id,NEW.content); END", "CREATE TRIGGER notes_fts_update AFTER UPDATE ON notes BEGIN DELETE FROM notes_fts WHERE rowid=OLD.id; INSERT INTO notes_fts(rowid,content) VALUES(NEW.id,NEW.content); END", "CREATE TRIGGER notes_fts_delete AFTER DELETE ON notes BEGIN DELETE FROM notes_fts WHERE rowid=OLD.id; END", "DELETE FROM notes_fts", "INSERT INTO notes_fts(rowid,content) SELECT id,content FROM notes", "CREATE INDEX IF NOT EXISTS notes_expiry_idx ON notes(expires_at) WHERE deleted_at IS NULL AND is_pinned=0", "CREATE INDEX IF NOT EXISTS notes_deleted_at_idx ON notes(deleted_at)"} {
		if _, e = tx.Exec(q); e != nil {
			return e
		}
	}
	rows, e = tx.Query("SELECT id,COALESCE(content,''),COALESCE(kill_hash,''),COALESCE(kill_start,''),expires_at,is_pinned,CAST(deleted_at AS TEXT) FROM notes")
	if e != nil {
		return e
	}
	type legacy struct {
		id                   int
		content, hash, start string
		expiry, deleted      sql.NullString
		pinned               bool
	}
	var notes []legacy
	for rows.Next() {
		var n legacy
		if e = rows.Scan(&n.id, &n.content, &n.hash, &n.start, &n.expiry, &n.pinned, &n.deleted); e != nil {
			rows.Close()
			return e
		}
		notes = append(notes, n)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, n := range notes {
		d, parseErr := ParseKillDirective(n.content)
		review := parseErr != nil
		source := ExpirationNone
		expiry := n.expiry
		if expiry.Valid {
			t, err := ParseLifecycleTime(expiry.String)
			if err != nil {
				review = true
			} else {
				expiry.String = FormatLifecycleTime(t)
				source = ExpirationPreset
			}
		}
		if !expiry.Valid && d.Present && !n.pinned && !n.deleted.Valid {
			hash, legacyDuration := getKillTimerInfo(n.content)
			start, err := ParseLifecycleTime(n.start)
			if err != nil || hash != n.hash || hash == "" || legacyDuration == nil || *legacyDuration != d.Duration {
				review = true
			} else {
				deadline := start.Add(d.Duration)
				if deadline.Year() > 9999 || deadline.Year() < 1 {
					review = true
				} else {
					expiry = sql.NullString{String: FormatLifecycleTime(deadline), Valid: true}
					source = ExpirationDirective
				}
			}
		}
		if n.pinned {
			expiry = sql.NullString{}
			source = ExpirationNone
		}
		if _, e = tx.Exec("UPDATE notes SET expires_at=?,expiration_source=?,expiration_needs_review=?,kill_hash=? WHERE id=?", expiry, source, review, d.Fingerprint, n.id); e != nil {
			return e
		}
	}
	rows, e = tx.Query("PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	bad := rows.Next()
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if bad {
		return fmt.Errorf("foreign key check failed; migration rolled back")
	}
	if _, e = tx.Exec("PRAGMA user_version=3"); e != nil {
		return e
	}
	return tx.Commit()
}

// Keep historical timestamp bytes untouched in SQLite; these helpers parse only.
func rawLifecycleTime(v any) (time.Time, string) {
	if v == nil {
		return time.Time{}, ""
	}
	raw := fmt.Sprint(v)
	if b, ok := v.([]byte); ok {
		raw = string(b)
	}
	if t, ok := v.(time.Time); ok {
		return t, t.Format(time.RFC3339Nano)
	}
	t, _ := ParseLifecycleTime(strings.TrimSpace(raw))
	return t, raw
}
