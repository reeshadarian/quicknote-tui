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
	ID          int
	Content     string
	UpdatedAt   string
	UpdatedTime time.Time
	CreatedAt   string
	CreatedTime time.Time
	DeletedAt   string
	DeletedTime time.Time
	IsGhost     bool
	KillHash    string
	KillStart   time.Time
	Tags        []string
	Preview     string
	MatchRow    int
	MatchCol    int
	IsDuplicate bool
	Revision int64
	ExpiresAt sql.NullString
	IsPinned bool
}

func initDB() *sql.DB {
	dataDir := filepath.Join(os.Getenv("HOME"), ".local", "share", "quicknote-tui")
	_ = os.MkdirAll(dataDir, 0755)

	dbPath := filepath.Join(dataDir, "notes.sqlite")
	_, existingErr := os.Stat(dbPath)
	wasExisting := existingErr == nil
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000")
	if err != nil {
		log.Fatal(err)
	}

	// 1. PERFORMANCE PRAGMAS
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL;")
	_, _ = db.Exec("PRAGMA mmap_size=3000000000;")

	// 2. Core Notes Table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		content TEXT
	)`)
	if err != nil {
		log.Fatal(err)
	}

	// 3. Schema Migrations (Only run if version is 0)
	var version int
	err = db.QueryRow("PRAGMA user_version").Scan(&version)
	if err == nil && version == 0 {
		_, _ = db.Exec("ALTER TABLE notes ADD COLUMN kill_hash TEXT DEFAULT ''")
		_, _ = db.Exec("ALTER TABLE notes ADD COLUMN kill_start TEXT DEFAULT ''")
		_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`)
		_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN created_at DATETIME DEFAULT CURRENT_TIMESTAMP`)
		_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN deleted_at DATETIME DEFAULT NULL`)
		_, _ = db.Exec("PRAGMA user_version = 1")
	}

	// 4. Tags Table
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS tags (
		note_id INTEGER,
		tag TEXT,
		FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
	)`)

	// 5. FTS5 Search Engine (Build ONLY if it doesn't exist)
	var ftsExists int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='notes_fts'").Scan(&ftsExists)
	if ftsExists == 0 {
		_, err = db.Exec(`CREATE VIRTUAL TABLE notes_fts USING fts5(content);`)
		if err != nil {
			log.Fatalf("FTS init error: %v", err)
		}

		_, _ = db.Exec(`CREATE TRIGGER notes_fts_insert AFTER INSERT ON notes BEGIN 
			INSERT INTO notes_fts (rowid, content) VALUES (NEW.id, NEW.content); 
		END;`)
		
		_, _ = db.Exec(`CREATE TRIGGER notes_fts_delete AFTER DELETE ON notes BEGIN 
			DELETE FROM notes_fts WHERE rowid = OLD.id; 
		END;`)
		
		_, _ = db.Exec(`CREATE TRIGGER notes_fts_update AFTER UPDATE ON notes BEGIN 
			UPDATE notes_fts SET content = NEW.content WHERE rowid = NEW.id; 
		END;`)

		// Backfill existing notes
		_, _ = db.Exec("INSERT INTO notes_fts(rowid, content) SELECT id, content FROM notes")
	}

	// v2 migration: snapshot the live SQLite database before any v2 DDL.
	// VACUUM INTO creates a transactionally consistent backup, including WAL content.
	var schemaVersion int
	if err := db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil { log.Fatal(err) }
	if schemaVersion > 2 { log.Fatalf("database schema version %d is newer than this Quicknote", schemaVersion) }
	if schemaVersion < 2 {
		if wasExisting {
			backupPath := dbPath + ".pre-v2.bak"
			if _, err := os.Stat(backupPath); os.IsNotExist(err) {
				quoted := strings.ReplaceAll(backupPath, "'", "''")
				if _, err := db.Exec("VACUUM INTO '" + quoted + "'"); err != nil { log.Fatalf("v2 backup failed; refusing migration: %v", err) }
			} else if err != nil { log.Fatalf("inspect backup: %v", err) }
		}
		tx, err := db.Begin()
		if err != nil { log.Fatal(err) }
		fields := []string{
			"ALTER TABLE notes ADD COLUMN revision INTEGER NOT NULL DEFAULT 1",
			"ALTER TABLE notes ADD COLUMN expires_at TEXT",
			"ALTER TABLE notes ADD COLUMN is_pinned INTEGER NOT NULL DEFAULT 0",
		}
		columns := make(map[string]bool)
		rows, err := tx.Query("PRAGMA table_info(notes)")
		if err != nil { _ = tx.Rollback(); log.Fatal(err) }
		for rows.Next() {
			var cid, notnull, pk int
			var name, kind string
			var def sql.NullString
			if err = rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil { break }
			columns[name] = true
		}
		if err == nil { err = rows.Err() }
		_ = rows.Close()
		if err != nil { _ = tx.Rollback(); log.Fatal(err) }
		names := []string{"revision", "expires_at", "is_pinned"}
		for i, statement := range fields {
			if !columns[names[i]] {
				if _, err = tx.Exec(statement); err != nil { _ = tx.Rollback(); log.Fatal(err) }
			}
		}
		if _, err = tx.Exec("CREATE INDEX IF NOT EXISTS notes_expiry_idx ON notes(expires_at) WHERE deleted_at IS NULL AND is_pinned = 0"); err != nil { _ = tx.Rollback(); log.Fatal(err) }
		if _, err = tx.Exec("CREATE INDEX IF NOT EXISTS notes_deleted_at_idx ON notes(deleted_at)"); err != nil { _ = tx.Rollback(); log.Fatal(err) }
		if _, err = tx.Exec("PRAGMA user_version = 2"); err != nil { _ = tx.Rollback(); log.Fatal(err) }
		if err = tx.Commit(); err != nil { log.Fatal(err) }
	}
	// Repair interrupted FTS initialization without destroying note data.
	triggers := []string{
		"CREATE TRIGGER IF NOT EXISTS notes_fts_insert AFTER INSERT ON notes BEGIN INSERT INTO notes_fts(rowid,content) VALUES(NEW.id,NEW.content); END",
		"CREATE TRIGGER IF NOT EXISTS notes_fts_delete AFTER DELETE ON notes BEGIN DELETE FROM notes_fts WHERE rowid=OLD.id; END",
		"CREATE TRIGGER IF NOT EXISTS notes_fts_update AFTER UPDATE ON notes BEGIN UPDATE notes_fts SET content=NEW.content WHERE rowid=NEW.id; END",
	}
	for _, statement := range triggers {
		if _, err = db.Exec(statement); err != nil { log.Fatalf("FTS repair: %v", err) }
	}
		// Retain every trashed note, including identical copies, for 72 full hours.
	// This comparison uses timestamps rather than calendar-day truncation.
	if _, err = db.Exec("DELETE FROM tags WHERE note_id IN (SELECT id FROM notes WHERE deleted_at IS NOT NULL AND datetime(deleted_at) <= datetime('now', '-3 days'))"); err != nil {
		log.Printf("quicknote: cleanup stale tags: %v", err)
	}
	if _, err = db.Exec("DELETE FROM notes WHERE deleted_at IS NOT NULL AND datetime(deleted_at) <= datetime('now', '-3 days')"); err != nil {
		log.Printf("quicknote: cleanup expired trash: %v", err)
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
	if id <= 0 { return "" }
	var content string
	err := db.QueryRow("SELECT content FROM notes WHERE id = ?", id).Scan(&content)
	if err != nil { return "" }
	return content
}

// Blazing fast Full-Text Search via SQLite with exact-line matching
func searchNotes(db *sql.DB, query string, inTrash bool) []Note {
	if strings.TrimSpace(query) == "" {
		notes := loadNotes(db, inTrash)
		for i := range notes {
			cleanContent := strings.TrimSpace(strings.ReplaceAll(notes[i].Content, "\n", " "))
			if len(cleanContent) > 55 {
				notes[i].Preview = cleanContent[:52] + "..."
			} else {
				notes[i].Preview = cleanContent
			}
		}
		return notes
	}

	safeQuery := strings.ReplaceAll(query, "\"", "\"\"")
	matchQuery := safeQuery + "*"

	var sqlQuery string
	if inTrash {
		sqlQuery = `
			SELECT n.id, n.content, n.updated_at, highlight(notes_fts, 0, '<<M_START>>', '<<M_END>>')
			FROM notes_fts f
			JOIN notes n ON f.rowid = n.id
			WHERE notes_fts MATCH ? AND n.deleted_at IS NOT NULL
			ORDER BY rank LIMIT 20
		`
	} else {
		sqlQuery = `
			SELECT n.id, n.content, n.updated_at, highlight(notes_fts, 0, '<<M_START>>', '<<M_END>>')
			FROM notes_fts f
			JOIN notes n ON f.rowid = n.id
			WHERE notes_fts MATCH ? AND n.deleted_at IS NULL
			ORDER BY rank LIMIT 20
		`
	}

	rows, err := db.Query(sqlQuery, matchQuery)
	if err != nil {
		return []Note{}
	}
	defer rows.Close()

	var results []Note
	for rows.Next() {
		var n Note
		var content, updatedAt, highlighted string
		
		_ = rows.Scan(&n.ID, &content, &updatedAt, &highlighted)
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
	
	return results
}

func loadNotes(db *sql.DB, inTrash bool) []Note {
	var query string
	if inTrash {
		query = `SELECT n.id, SUBSTR(n.content, 1, 200), n.updated_at, n.created_at, n.deleted_at, n.kill_hash, n.kill_start, GROUP_CONCAT(t.tag) 
				 FROM notes n LEFT JOIN tags t ON n.id = t.note_id 
				 WHERE n.deleted_at IS NOT NULL GROUP BY n.id ORDER BY n.deleted_at DESC`
	} else {
		query = `SELECT n.id, SUBSTR(n.content, 1, 200), n.updated_at, n.created_at, n.deleted_at, n.kill_hash, n.kill_start, GROUP_CONCAT(t.tag) 
				 FROM notes n LEFT JOIN tags t ON n.id = t.note_id 
				 WHERE n.deleted_at IS NULL GROUP BY n.id ORDER BY n.updated_at DESC`
	}

	rows, err := db.Query(query)
	if err != nil {
		return []Note{{ID: 0, Content: "", UpdatedTime: time.Now(), CreatedTime: time.Now()}}
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var n Note
		var rawUpdated, rawCreated, rawDeleted interface{}
		var killHash, killStartStr, rawTags sql.NullString

		err := rows.Scan(&n.ID, &n.Preview, &rawUpdated, &rawCreated, &rawDeleted, &killHash, &killStartStr, &rawTags)
		if err == nil {
			n.UpdatedTime, n.UpdatedAt = parseSQLiteTime(rawUpdated)
			n.CreatedTime, n.CreatedAt = parseSQLiteTime(rawCreated)
			
			if n.CreatedAt == "" {
				n.CreatedTime = n.UpdatedTime
				n.CreatedAt = n.UpdatedAt
			}

			n.DeletedTime, n.DeletedAt = parseSQLiteTime(rawDeleted)
			
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
		}
		notes = append(notes, n)
	}

	if len(notes) == 0 {
		if inTrash {
			notes = append(notes, Note{ID: -1, Content: "--- Trash is Empty ---", UpdatedAt: ""})
		} else {
			notes = append(notes, Note{ID: 0, Content: "", UpdatedTime: time.Now(), CreatedTime: time.Now()})
		}
	}
	return notes
}

// saveNoteChecked atomically persists content and tags. On failure the
// caller keeps its original note identity and its unsaved editor buffer.
func saveNoteChecked(db *sql.DB, n Note) (Note, error) {
 if n.ID == -1 || (n.ID == 0 && n.Content == "") || n.IsGhost {return n,nil}
 killStartStr:=""
 if !n.KillStart.IsZero() {killStartStr=n.KillStart.Format(time.RFC3339)}
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

 tx,err:=db.Begin()
 if err!=nil {return n,fmt.Errorf("begin save: %w",err)}
 defer tx.Rollback()
 if n.ID==0 {
  var res sql.Result
  res,err=tx.Exec("INSERT INTO notes(content, kill_hash, kill_start) VALUES(?,?,?)",n.Content,n.KillHash,killStartStr)
  if err!=nil {return n,fmt.Errorf("insert note: %w",err)}
  var id int64
  id,err=res.LastInsertId()
  if err!=nil {return n,fmt.Errorf("read new note ID: %w",err)}
  n.ID=int(id)
 } else {
  var res sql.Result
  res,err=tx.Exec("UPDATE notes SET content=?, updated_at=CURRENT_TIMESTAMP, kill_hash=?, kill_start=? WHERE id=? AND deleted_at IS NULL",n.Content,n.KillHash,killStartStr,n.ID)
  if err!=nil {return n,fmt.Errorf("update note %d: %w",n.ID,err)}
  affected,e:=res.RowsAffected()
  if e!=nil {return n,fmt.Errorf("count updated rows: %w",e)}
  if affected!=1 {return n,fmt.Errorf("note %d no longer active",n.ID)}
 }
 if _,err=tx.Exec("DELETE FROM tags WHERE note_id=?",n.ID);err!=nil {return n,fmt.Errorf("clear tags: %w",err)}
 for _,tag:=range n.Tags {
  if _,err=tx.Exec("INSERT INTO tags(note_id,tag) VALUES(?,?)",n.ID,tag);err!=nil {return n,fmt.Errorf("save tag %q: %w",tag,err)}
 }
 var duplicates int
 if err=tx.QueryRow("SELECT count(*) FROM notes WHERE content=? AND id<>? AND deleted_at IS NULL",n.Content,n.ID).Scan(&duplicates);err!=nil {return n,fmt.Errorf("check duplicates: %w",err)}
 if err=tx.Commit();err!=nil {return n,fmt.Errorf("commit note: %w",err)}
 now:=time.Now()
 n.UpdatedTime=now
 n.UpdatedAt=now.Format("02 Jan 2006, 3:04 PM")
 if n.CreatedTime.IsZero() {n.CreatedTime=now;n.CreatedAt=n.UpdatedAt}
 n.IsDuplicate=duplicates>0
 return n,nil
}

// Legacy call sites can use this until their Update paths are converted to
// explicit error handling. The checked API above is preferred.
func saveNote(db *sql.DB,n Note) Note {
 saved,err:=saveNoteChecked(db,n)
 if err!=nil {log.Printf("quicknote: note save failed (id %d): %v",n.ID,err);return n}
 return saved
}

// trashNote never deduplicates content: note IDs remain independently recoverable.
func trashNote(db *sql.DB, id int) {
	if id <= 0 { return }
	if _, err := db.Exec("UPDATE notes SET deleted_at = COALESCE(deleted_at, CURRENT_TIMESTAMP) WHERE id = ?", id); err != nil {
		log.Printf("Error trashing note %d: %v", id, err)
	}
}

func hardDeleteNote(db *sql.DB, id int) {
	if id > 0 {
		_, _ = db.Exec("DELETE FROM tags WHERE note_id = ?", id)
		_, _ = db.Exec("DELETE FROM notes WHERE id = ?", id)
	}
}

func restoreNote(db *sql.DB, id int) {
	if id > 0 {
		_, err := db.Exec("UPDATE notes SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP, kill_start = CURRENT_TIMESTAMP WHERE id = ?", id)
		if err != nil {
			log.Printf("Error restoring note %d: %v", id, err)
		}
	}
}

func deleteNote(db *sql.DB, id int) {
	if id > 0 {
		_, _ = db.Exec("DELETE FROM tags WHERE note_id = ?", id)
		_, err := db.Exec("DELETE FROM notes WHERE id = ?", id)
		if err != nil {
			log.Printf("Error deleting note %d: %v", id, err)
		}
	}
}