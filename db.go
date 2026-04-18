package main

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Note struct {
	ID        int
	Content   string
	UpdatedAt string
	DeletedAt string
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
	if err != nil {
		log.Fatal(err)
	}

	_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN updated_at DATETIME DEFAULT CURRENT_TIMESTAMP`)
	_, _ = db.Exec(`ALTER TABLE notes ADD COLUMN deleted_at DATETIME`)

	_, _ = db.Exec("DELETE FROM notes WHERE deleted_at <= date('now', '-3 days')")

	return db
}

// --- NEW: Bulletproof Date Parser ---
// This handles both native time.Time objects and raw strings gracefully
func parseSQLiteTime(val interface{}) string {
	if val == nil {
		return ""
	}

	// 1. If the driver already parsed it into a native Go time.Time
	if t, ok := val.(time.Time); ok {
		return t.Local().Format("02 Jan 2006, 3:04 PM")
	}

	// 2. If it came through as a raw string or byte slice
	strVal := ""
	if b, ok := val.([]byte); ok {
		strVal = string(b)
	} else if s, ok := val.(string); ok {
		strVal = s
	}

	if strVal != "" {
		// SQLite CURRENT_TIMESTAMP generates "YYYY-MM-DD HH:MM:SS" in UTC
		if t, err := time.Parse("2006-01-02 15:04:05", strVal); err == nil {
			return t.Local().Format("02 Jan 2006, 3:04 PM")
		}
		if t, err := time.Parse(time.RFC3339, strVal); err == nil {
			return t.Local().Format("02 Jan 2006, 3:04 PM")
		}
	}
	return ""
}

func loadNotes(db *sql.DB, inTrash bool) []Note {
	var query string
	if inTrash {
		query = "SELECT id, content, updated_at, deleted_at FROM notes WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC"
	} else {
		query = "SELECT id, content, updated_at, deleted_at FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC"
	}

	rows, err := db.Query(query)
	if err != nil {
		return []Note{{ID: 0, Content: "", UpdatedAt: "Just now"}}
	}
	defer rows.Close()

	var notes []Note
	for rows.Next() {
		var n Note
		var rawUpdated interface{}
		var rawDeleted interface{}

		// Scan into empty interfaces so we gracefully catch the data
		err := rows.Scan(&n.ID, &n.Content, &rawUpdated, &rawDeleted)
		if err == nil {
			n.UpdatedAt = parseSQLiteTime(rawUpdated)
			n.DeletedAt = parseSQLiteTime(rawDeleted)
		}

		notes = append(notes, n)
	}

	if len(notes) == 0 {
		if inTrash {
			notes = append(notes, Note{ID: -1, Content: "--- Trash is Empty ---", UpdatedAt: ""})
		} else {
			notes = append(notes, Note{ID: 0, Content: "", UpdatedAt: "Just now"})
		}
	}
	return notes
}

func saveNote(db *sql.DB, n Note) Note {
	if n.ID == -1 { return n }
	if n.Content == "" && n.ID == 0 { return n }

	if n.ID == 0 {
		res, err := db.Exec("INSERT INTO notes (content) VALUES (?)", n.Content)
		if err == nil {
			id, _ := res.LastInsertId()
			n.ID = int(id)
		}
	} else {
		_, _ = db.Exec("UPDATE notes SET content = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", n.Content, n.ID)
	}

	// Instantly update the timestamp in memory so the UI updates as soon as you type!
	n.UpdatedAt = time.Now().Format("02 Jan 2006, 3:04 PM")
	return n
}

func trashNote(db *sql.DB, id int) {
	if id > 0 {
		_, _ = db.Exec("UPDATE notes SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	}
}

func hardDeleteNote(db *sql.DB, id int) {
	if id > 0 {
		_, _ = db.Exec("DELETE FROM notes WHERE id = ?", id)
	}
}

func restoreNote(db *sql.DB, id int) {
	if id > 0 {
		_, _ = db.Exec("UPDATE notes SET deleted_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	}
}