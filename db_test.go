package main

import (
    "database/sql"
    "path/filepath"
    "strings"
    "testing"
)

// newTestDB creates a real SQLite database, FTS index, and schema in an
// isolated temporary HOME. Never point a test at a developer's notes.sqlite.
// Do not call t.Parallel in tests that use t.Setenv.
func newTestDB(t *testing.T) *sql.DB {
    t.Helper()
    home := t.TempDir()
    t.Setenv("HOME", home)
    db := initDB()
    t.Cleanup(func() { _ = db.Close() })
    expected := filepath.Join(home, ".local", "share", "quicknote-tui", "notes.sqlite")
    var files []struct { seq int; name, file string }
    rows, err := db.Query("PRAGMA database_list")
    if err != nil { t.Fatalf("database_list: %v", err) }
    defer rows.Close()
    for rows.Next() {
        var item struct { seq int; name, file string }
        if err := rows.Scan(&item.seq, &item.name, &item.file); err != nil { t.Fatal(err) }
        files = append(files, item)
    }
    if err := rows.Err(); err != nil { t.Fatal(err) }
    found := false
    for _, item := range files {
        if item.name == "main" && item.file == expected { found = true }
    }
    if !found { t.Fatalf("test database was not isolated at %q: %#v", expected, files) }
    return db
}

func insertTestNote(t *testing.T, db *sql.DB, content string) int {
    t.Helper()
    res, err := db.Exec("INSERT INTO notes(content) VALUES(?)", content)
    if err != nil { t.Fatalf("insert synthetic note: %v", err) }
    id, err := res.LastInsertId()
    if err != nil { t.Fatal(err) }
    return int(id)
}

func TestSyntheticDatabaseIsolationAndFTS(t *testing.T) {
    db := newTestDB(t)
    var version int
    if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil { t.Fatal(err) }
    if version != 2 { t.Fatalf("schema version = %d; want 2", version) }
    id := insertTestNote(t, db, "uniquebaselinekeyword")
    var indexed int
    if err := db.QueryRow("SELECT COUNT(*) FROM notes_fts WHERE notes_fts MATCH 'uniquebaselinekeyword'").Scan(&indexed); err != nil { t.Fatal(err) }
    if indexed != 1 { t.Fatalf("FTS index count = %d; want 1 (id %d)", indexed, id) }
    if got := loadNoteContent(db, id); got != "uniquebaselinekeyword" { t.Fatalf("loaded %q", got) }
}

func TestSyntheticNoteSaveAndOrdinaryTrash(t *testing.T) {
    db := newTestDB(t)
    note := saveNote(db, Note{Content:"Scratch #baseline"})
    if note.ID <= 0 { t.Fatalf("save did not assign note ID: %#v", note) }
    if !strings.Contains(loadNoteContent(db,note.ID),"#baseline") { t.Fatal("saved content missing") }
    trashNote(db,note.ID)
    var deletedAt sql.NullString
    if err := db.QueryRow("SELECT deleted_at FROM notes WHERE id = ?", note.ID).Scan(&deletedAt); err != nil { t.Fatal(err) }
    if !deletedAt.Valid || deletedAt.String == "" { t.Fatal("expected soft-deleted note") }
    restoreNote(db,note.ID)
    if err := db.QueryRow("SELECT deleted_at FROM notes WHERE id = ?", note.ID).Scan(&deletedAt); err != nil { t.Fatal(err) }
    if deletedAt.Valid { t.Fatalf("note not restored: %#v", deletedAt) }
}

// QN-005 / T-05: activate this known-failing regression after fixing
// duplicate trash handling. A failing assertion is kept, not weakened.
func TestRegressionDuplicateTrashPreservesBothNotes(t *testing.T) {
    db := newTestDB(t)
    first := insertTestNote(t,db,"same content")
    second := insertTestNote(t,db,"same content")
    trashNote(db,first)
    trashNote(db,second)
    var count int
    if err:=db.QueryRow("SELECT count(*) FROM notes WHERE id IN (?,?) AND deleted_at IS NOT NULL",first,second).Scan(&count); err!=nil { t.Fatal(err) }
    if count!=2 { t.Fatalf("retained trashed duplicates = %d; want 2",count) }
}

func TestQN005DuplicateTrashSurvivesDatabaseRestart(t *testing.T) {
 db:=newTestDB(t)
 first:=insertTestNote(t,db,"duplicate")
 second:=insertTestNote(t,db,"duplicate")
 trashNote(db,first)
 trashNote(db,second)
 // Startup cleanup must not deduplicate unexpired trashed content.
 // Call the same database initializer using the temporary HOME from newTestDB.
 restarted:=initDB()
 defer restarted.Close()
 var count int
 if err:=restarted.QueryRow("SELECT count(*) FROM notes WHERE id IN (?,?) AND deleted_at IS NOT NULL",first,second).Scan(&count);err!=nil {t.Fatal(err)}
 if count!=2 {t.Fatalf("restart retained %d of 2 duplicated notes",count)}
 restoreNote(restarted,first)
 if got:=loadNoteContent(restarted,first);got!="duplicate" {t.Fatalf("restore failed: %q",got)}
}
func TestQN005OnlyExpiredTrashPurged(t *testing.T) {
 db:=newTestDB(t)
 fresh:=insertTestNote(t,db,"fresh")
 old:=insertTestNote(t,db,"old")
 trashNote(db,fresh)
 trashNote(db,old)
 if _,err:=db.Exec("UPDATE notes SET deleted_at = datetime('now','-4 days') WHERE id = ?",old);err!=nil{t.Fatal(err)}
 reopened:=initDB()
 defer reopened.Close()
 var count int
 if err:=reopened.QueryRow("SELECT count(*) FROM notes WHERE id IN (?,?)",fresh,old).Scan(&count);err!=nil {t.Fatal(err)}
 if count!=1 {t.Fatalf("expected only fresh trash after maintenance, got %d",count)}
}
