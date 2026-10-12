package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func legacyLifecycleDB(t *testing.T, version int) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.sqlite")
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{`CREATE TABLE notes(id INTEGER PRIMARY KEY,content TEXT,kill_hash TEXT DEFAULT '',kill_start TEXT DEFAULT '',created_at TEXT,updated_at TEXT,deleted_at TEXT,revision INTEGER NOT NULL DEFAULT 7,expires_at TEXT,is_pinned INTEGER NOT NULL DEFAULT 0)`, `CREATE TABLE tags(note_id INTEGER,tag TEXT)`} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = db.Exec("PRAGMA user_version=" + string(rune('0'+version))); e != nil {
		t.Fatal(e)
	}
	return db, path
}
func TestQN028V3MigrationBackup(t *testing.T) {
	for _, version := range []int{0, 1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			db, path := legacyLifecycleDB(t, version)
			stamp := "2026-10-12 01:02:03"
			for _, id := range []int{17, 18} {
				if _, e := db.Exec("INSERT INTO notes(id,content,created_at,updated_at)VALUES(?,?,?,?)", id, "same #tag", stamp, stamp); e != nil {
					t.Fatal(e)
				}
			}
			db.Exec("INSERT INTO tags VALUES(17,'#tag'),(17,'#tag')")
			suffix := ".pre-v3.bak"
			if version < 2 {
				suffix = ".pre-v2.bak"
			}
			if e := os.WriteFile(path+suffix, []byte("existing backup"), 0600); e != nil {
				t.Fatal(e)
			}
			if e := migrateLifecycleV3(db, path, true); e != nil {
				t.Fatal(e)
			}
			old, _ := os.ReadFile(path + suffix)
			if string(old) != "existing backup" {
				t.Fatal("overwrote backup")
			}
			matches, _ := filepath.Glob(path + suffix + "-*")
			if len(matches) != 1 {
				t.Fatal(matches)
			}
			backup, e := sql.Open("sqlite3", matches[0])
			if e != nil {
				t.Fatal(e)
			}
			defer backup.Close()
			var v, count int
			if e = backup.QueryRow("PRAGMA user_version").Scan(&v); e != nil || v != version {
				t.Fatal(v, e)
			}
			recovery := filepath.Join(t.TempDir(), "recovered.sqlite")
			if _, e = backup.Exec("VACUUM INTO ?", recovery); e != nil {
				t.Fatal(e)
			}
			recovered, e := sql.Open("sqlite3", recovery)
			if e != nil {
				t.Fatal(e)
			}
			defer recovered.Close()
			for _, conn := range []*sql.DB{db, recovered} {
				if e = conn.QueryRow("SELECT count(*) FROM notes WHERE content='same #tag' AND created_at=? AND updated_at=? AND revision=7", stamp, stamp).Scan(&count); e != nil || count != 2 {
					t.Fatal(count, e)
				}
				if e = conn.QueryRow("SELECT count(*) FROM tags").Scan(&count); e != nil || count != 2 {
					t.Fatal(count, e)
				}
				var check string
				if e = conn.QueryRow("PRAGMA integrity_check").Scan(&check); e != nil || check != "ok" {
					t.Fatal(check, e)
				}
			}
			if e = migrateLifecycleV3(db, path, true); e != nil {
				t.Fatal(e)
			}
			if e = db.QueryRow("PRAGMA user_version").Scan(&v); e != nil || v != 3 {
				t.Fatal(v, e)
			}
			if e = db.QueryRow("SELECT count(*) FROM notes_fts").Scan(&count); e != nil || count != 2 {
				t.Fatal(count, e)
			}
		})
	}
}
func TestQN028MigrationRollback(t *testing.T) {
	for _, failure := range []string{"backup", "ddl", "orphan"} {
		t.Run(failure, func(t *testing.T) {
			db, path := legacyLifecycleDB(t, 2)
			db.Exec("INSERT INTO notes(id,content)VALUES(1,'preserve')")
			switch failure {
			case "backup":
				path = filepath.Join(path, "impossible")
			case "ddl":
				db.Exec("CREATE TABLE tags_lifecycle_v3(id INTEGER)")
			case "orphan":
				db.Exec("INSERT INTO tags VALUES(999,'#orphan')")
			}
			if e := migrateLifecycleV3(db, path, true); e == nil {
				t.Fatal("expected migration failure")
			}
			var version int
			db.QueryRow("PRAGMA user_version").Scan(&version)
			if version != 2 {
				t.Fatal("partial advance", version)
			}
			var text string
			db.QueryRow("SELECT content FROM notes WHERE id=1").Scan(&text)
			if text != "preserve" {
				t.Fatal(text)
			}
			var count int
			db.QueryRow("SELECT count(*) FROM pragma_table_info('notes') WHERE name='expiration_source'").Scan(&count)
			if count != 0 {
				t.Fatal("partial DDL")
			}
		})
	}
}
func TestQN028LegacyDeadlineImport(t *testing.T) {
	db, path := legacyLifecycleDB(t, 2)
	now := lifecycleNow()
	content := "#kill 15 mins"
	hash, _ := getKillTimerInfo(content)
	rows := []struct {
		hash, start, expiry string
		pin                 bool
		deleted             string
		review              bool
		want                string
	}{{hash, now.Format(time.RFC3339), "", false, "", false, "2026-10-12T12:15:00.000Z"}, {"bad", now.Format(time.RFC3339), "", false, "", true, ""}, {hash, "nonsense", "", false, "", true, ""}, {hash, "bad", "2026-10-12T16:00:00.000001+02:00", false, "", false, "2026-10-12T14:00:00.001Z"}, {hash, "bad", "invalid-expiry", false, "", true, "invalid-expiry"}, {hash, now.Format(time.RFC3339), "", true, "", false, ""}, {hash, now.Format(time.RFC3339), "", false, "2026-10-12 11:00:00", false, ""}}
	for i, r := range rows {
		var expiry, deleted any
		if r.expiry != "" {
			expiry = r.expiry
		}
		if r.deleted != "" {
			deleted = r.deleted
		}
		if _, e := db.Exec("INSERT INTO notes(id,content,kill_hash,kill_start,expires_at,is_pinned,deleted_at)VALUES(?,?,?,?,?,?,?)", i+1, content, r.hash, r.start, expiry, r.pin, deleted); e != nil {
			t.Fatal(e)
		}
	}
	if e := migrateLifecycleV3(db, path, true); e != nil {
		t.Fatal(e)
	}
	s := NewSQLiteStore(db)
	for i, r := range rows {
		n, e := s.GetNote(i + 1)
		if e != nil || n.Revision != 7 || n.ExpiresAt.String != r.want || n.ExpirationNeedsReview != r.review {
			t.Fatalf("row %d %+v %v", i, n, e)
		}
		if n.DeletedAt == "" {
			saved, e := NewSQLiteStoreWithClock(db, fixedClock{now.Add(time.Hour)}).Save(n)
			if e != nil || saved.ExpiresAt != n.ExpiresAt {
				t.Fatal("rearmed imported observation", saved, e)
			}
		}
	}
}
func TestQN028T07RestartExpiresAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.sqlite")
	now := lifecycleNow()
	db, e := openDatabase(path, fixedClock{now})
	if e != nil {
		t.Fatal(e)
	}
	s := NewSQLiteStoreWithClock(db, fixedClock{now})
	for i := 0; i < 3; i++ {
		if _, e = s.Save(Note{Content: "#kill 1 minute"}); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	db, e = openDatabase(path, fixedClock{now.Add(time.Minute)})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var count int
	db.QueryRow("SELECT count(*) FROM notes WHERE deleted_at IS NOT NULL AND delete_reason='expired' AND revision=2").Scan(&count)
	if count != 3 {
		t.Fatal(count)
	}
	if _, e = NewSQLiteStore(db).Maintain(now.Add(2*time.Minute), nil); e != nil {
		t.Fatal(e)
	}
	db.QueryRow("SELECT count(*) FROM notes WHERE deleted_at=? AND revision=2", FormatLifecycleTime(now.Add(time.Minute))).Scan(&count)
	if count != 3 {
		t.Fatal("sweep reset deletion", count)
	}
}
func TestQN028PartialLegacyAndConnectionFK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "partial.sqlite")
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	db.Exec("CREATE TABLE notes(id INTEGER PRIMARY KEY,content TEXT)")
	db.Exec("INSERT INTO notes VALUES(41,'preserved')")
	db.Close()
	db, e = openDatabase(path, fixedClock{lifecycleNow()})
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	a, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	for _, c := range []*sql.Conn{a, b} {
		var fk int
		if e = c.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&fk); e != nil || fk != 1 {
			t.Fatal(fk, e)
		}
	}
	n, e := NewSQLiteStore(db).GetNote(41)
	if e != nil || n.Content != "preserved" {
		t.Fatal(n, e)
	}
}
