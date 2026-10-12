package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Note struct {
	SessionID             uint64 // In-memory identity only; never stored in SQLite.
	ID                    int
	Content               string
	UpdatedAt             string
	UpdatedTime           time.Time
	CreatedAt             string
	CreatedTime           time.Time
	DeletedAt             string
	DeletedTime           time.Time
	IsGhost               bool
	KillHash              string
	KillStart             time.Time
	Tags                  []string
	Preview               string
	MatchRow              int
	MatchCol              int
	IsDuplicate           bool
	Revision              int64
	ExpiresAt             sql.NullString
	IsPinned              bool
	ExpirationSource      ExpirationSource
	ExpirationNeedsReview bool
	DeleteReason          DeleteReason
}

func initDB() *sql.DB {
	path := filepath.Join(os.Getenv("HOME"), ".local", "share", "quicknote-tui", "notes.sqlite")
	db, err := openDatabase(path, RealClock{})
	if err != nil {
		log.Fatal(err)
	}
	return db
}

func parseSQLiteTime(val interface{}) (time.Time, string) {
	if val == nil {
		return time.Time{}, ""
	}

	if t, ok := val.(time.Time); ok {
		return t.Local(), t.Local().Format("02 Jan 2006, 3:04 PM")
	}

	strVal := ""
	if b, ok := val.([]byte); ok {
		strVal = string(b)
	} else if s, ok := val.(string); ok {
		strVal = s
	}

	if strVal != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", strVal); err == nil {
			return t.Local(), t.Local().Format("02 Jan 2006, 3:04 PM")
		}
		if t, err := time.Parse(time.RFC3339, strVal); err == nil {
			return t.Local(), t.Local().Format("02 Jan 2006, 3:04 PM")
		}
	}
	return time.Time{}, ""
}

func loadNoteContent(db *sql.DB, id int) string {
	if id <= 0 {
		return ""
	}
	var content string
	err := db.QueryRow("SELECT content FROM notes WHERE id = ?", id).Scan(&content)
	if err != nil {
		return ""
	}
	return content
}

// Blazing fast Full-Text Search via SQLite with exact-line matching
func searchNotes(db *sql.DB, query string, inTrash bool) []Note {
	notes, _ := searchNotesChecked(db, query, inTrash)
	return notes
}

