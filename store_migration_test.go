package main

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
	"os"
	"path/filepath"
	"testing"
)

func TestQN010V1MigrationBackupAndIdempotency(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local", "share", "quicknote-tui")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "notes.sqlite")
	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE notes (id INTEGER PRIMARY KEY,content TEXT,kill_hash TEXT DEFAULT '',kill_start TEXT DEFAULT '',updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,created_at DATETIME DEFAULT CURRENT_TIMESTAMP,deleted_at DATETIME)",
		"CREATE TABLE tags(note_id INTEGER,tag TEXT)",
		"CREATE VIRTUAL TABLE notes_fts USING fts5(content)",
		"INSERT INTO notes(id,content) VALUES(17,'legacy note')",
		"INSERT INTO tags(note_id,tag) VALUES(17,'#legacy')",
		"PRAGMA user_version=1",
	} {
		if _, err = legacy.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db := initDB()
	var version int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 3 {
		t.Fatalf("schema version=%d", version)
	}
	if got := loadNoteContent(db, 17); got != "legacy note" {
		t.Fatalf("lost existing note: %q", got)
	}
	var revision int64
	var expires sql.NullString
	var pin int
	if err = db.QueryRow("SELECT revision,expires_at,is_pinned FROM notes WHERE id=17").Scan(&revision, &expires, &pin); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || expires.Valid || pin != 0 {
		t.Fatalf("unexpected defaults: %d %v %d", revision, expires, pin)
	}
	backup := path + ".pre-v2.bak"
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("missing pre-v2 backup: %v", err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := initDB()
	defer reopened.Close()
	if got := loadNoteContent(reopened, 17); got != "legacy note" {
		t.Fatalf("idempotent restart lost note: %q", got)
	}
	if _, err = reopened.Exec("INSERT INTO notes(content) VALUES('newly indexed')"); err != nil {
		t.Fatal(err)
	}
	var hits int
	if err = reopened.QueryRow("SELECT count(*) FROM notes_fts WHERE notes_fts MATCH 'newly'").Scan(&hits); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("FTS trigger not repaired: %d", hits)
	}
}

func TestQN009TypedStoreCRUD(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLiteStore(db)
	note, err := store.Save(Note{Content: "typed #store"})
	if err != nil {
		t.Fatal(err)
	}
	content, err := store.Get(note.ID)
	if err != nil || content != "typed #store" {
		t.Fatalf("Get %q: %v", content, err)
	}
	if err = store.Trash(note.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Restore(note.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.Purge(note.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(note.ID); err == nil {
		t.Fatal("purged note remains accessible")
	}
}
