package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"time"
	"regexp"   // ADDED
	"strings"  // ADDED

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
}

func initDB() *sql.DB {
	dataDir := filepath.Join(os.Getenv("HOME"), ".local", "share", "quicknote-tui")
	_ = os.MkdirAll(dataDir, 0755)

	dbPath := filepath.Join(dataDir, "notes.sqlite")
	db, err := sql.Open("sqlite3", dbPath+"?_busy_timeout=5000")
	if err != nil {
		log.Fatal(err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		content TEXT
	)`)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS tags (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		note_id INTEGER,
		tag TEXT,
		FOREIGN KEY(note_id) REFERENCES notes(id) ON DELETE CASCADE
	)`)
	if err != nil {
		log.Fatal(err)
	}

	_, _ = db.Exec("ALTER TABLE notes ADD COLUMN kill_hash TEXT DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE notes ADD COLUMN kill_start TEXT DEFAULT ''")
	_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`)
	_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN deleted_at DATETIME`)
	
	// ADDED: Create the created_at column. Ignore error if it already exists.
	_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN created_at DATETIME`)

	_, _ = db.Exec("DELETE FROM notes WHERE deleted_at <= date('now', '-3 days')")

	return db
}

// --- NEW: Bulletproof Date Parser ---
// Returns both the native time.Time and the formatted string
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

func loadNotes(db *sql.DB, inTrash bool) []Note {
	var query string
	if inTrash {
		query = `SELECT n.id, n.content, n.updated_at, n.created_at, n.deleted_at, n.kill_hash, n.kill_start, GROUP_CONCAT(t.tag) 
				 FROM notes n LEFT JOIN tags t ON n.id = t.note_id 
				 WHERE n.deleted_at IS NOT NULL GROUP BY n.id ORDER BY n.deleted_at DESC`
	} else {
		query = `SELECT n.id, n.content, n.updated_at, n.created_at, n.deleted_at, n.kill_hash, n.kill_start, GROUP_CONCAT(t.tag) 
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

		// Scan now includes rawTags
		err := rows.Scan(&n.ID, &n.Content, &rawUpdated, &rawCreated, &rawDeleted, &killHash, &killStartStr, &rawTags)
		if err == nil {
			n.UpdatedTime, n.UpdatedAt = parseSQLiteTime(rawUpdated)
			n.CreatedTime, n.CreatedAt = parseSQLiteTime(rawCreated)
			
			if n.CreatedAt == "" {
				n.CreatedTime = n.UpdatedTime
				n.CreatedAt = n.UpdatedAt
			}
			n.DeletedTime, n.DeletedAt = parseSQLiteTime(rawDeleted)
			
			if killHash.Valid { n.KillHash = killHash.String }
			if killStartStr.Valid && killStartStr.String != "" {
				if t, err := time.Parse(time.RFC3339, killStartStr.String); err == nil {
					n.KillStart = t
				}
			}

			// NEW: Parse the concatenated string back into the struct slice
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

func saveNote(db *sql.DB, n Note) Note {
	if n.ID == -1 { return n }
	if n.Content == "" && n.ID == 0 { return n }
	if n.IsGhost { return n }
	
	killStartStr := ""
	if !n.KillStart.IsZero() {
		killStartStr = n.KillStart.Format(time.RFC3339)
	}

	// 1. Extract Tags (Ignoring keywords/kill timers)
	reTags := regexp.MustCompile(`(?i)#[a-zA-Z0-9_-]+`)
	matches := reTags.FindAllString(n.Content, -1)
	tagMap := make(map[string]bool)
	n.Tags = []string{}
	
	for _, t := range matches {
		tLower := strings.ToLower(t)
		if tLower == "#kill" || tLower == "#idea" || tLower == "#todo" {
			continue
		}
		if !tagMap[tLower] {
			tagMap[tLower] = true
			n.Tags = append(n.Tags, tLower)
		}
	}

	// 2. Save the Note
	if n.ID == 0 {
		res, err := db.Exec("INSERT INTO notes (content, kill_hash, kill_start) VALUES (?, ?, ?)", n.Content, n.KillHash, killStartStr)
		if err == nil {
			id, _ := res.LastInsertId()
			n.ID = int(id)
		}
	} else {
		_, _ = db.Exec("UPDATE notes SET content = ?, updated_at = CURRENT_TIMESTAMP, kill_hash = ?, kill_start = ? WHERE id = ?", n.Content, n.KillHash, killStartStr, n.ID)
	}

	// 3. Sync the Tags Table
	if n.ID > 0 {
		_, _ = db.Exec("DELETE FROM tags WHERE note_id = ?", n.ID)
		for _, tag := range n.Tags {
			_, _ = db.Exec("INSERT INTO tags (note_id, tag) VALUES (?, ?)", n.ID, tag)
		}
	}

	n.UpdatedTime = time.Now()
	n.UpdatedAt = n.UpdatedTime.Format("02 Jan 2006, 3:04 PM")
	if n.CreatedTime.IsZero() {
		n.CreatedTime = n.UpdatedTime
		n.CreatedAt = n.UpdatedAt
	}
	return n
}

func trashNote(db *sql.DB, id int) {
	_, err := db.Exec("UPDATE notes SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	if err != nil {
		log.Printf("Error trashing note: %v", err)
	}
}

func hardDeleteNote(db *sql.DB, id int) {
	if id > 0 {
		_, _ = db.Exec("DELETE FROM notes WHERE id = ?", id)
		_, _ = db.Exec("DELETE FROM tags WHERE note_id = ?", id)
	}
}

func restoreNote(db *sql.DB, id int) {
	if id > 0 {
		_, _ = db.Exec("UPDATE notes SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	}
}

func deleteNote(db *sql.DB, id int) {
	// If the note has an ID > 0, it exists in the database.
	// We perform a hard delete here because these are expired #kill notes 
	// or Ghost notes being purged.
	_, err := db.Exec("DELETE FROM notes WHERE id = ?", id)
	_, _ = db.Exec("DELETE FROM tags WHERE note_id = ?", id)
	if err != nil {
		// We'll log it for now, though in a TUI it's often 
		// better to just fail silently or show a flashMsg.
		log.Printf("Error deleting note %d: %v", id, err)
	}
}