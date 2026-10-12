package main

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQN011MigratedLegacyCASAndBackupRecovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local/share/quicknote-tui")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "notes.sqlite")
	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE notes(id INTEGER PRIMARY KEY,content TEXT,kill_hash TEXT DEFAULT '',kill_start TEXT DEFAULT '',updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,created_at DATETIME DEFAULT CURRENT_TIMESTAMP,deleted_at DATETIME)`,
		`CREATE TABLE tags(note_id INTEGER,tag TEXT)`,
		`CREATE VIRTUAL TABLE notes_fts USING fts5(content)`,
		`INSERT INTO notes(id,content) VALUES(17,'legacy #legacy'),(18,'recoverable trash')`,
		`UPDATE notes SET deleted_at=CURRENT_TIMESTAMP WHERE id=18`,
		`INSERT INTO tags(note_id,tag) VALUES(17,'#legacy')`,
		`PRAGMA user_version=1`,
	} {
		if _, err := legacy.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	legacy.Close()
	migrated := initDB()
	defer migrated.Close()
	a := NewSQLiteStore(migrated)
	n, err := a.GetNote(17)
	if err != nil {
		t.Fatal(err)
	}
	if n.Revision != 1 || n.Content != "legacy #legacy" {
		t.Fatalf("legacy snapshot changed: %+v", n)
	}
	newer := n
	newer.Content = "new #new"
	if _, err = a.Save(newer); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Save(n); !errors.Is(err, ErrConflict) {
		t.Fatalf("legacy snapshot bypassed CAS: %v", err)
	}
	trash, err := a.GetNote(18)
	if err != nil || trash.DeletedAt == "" || trash.Content != "recoverable trash" {
		t.Fatalf("legacy trash lost: %+v %v", trash, err)
	}
	backup, err := sql.Open("sqlite3", path+".pre-v2.bak")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	recoveredPath := filepath.Join(t.TempDir(), "recovered.sqlite")
	quoted := strings.ReplaceAll(recoveredPath, "'", "''")
	if _, err = backup.Exec("VACUUM INTO '" + quoted + "'"); err != nil {
		t.Fatal(err)
	}
	recovered, err := sql.Open("sqlite3", recoveredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	var content, integrity string
	var tags, version int
	if err = recovered.QueryRow("SELECT content FROM notes WHERE id=17").Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != "legacy #legacy" {
		t.Fatalf("backup lost original: %q", content)
	}
	if err = recovered.QueryRow("SELECT count(*) FROM tags WHERE note_id=17 AND tag='#legacy'").Scan(&tags); err != nil || tags != 1 {
		t.Fatalf("backup tags=%d err=%v", tags, err)
	}
	if err = recovered.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("backup version=%d err=%v", version, err)
	}
	if err = recovered.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("recovery integrity=%q err=%v", integrity, err)
	}
}