func searchNotesChecked(db *sql.DB, query string, inTrash bool) ([]Note, error) {
	if strings.TrimSpace(query) == "" {
		notes, err := loadNotesChecked(db, inTrash)
		if err != nil {
			return nil, err
		}
		for i := range notes {
			cleanContent := strings.TrimSpace(strings.ReplaceAll(notes[i].Content, "\n", " "))
			if len(cleanContent) > 55 {
				notes[i].Preview = cleanContent[:52] + "..."
			} else {
				notes[i].Preview = cleanContent
			}
		}
		return notes, nil
	}

	safeQuery := strings.ReplaceAll(query, "\"", "\"\"")
	matchQuery := safeQuery + "*"

	var sqlQuery string
	if inTrash {
		sqlQuery = `
			SELECT n.id, n.content, n.updated_at, n.revision, n.expires_at, n.is_pinned,n.expiration_source,n.expiration_needs_review,n.delete_reason, highlight(notes_fts, 0, '<<M_START>>', '<<M_END>>')
			FROM notes_fts f
			JOIN notes n ON f.rowid = n.id
			WHERE notes_fts MATCH ? AND n.deleted_at IS NOT NULL
			ORDER BY rank LIMIT 20
		`
	} else {
		sqlQuery = `
			SELECT n.id, n.content, n.updated_at, n.revision, n.expires_at, n.is_pinned,n.expiration_source,n.expiration_needs_review,n.delete_reason, highlight(notes_fts, 0, '<<M_START>>', '<<M_END>>')
			FROM notes_fts f
			JOIN notes n ON f.rowid = n.id
			WHERE notes_fts MATCH ? AND n.deleted_at IS NULL
			ORDER BY rank LIMIT 20
		`
	}

	rows, err := db.Query(sqlQuery, matchQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Note
	for rows.Next() {
		var n Note
		var content, highlighted string
		var updatedAt sql.NullString

		if err := rows.Scan(&n.ID, &content, &updatedAt, &n.Revision, &n.ExpiresAt, &n.IsPinned, &n.ExpirationSource, &n.ExpirationNeedsReview, &n.DeleteReason, &highlighted); err != nil {
			return nil, err
		}
		n.Content = content

		lines := strings.Split(highlighted, "\n")
		matchedLinesCount := 0

		for rowIdx, line := range lines {
			startIdx := strings.Index(line, "<<M_START>>")
			if startIdx != -1 {
				// Calculate exact column index (accounting for multi-byte runes)
				cleanBefore := strings.ReplaceAll(line[:startIdx], "<<M_START>>", "")
				cleanBefore = strings.ReplaceAll(cleanBefore, "<<M_END>>", "")

				matchNote := n
				matchNote.MatchRow = rowIdx
				matchNote.MatchCol = len([]rune(cleanBefore))

				// Smart truncation: ensure the matched word is visible in the preview
				cleanLine := strings.TrimSpace(line)
				tIdx := strings.Index(cleanLine, "<<M_START>>")
				if tIdx > 25 {
					cleanLine = "..." + cleanLine[tIdx-20:]
				}
				if len(cleanLine) > 75 {
					cleanLine = cleanLine[:72] + "..."
				}

				matchNote.Preview = cleanLine
				results = append(results, matchNote)
				matchedLinesCount++
			}
		}

		if matchedLinesCount == 0 {
			cleanContent := strings.ReplaceAll(content, "\n", " ")
			if len(cleanContent) > 55 {
				n.Preview = cleanContent[:52] + "..."
			} else {
				n.Preview = cleanContent
			}
			results = append(results, n)
		}
	}

	return results, rows.Err()
}

func loadNotes(db *sql.DB, inTrash bool) []Note {
	notes, _ := loadNotesChecked(db, inTrash)
	return notes
}

func loadNotesChecked(db *sql.DB, inTrash bool) ([]Note, error) {
	var query string
	if inTrash {
		query = `SELECT n.id, SUBSTR(n.content, 1, 200), n.updated_at, n.created_at, n.deleted_at, n.kill_hash, n.kill_start, n.revision, n.expires_at, n.is_pinned,n.expiration_source,n.expiration_needs_review,n.delete_reason, GROUP_CONCAT(t.tag)
				 FROM notes n LEFT JOIN tags t ON n.id = t.note_id
				 WHERE n.deleted_at IS NOT NULL GROUP BY n.id ORDER BY n.deleted_at DESC`
	} else {
		query = `SELECT n.id, SUBSTR(n.content, 1, 200), n.updated_at, n.created_at, n.deleted_at, n.kill_hash, n.kill_start, n.revision, n.expires_at, n.is_pinned,n.expiration_source,n.expiration_needs_review,n.delete_reason, GROUP_CONCAT(t.tag)
				 FROM notes n LEFT JOIN tags t ON n.id = t.note_id
				 WHERE n.deleted_at IS NULL GROUP BY n.id ORDER BY n.updated_at DESC`
	}

	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var n Note
		var rawUpdated, rawCreated, rawDeleted interface{}
		var killHash, killStartStr, rawTags sql.NullString

		err := rows.Scan(&n.ID, &n.Preview, &rawUpdated, &rawCreated, &rawDeleted, &killHash, &killStartStr, &n.Revision, &n.ExpiresAt, &n.IsPinned, &n.ExpirationSource, &n.ExpirationNeedsReview, &n.DeleteReason, &rawTags)
		if err != nil {
			return nil, err
		}
		n.UpdatedTime, n.UpdatedAt = parseSQLiteTime(rawUpdated)
		n.CreatedTime, n.CreatedAt = parseSQLiteTime(rawCreated)

		if n.CreatedAt == "" {
			n.CreatedTime = n.UpdatedTime
			n.CreatedAt = n.UpdatedAt
		}

		n.DeletedTime, n.DeletedAt = rawLifecycleTime(rawDeleted)

		if killHash.Valid {
			n.KillHash = killHash.String
		}
		if killStartStr.Valid && killStartStr.String != "" {
			if t, err := time.Parse(time.RFC3339, killStartStr.String); err == nil {
				n.KillStart = t
			}
		}

		if rawTags.Valid && rawTags.String != "" {
			n.Tags = strings.Split(rawTags.String, ",")
		} else {
			n.Tags = []string{}
		}
		notes = append(notes, n)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(notes) == 0 {
		if inTrash {
			notes = append(notes, Note{ID: -1, Content: "--- Trash is Empty ---", UpdatedAt: ""})
		} else {
			notes = append(notes, Note{ID: 0, Content: "", UpdatedTime: time.Now(), CreatedTime: time.Now()})
		}
	}
	return notes, nil
}

// saveNoteChecked atomically persists content and tags. On failure the
// caller keeps its original note identity and its unsaved editor buffer.
func saveNoteChecked(db *sql.DB, n Note) (Note, error) { return saveNoteSnapshot(db, n, false) }

func saveNoteSnapshot(db *sql.DB, n Note, allowEmpty bool) (Note, error) {
	return saveNoteSnapshotAt(db, n, allowEmpty, time.Now())
}
func saveNoteSnapshotAt(db *sql.DB, n Note, allowEmpty bool, now time.Time) (Note, error) {
	original := n
	if n.ID == -1 || (n.ID == 0 && n.Content == "" && !allowEmpty) || n.IsGhost {
		return n, nil
	}
	// 1. THE FIX: Protect database tag parsing from code blocks and comments!
	reTags := regexp.MustCompile(`(?i)#[a-zA-Z0-9_-]+`)
	tagMap := make(map[string]bool)
	n.Tags = []string{}

	inCodeBlock := false
	lines := strings.Split(n.Content, "\n")
	for _, line := range lines {
		// Flip state when encountering a codeblock delimiter
		if strings.Count(line, "```")%2 != 0 {
			inCodeBlock = !inCodeBlock
		}
		// Skip extracting tags if inside a code block or line comment
		if inCodeBlock || strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		matches := reTags.FindAllStringIndex(line, -1)
		for _, m := range matches {
			// Skip tags trapped in inline backticks
			if strings.Count(line[:m[0]], "`")%2 != 0 {
				continue
			}

			tLower := strings.ToLower(line[m[0]:m[1]])
			if tLower == "#kill" || tLower == "#idea" || tLower == "#todo" {
				continue
			}
			if !tagMap[tLower] {
				tagMap[tLower] = true
				n.Tags = append(n.Tags, tLower)
			}
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return original, fmt.Errorf("begin save: %w", err)
	}
	defer tx.Rollback()
	base := Note{IsPinned: n.IsPinned}
	if n.ID > 0 {
		base, err = getNoteSnapshot(tx, n.ID)
		if err != nil {
			return original, err
		}
		if base.DeletedAt != "" {
			return original, ErrTrashed
		}
		if base.Revision != n.Revision {
			return original, &ConflictError{ID: n.ID, Expected: n.Revision, Actual: base.Revision}
		}
	}
	derived, _ := DeriveExpiration(base, n.Content, now) // Parse issues are persisted as review, never save failures.
	n.KillHash, n.KillStart, n.ExpiresAt, n.IsPinned = derived.KillHash, derived.KillStart, derived.ExpiresAt, derived.IsPinned
	n.ExpirationSource, n.ExpirationNeedsReview, n.DeleteReason = derived.ExpirationSource, derived.ExpirationNeedsReview, derived.DeleteReason
	killStartStr := ""
	if !n.KillStart.IsZero() {
		killStartStr = FormatLifecycleTime(n.KillStart)
	}
	if n.ID == 0 {
		var res sql.Result
		res, err = tx.Exec("INSERT INTO notes(content, kill_hash, kill_start, revision, expires_at, is_pinned, expiration_source, expiration_needs_review,created_at,updated_at) VALUES(?,?,?,1,?,?,?,?,?,?)", n.Content, n.KillHash, killStartStr, n.ExpiresAt, n.IsPinned, n.ExpirationSource, n.ExpirationNeedsReview, FormatLifecycleTime(now), FormatLifecycleTime(now))
		if err != nil {
			return original, fmt.Errorf("insert note: %w", err)
		}
		var id int64
		id, err = res.LastInsertId()
		if err != nil {
			return original, fmt.Errorf("read new note ID: %w", err)
		}
		n.ID = int(id)
		n.Revision = 1
	} else {
		var res sql.Result
		res, err = tx.Exec("UPDATE notes SET content=?, updated_at=?, kill_hash=?, kill_start=?, expires_at=?, is_pinned=?,expiration_source=?,expiration_needs_review=?, revision=revision+1 WHERE id=? AND revision=? AND deleted_at IS NULL", n.Content, FormatLifecycleTime(now), n.KillHash, killStartStr, n.ExpiresAt, n.IsPinned, n.ExpirationSource, n.ExpirationNeedsReview, n.ID, n.Revision)
		if err != nil {
			return original, fmt.Errorf("update note %d: %w", n.ID, err)
		}
		affected, e := res.RowsAffected()
		if e != nil {
			return original, fmt.Errorf("count updated rows: %w", e)
		}
		if affected != 1 {
			return original, casMiss(tx, n.ID, n.Revision, true)
		}
		n.Revision++
	}
	if _, err = tx.Exec("DELETE FROM tags WHERE note_id=?", n.ID); err != nil {
		return original, fmt.Errorf("clear tags: %w", err)
	}
	for _, tag := range n.Tags {
		if _, err = tx.Exec("INSERT INTO tags(note_id,tag) VALUES(?,?)", n.ID, tag); err != nil {
			return original, fmt.Errorf("save tag %q: %w", tag, err)
		}
	}
	var duplicates int
	if err = tx.QueryRow("SELECT count(*) FROM notes WHERE content=? AND id<>? AND deleted_at IS NULL", n.Content, n.ID).Scan(&duplicates); err != nil {
		return original, fmt.Errorf("check duplicates: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return original, fmt.Errorf("commit note: %w", err)
	}
	n.UpdatedTime = now
	n.UpdatedAt = now.Format("02 Jan 2006, 3:04 PM")
	if n.CreatedTime.IsZero() {
		n.CreatedTime = now
		n.CreatedAt = n.UpdatedAt
	}
	n.IsDuplicate = duplicates > 0
	return n, nil
}

// Legacy call sites can use this until their Update paths are converted to
// explicit error handling. The checked API above is preferred.
func saveNote(db *sql.DB, n Note) Note {
	saved, err := saveNoteChecked(db, n)
	if err != nil {
		log.Printf("quicknote: note save failed (id %d): %v", n.ID, err)
		return n
	}
	return saved
}

// Legacy helpers retain signatures while sharing guarded storage operations.
func trashNote(db *sql.DB, id int) {
	if id > 0 {
		if err := NewSQLiteStore(db).Trash(id); err != nil {
			log.Printf("trash note %d: %v", id, err)
		}
	}
}
func hardDeleteNote(db *sql.DB, id int) { deleteNote(db, id) }
func restoreNote(db *sql.DB, id int) {
	if id > 0 {
		if err := NewSQLiteStore(db).Restore(id); err != nil {
			log.Printf("restore note %d: %v", id, err)
		}
	}
}
func deleteNote(db *sql.DB, id int) {
	if id > 0 {
		if err := NewSQLiteStore(db).Purge(id); err != nil {
			log.Printf("purge note %d: %v", id, err)
		}
	}
}
