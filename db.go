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
	IsGhost   bool
	KillHash  string
	KillStart time.Time
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

	_, _ = db.Exec("ALTER TABLE notes ADD COLUMN kill_hash TEXT DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE notes ADD COLUMN kill_start TEXT DEFAULT ''")

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
        query = "SELECT id, content, updated_at, deleted_at, kill_hash, kill_start FROM notes WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC"
    } else {
        query = "SELECT id, content, updated_at, deleted_at, kill_hash, kill_start FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC"
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
        
        // Setup variables to catch the new timer columns safely
        var killHash sql.NullString
        var killStartStr sql.NullString

        err := rows.Scan(&n.ID, &n.Content, &rawUpdated, &rawDeleted, &killHash, &killStartStr)
        if err == nil {
            n.UpdatedAt = parseSQLiteTime(rawUpdated)
            n.DeletedAt = parseSQLiteTime(rawDeleted)
            
            // Restore the timer state if it exists
            if killHash.Valid {
                n.KillHash = killHash.String
            }
            if killStartStr.Valid && killStartStr.String != "" {
                if t, err := time.Parse(time.RFC3339, killStartStr.String); err == nil {
                    n.KillStart = t
                }
            }
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

	if n.IsGhost { return n }
	
    // Convert the start time to a string for SQLite
    killStartStr := ""
    if !n.KillStart.IsZero() {
        killStartStr = n.KillStart.Format(time.RFC3339)
    }

    if n.ID == 0 {
        res, err := db.Exec("INSERT INTO notes (content, kill_hash, kill_start) VALUES (?, ?, ?)", n.Content, n.KillHash, killStartStr)
        if err == nil {
            id, _ := res.LastInsertId()
            n.ID = int(id)
        }
    } else {
        _, _ = db.Exec("UPDATE notes SET content = ?, updated_at = CURRENT_TIMESTAMP, kill_hash = ?, kill_start = ? WHERE id = ?", n.Content, n.KillHash, killStartStr, n.ID)
    }

    // Instantly update the timestamp in memory so the UI updates as soon as you type!
    n.UpdatedAt = time.Now().Format("02 Jan 2006, 3:04 PM")
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
	if err != nil {
		// We'll log it for now, though in a TUI it's often 
		// better to just fail silently or show a flashMsg.
		log.Printf("Error deleting note %d: %v", id, err)
	}
}