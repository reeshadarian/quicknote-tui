package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrNotFound   = errors.New("note not found")
	ErrTrashed    = errors.New("note is in trash")
	ErrConflict   = errors.New("note changed in another session")
	ErrWrongState = errors.New("note is not in the required state")
)

// ConflictError identifies the stale snapshot without logging either text.
type ConflictError struct {
	ID               int
	Expected, Actual int64
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("note %d changed in another session (revision %d, expected %d)", e.ID, e.Actual, e.Expected)
}
func (e *ConflictError) Unwrap() error { return ErrConflict }

// Get remains a compatibility content-only reader. Editors use GetNote so
// content and its base revision always come from one SQLite snapshot.
type NoteStore interface {
	LifecycleStore
	Get(int) (string, error)
	GetNote(int) (Note, error)
	List(bool) []Note
	ListChecked(bool) ([]Note, error)
	Search(string, bool) []Note
	SearchChecked(string, bool) ([]Note, error)
	Save(Note) (Note, error)
	SaveCopy(Note) (Note, error)
	Trash(int) error
	Restore(int) error
	Delete(int) error
	Purge(int) error
	TrashCAS(int, int64) error
	RestoreCAS(int, int64) error
	PurgeCAS(int, int64) error
}

type SQLiteStore struct {
	db    *sql.DB
	clock Clock
}

func NewSQLiteStore(db *sql.DB) *SQLiteStore      { return NewSQLiteStoreWithClock(db, RealClock{}) }
func (s *SQLiteStore) Get(id int) (string, error) { n, err := s.GetNote(id); return n.Content, err }
func NewSQLiteStoreWithClock(db *sql.DB, clock Clock) *SQLiteStore {
	return &SQLiteStore{db: db, clock: clockOrReal(clock)}
}
func (s *SQLiteStore) GetNote(id int) (Note, error) { return getNoteSnapshot(s.db, id) }

type noteQuerier interface{ QueryRow(string, ...any) *sql.Row }

func getNoteSnapshot(q noteQuerier, id int) (Note, error) {
	var n Note
	var updated, created, deleted any
	var hash, start, tags sql.NullString
	err := q.QueryRow(`SELECT id,COALESCE(content,''),revision,CAST(updated_at AS TEXT),CAST(created_at AS TEXT),CAST(deleted_at AS TEXT),kill_hash,kill_start,expires_at,is_pinned,expiration_source,expiration_needs_review,delete_reason,(SELECT GROUP_CONCAT(tag) FROM tags WHERE note_id=notes.id) FROM notes WHERE id=?`, id).Scan(&n.ID, &n.Content, &n.Revision, &updated, &created, &deleted, &hash, &start, &n.ExpiresAt, &n.IsPinned, &n.ExpirationSource, &n.ExpirationNeedsReview, &n.DeleteReason, &tags)
	if errors.Is(err, sql.ErrNoRows) {
		return n, fmt.Errorf("note %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return n, err
	}
	n.UpdatedTime, n.UpdatedAt = parseSQLiteTime(updated)
	n.CreatedTime, n.CreatedAt = parseSQLiteTime(created)
	n.DeletedTime, n.DeletedAt = rawLifecycleTime(deleted)
	n.KillHash = hash.String
	n.KillStart, _ = ParseLifecycleTime(start.String)
	if tags.Valid && tags.String != "" {
		n.Tags = strings.Split(tags.String, ",")
	}
	return n, nil
}
func (s *SQLiteStore) List(trash bool) []Note             { return loadNotes(s.db, trash) }
func (s *SQLiteStore) Search(q string, trash bool) []Note { return searchNotes(s.db, q, trash) }
func (s *SQLiteStore) Save(n Note) (Note, error) {
	return saveNoteSnapshotAt(s.db, n, false, clockOrReal(s.clock).Now())
}

// SaveCopy is explicit recovery, including an intentionally empty draft.
// Ordinary autosave still avoids inserting an untouched empty new note.
func (s *SQLiteStore) SaveCopy(n Note) (Note, error) {
	n.ID, n.Revision = 0, 0
	return saveNoteSnapshotAt(s.db, n, true, clockOrReal(s.clock).Now())
}

// Classify zero-row CAS inside the transaction that attempted the write.
// State failures are distinct from revision conflicts and SQLite failures.
func casMiss(tx *sql.Tx, id int, expected int64, activeOnly bool) error {
	var actual int64
	var deleted sql.NullString
	err := tx.QueryRow("SELECT revision,deleted_at FROM notes WHERE id=?", id).Scan(&actual, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("note %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return err
	}
	if activeOnly && deleted.Valid {
		return fmt.Errorf("note %d: %w", id, ErrTrashed)
	}
	if actual != expected {
		return &ConflictError{ID: id, Expected: expected, Actual: actual}
	}
	return fmt.Errorf("note %d: %w", id, ErrWrongState)
}

func (s *SQLiteStore) mutateCAS(id int, expected int64, activeOnly bool, query string, args ...any) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	args = append(args, id, expected)
	res, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return casMiss(tx, id, expected, activeOnly)
	}
	return tx.Commit()
}
func (s *SQLiteStore) TrashCAS(id int, expected int64) error {
	_, e := s.TrashSnapshotCAS(id, expected, DeleteManual, clockOrReal(s.clock).Now())
	return e
}
func (s *SQLiteStore) RestoreCAS(id int, expected int64) error {
	_, e := s.RestoreSnapshotCAS(id, expected, RestoreWithoutTimer, clockOrReal(s.clock).Now())
	return e
}
func (s *SQLiteStore) SetPinnedCAS(id int, expected int64, pinned bool) error {
	_, e := s.ApplyPinnedCAS(id, expected, pinned, clockOrReal(s.clock).Now())
	return e
}
func (s *SQLiteStore) SetExpirationCAS(id int, expected int64, at sql.NullString) error {
	c := ExpirationChoice{Source: ExpirationPreset}
	if at.Valid {
		t, e := ParseLifecycleTime(at.String)
		if e != nil {
			return e
		}
		c.Deadline = &t
	}
	_, e := s.ApplyExpirationCAS(id, expected, c, clockOrReal(s.clock).Now())
	return e
}
func (s *SQLiteStore) PurgeCAS(id int, expected int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Delete tags only after the revision guard succeeds; roll back together.
	res, err := tx.Exec("DELETE FROM notes WHERE id=? AND revision=?", id, expected)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return casMiss(tx, id, expected, false)
	}
	if _, err = tx.Exec("DELETE FROM tags WHERE note_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// Legacy ID-only operations take a fresh snapshot, then still guard the write.
// UI operations must use the CAS variants with the displayed revision.
func (s *SQLiteStore) Trash(id int) error {
	n, e := s.GetNote(id)
	if e != nil {
		return e
	}
	return s.TrashCAS(id, n.Revision)
}
func (s *SQLiteStore) Restore(id int) error {
	n, e := s.GetNote(id)
	if e != nil {
		return e
	}
	return s.RestoreCAS(id, n.Revision)
}
func (s *SQLiteStore) Delete(id int) error { return s.Purge(id) }
func (s *SQLiteStore) Purge(id int) error {
	n, e := s.GetNote(id)
	if e != nil {
		return e
	}
	return s.PurgeCAS(id, n.Revision)
}

func (s *SQLiteStore) ListChecked(trash bool) ([]Note, error) { return loadNotesChecked(s.db, trash) }
func (s *SQLiteStore) SearchChecked(q string, trash bool) ([]Note, error) {
	return searchNotesChecked(s.db, q, trash)
}
