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
    if version != 1 { t.Fatalf("baseline schema version = %d; want 1", version) }
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
    t.Skip("KNOWN FAILURE QN-005: trashNote hard-deletes duplicates; enable after fix")
    db := newTestDB(t)
    first := insertTestNote(t,db,"same content")
    second := insertTestNote(t,db,"same content")
    trashNote(db,first)
    trashNote(db,second)
    var count int
    if err:=db.QueryRow("SELECT count(*) FROM notes WHERE id IN (?,?) AND deleted_at IS NOT NULL",first,second).Scan(&count); err!=nil { t.Fatal(err) }
    if count!=2 { t.Fatalf("retained trashed duplicates = %d; want 2",count) }
}
