package main

import (
	"database/sql"
	"fmt"
	"time"
)

type LifecycleStore interface {
	ApplyExpirationCAS(NoteID, int64, ExpirationChoice, time.Time) (Note, error)
	ApplyPinnedCAS(NoteID, int64, bool, time.Time) (Note, error)
	TrashSnapshotCAS(NoteID, int64, DeleteReason, time.Time) (Note, error)
	ExpireNoteCAS(NoteID, int64, time.Time) (Note, error)
	RestoreSnapshotCAS(NoteID, int64, RestoreMode, time.Time) (Note, error)
	DeleteTrashCAS(NoteID, int64) error
	Maintain(time.Time, []NoteID) (MaintenanceResult, error)
}

func (s *SQLiteStore) lifecycleCAS(id NoteID, expected int64, now time.Time, change func(Note) (Note, error)) (Note, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return Note{}, e
	}
	defer tx.Rollback()
	n, e := getNoteSnapshot(tx, id)
	if e != nil {
		return n, e
	}
	baseRevision := n.Revision
	n, e = change(n)
	if e != nil {
		return n, e
	}
	if baseRevision != expected {
		return n, &ConflictError{ID: id, Expected: expected, Actual: baseRevision}
	}
	var deleted any
	if n.DeletedAt != "" {
		deleted = n.DeletedAt
	}
	_, e = tx.Exec(`UPDATE notes SET expires_at=?,is_pinned=?,expiration_source=?,expiration_needs_review=?,kill_hash=?,kill_start=?,deleted_at=?,delete_reason=?,updated_at=?,revision=revision+1 WHERE id=? AND revision=?`, n.ExpiresAt, n.IsPinned, n.ExpirationSource, n.ExpirationNeedsReview, n.KillHash, formatOptionalTime(n.KillStart), deleted, n.DeleteReason, FormatLifecycleTime(now), id, expected)
	if e != nil {
		return n, e
	}
	n, e = getNoteSnapshot(tx, id)
	if e != nil {
		return n, e
	}
	e = tx.Commit()
	return n, e
}
func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return FormatLifecycleTime(t)
}
func observeDirective(n Note) Note {
	d, e := ParseKillDirective(n.Content)
	if e == nil {
		n.KillHash = d.Fingerprint
	}
	n.ExpirationNeedsReview = e != nil
	return n
}
func (s *SQLiteStore) ApplyExpirationCAS(id NoteID, expected int64, c ExpirationChoice, now time.Time) (Note, error) {
	return s.lifecycleCAS(id, expected, now, func(n Note) (Note, error) {
		if n.DeletedAt != "" {
			return n, ErrTrashed
		}
		if c.Deadline != nil && (n.IsPinned || !c.Deadline.After(now) || c.Deadline.Year() > 9999 || c.Deadline.Year() < 1) {
			return n, fmt.Errorf("unpin first and choose a future expiration")
		}
		if c.Source != "" && c.Source != ExpirationPreset && c.Source != ExpirationNone {
			return n, fmt.Errorf("invalid expiration choice source")
		}
		n = observeDirective(n)
		n.ExpiresAt = sql.NullString{}
		n.ExpirationSource = ExpirationNone
		if c.Deadline != nil {
			n.ExpiresAt = sql.NullString{String: FormatLifecycleTime(*c.Deadline), Valid: true}
			n.ExpirationSource = ExpirationPreset
		}
		return n, nil
	})
}
func (s *SQLiteStore) ApplyPinnedCAS(id NoteID, expected int64, pinned bool, now time.Time) (Note, error) {
	return s.lifecycleCAS(id, expected, now, func(n Note) (Note, error) {
		if n.DeletedAt != "" {
			return n, ErrTrashed
		}
		n = observeDirective(n)
		n.IsPinned = pinned
		n.ExpiresAt = sql.NullString{}
		n.ExpirationSource = ExpirationNone
		return n, nil
	})
}
func (s *SQLiteStore) TrashSnapshotCAS(id NoteID, expected int64, reason DeleteReason, now time.Time) (Note, error) {
	if reason != DeleteManual && reason != DeleteExpired {
		return Note{}, fmt.Errorf("invalid deletion reason")
	}
	return s.lifecycleCAS(id, expected, now, func(n Note) (Note, error) {
		if n.DeletedAt != "" {
			return n, ErrTrashed
		}
		if reason == DeleteExpired && !noteDue(n, now) {
			return n, ErrWrongState
		}
		n.DeletedAt = FormatLifecycleTime(now)
		n.DeleteReason = reason
		return n, nil
	})
}
func (s *SQLiteStore) ExpireNoteCAS(id NoteID, expected int64, now time.Time) (Note, error) {
	return s.TrashSnapshotCAS(id, expected, DeleteExpired, now)
}
func (s *SQLiteStore) RestoreSnapshotCAS(id NoteID, expected int64, mode RestoreMode, now time.Time) (Note, error) {
	return s.lifecycleCAS(id, expected, now, func(n Note) (Note, error) {
		if n.DeletedAt == "" {
			return n, ErrWrongState
		}
		// A delayed UI action must not extend the elapsed recovery window even
		// when periodic maintenance has not removed this row yet. Uncertain
		// legacy timestamps remain recoverable rather than guessing an age.
		if at, err := ParseLifecycleTime(n.DeletedAt); err == nil && now.After(at.Add(72*time.Hour)) {
			return n, ErrNotFound
		}
		if mode != RestoreWithoutTimer && mode != RestoreResetDirective {
			return n, ErrWrongState
		}
		if mode == RestoreResetDirective {
			d, e := ParseKillDirective(n.Content)
			if e != nil || !d.Present || n.ExpirationSource != ExpirationDirective || n.IsPinned || n.ExpirationNeedsReview {
				return n, fmt.Errorf("no validated directive timer to reset")
			}
			n.KillHash = ""
			n, e = DeriveExpiration(n, n.Content, now)
			if e != nil {
				return n, e
			}
		} else {
			n = observeDirective(n)
			n.ExpiresAt = sql.NullString{}
			n.ExpirationSource = ExpirationNone
		}
		n.DeletedAt = ""
		n.DeletedTime = time.Time{}
		n.DeleteReason = ""
		return n, nil
	})
}
func (s *SQLiteStore) DeleteTrashCAS(id NoteID, expected int64) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	n, e := getNoteSnapshot(tx, id)
	if e != nil {
		return e
	}
	if n.Revision != expected {
		return &ConflictError{ID: id, Expected: expected, Actual: n.Revision}
	}
	if n.DeletedAt == "" {
		return ErrWrongState
	}
	if e = deleteLifecycleRow(tx, id, expected); e != nil {
		return e
	}
	return tx.Commit()
}
func deleteLifecycleRow(tx *sql.Tx, id NoteID, revision int64) error {
	if _, e := tx.Exec("DELETE FROM tags WHERE note_id=?", id); e != nil {
		return e
	}
	res, e := tx.Exec("DELETE FROM notes WHERE id=? AND revision=? AND deleted_at IS NOT NULL", id, revision)
	if e != nil {
		return e
	}
	n, e := res.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func (s *SQLiteStore) Maintain(now time.Time, protectedIDs []NoteID) (MaintenanceResult, error) {
	result := MaintenanceResult{}
	tx, e := s.db.Begin()
	if e != nil {
		return result, e
	}
	defer tx.Rollback()
	protected := map[NoteID]bool{}
	for _, id := range protectedIDs {
		protected[id] = true
	}
	// Canonical expiry timestamps use the partial index. Legacy trash timestamps
	// use a coarse SQLite cutoff, then exact Go comparison below (strict 72h).
	rows, e := tx.Query(`SELECT id FROM notes WHERE deleted_at IS NULL AND is_pinned=0 AND expiration_needs_review=0 AND expires_at<=? UNION SELECT id FROM notes WHERE deleted_at IS NOT NULL AND julianday(deleted_at)<=julianday(?)`, FormatLifecycleTime(now), FormatLifecycleTime(now.Add(-72*time.Hour).Add(time.Second)))
	if e != nil {
		return result, e
	}
	var ids []int
	for rows.Next() {
		var id int
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return result, e
		}
		if !protected[id] {
			ids = append(ids, id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return result, e
	}
	for _, id := range ids {
		n, err := getNoteSnapshot(tx, id)
		if err != nil {
			return MaintenanceResult{}, err
		}
		if n.DeletedAt != "" {
			at, err := ParseLifecycleTime(n.DeletedAt)
			if err != nil || !at.Before(now.Add(-72*time.Hour)) {
				continue
			}
			if err = deleteLifecycleRow(tx, id, n.Revision); err != nil {
				return MaintenanceResult{}, err
			}
			result.Purged = append(result.Purged, id)
			continue
		}
		if !noteDue(n, now) {
			continue
		}
		res, err := tx.Exec("UPDATE notes SET deleted_at=?,delete_reason=?,revision=revision+1 WHERE id=? AND revision=? AND deleted_at IS NULL AND is_pinned=0", FormatLifecycleTime(now), DeleteExpired, id, n.Revision)
		if err != nil {
			return MaintenanceResult{}, err
		}
		count, err := res.RowsAffected()
		if err != nil {
			return MaintenanceResult{}, err
		}
		if count == 1 {
			result.Expired = append(result.Expired, LifecycleChange{id, n.Revision + 1, now, DeleteExpired})
		}
	}
	if e = tx.Commit(); e != nil {
		return MaintenanceResult{}, e
	}
	return result, nil
}
