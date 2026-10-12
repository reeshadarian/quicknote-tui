package main

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }
func lifecycleNow() time.Time       { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) }
func TestQN028DirectivePrecedence(t *testing.T) {
	now := lifecycleNow()
	n, e := DeriveExpiration(Note{}, "#kill 15 mins", now)
	if e != nil || n.ExpiresAt.String != "2026-10-12T12:15:00.000Z" {
		t.Fatalf("derive: %+v %v", n, e)
	}
	same, _ := DeriveExpiration(n, "#kill 15 mins", now.Add(time.Hour))
	if same.ExpiresAt != n.ExpiresAt {
		t.Fatal("reset unchanged deadline")
	}
	changed, _ := DeriveExpiration(n, "#kill 1 hour", now)
	if changed.ExpiresAt.String != "2026-10-12T13:00:00.000Z" {
		t.Fatal(changed)
	}
	removed, _ := DeriveExpiration(n, "ordinary", now)
	if removed.ExpiresAt.Valid {
		t.Fatal("directive removal retained expiry")
	}
	n.ExpirationSource = ExpirationPreset
	removed, _ = DeriveExpiration(n, "ordinary", now)
	if removed.ExpiresAt != n.ExpiresAt {
		t.Fatal("removal cleared preset")
	}
	n.IsPinned = true
	n.ExpiresAt = sql.NullString{}
	p, _ := DeriveExpiration(n, "#kill 2 hours", now)
	if p.ExpiresAt.Valid || p.KillHash == n.KillHash {
		t.Fatal("pin observation")
	}
	d, e := ParseKillDirective("```\n#kill 3 hours\n```\n`#kill 9 days`\n// #kill 2 hours\n#kill 1.5 mins\n#kill -1 day")
	if e != nil || d.Duration != 90*time.Second {
		t.Fatalf("parser: %+v %v", d, e)
	}
}
func TestQN028UnsafeDirectiveReview(t *testing.T) {
	for _, s := range []string{"#kill 0 m", "#kill -1 day", "#kill NaN hours", "#kill 1e99 days", "#kill 999999999999999999999 days", "#kill 2 garbage"} {
		n, e := DeriveExpiration(Note{}, s, lifecycleNow())
		if e == nil || !n.ExpirationNeedsReview || n.ExpiresAt.Valid {
			t.Fatalf("unsafe %q: %+v %v", s, n, e)
		}
	}
}
func FuzzQN028KillDirective(f *testing.F) {
	for _, s := range []string{"#kill 15 mins", "#kill NaN days", "```\n#kill 1 hour", "#kill .1 s"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, e := ParseKillDirective(s)
		if e == nil && d.Present && d.Duration <= 0 {
			t.Fatal("unsafe duration")
		}
	})
}
func TestQN028MaintenanceRetentionBoundary(t *testing.T) {
	db := newTestDB(t)
	now := lifecycleNow()
	s := NewSQLiteStoreWithClock(db, fixedClock{now})
	n, e := s.Save(Note{Content: "#kill 15 mins #test"})
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.Maintain(now.Add(15*time.Minute), nil)
	if e != nil || len(r.Expired) != 1 {
		t.Fatalf("expiry %+v %v", r, e)
	}
	n, e = s.GetNote(n.ID)
	if e != nil || n.DeleteReason != DeleteExpired {
		t.Fatal(n, e)
	}
	at := now.Add(15*time.Minute + 72*time.Hour)
	r, e = s.Maintain(at, nil)
	if e != nil || len(r.Purged) != 0 {
		t.Fatal("boundary", r, e)
	}
	r, e = s.Maintain(at.Add(time.Millisecond), nil)
	if e != nil || len(r.Purged) != 1 {
		t.Fatal("past boundary", r, e)
	}
	if _, e = s.GetNote(n.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
}

func TestQN028StorePrecedenceRoundTrip(t *testing.T) {
	db := newTestDB(t)
	now := lifecycleNow()
	s := NewSQLiteStoreWithClock(db, fixedClock{now})
	n, e := s.Save(Note{Content: "#kill 15 mins"})
	if e != nil {
		t.Fatal(e)
	}
	original := n.ExpiresAt
	n, e = s.ApplyPinnedCAS(n.ID, n.Revision, true, now)
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.ApplyPinnedCAS(n.ID, n.Revision, false, now)
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.Save(n)
	if e != nil || n.ExpiresAt.Valid {
		t.Fatal("unpin rearmed", n, e)
	}
	n.Content = "#kill 1 hour"
	n, e = s.Save(n)
	if e != nil || !n.ExpiresAt.Valid || n.ExpiresAt == original {
		t.Fatal(n, e)
	}
	n, e = s.TrashSnapshotCAS(n.ID, n.Revision, DeleteManual, now)
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.RestoreSnapshotCAS(n.ID, n.Revision, RestoreWithoutTimer, now)
	if e != nil {
		t.Fatal(e)
	}
	n, e = s.Save(n)
	if e != nil || n.ExpiresAt.Valid {
		t.Fatal("restore rearmed", n, e)
	}
	at := now.Add(3 * time.Hour)
	n, e = s.ApplyExpirationCAS(n.ID, n.Revision, ExpirationChoice{Deadline: &at, Source: ExpirationPreset}, now)
	if e != nil {
		t.Fatal(e)
	}
	n.Content = "removed directive"
	n, e = s.Save(n)
	if e != nil || n.ExpiresAt.String != FormatLifecycleTime(at) {
		t.Fatal("preset removed", n, e)
	}
}
func TestQN028MaintenanceRollbackAndInvalidTrash(t *testing.T) {
	db := newTestDB(t)
	now := lifecycleNow()
	s := NewSQLiteStoreWithClock(db, fixedClock{now})
	n, e := s.Save(Note{Content: "note #tag #kill 1 minute"})
	if e != nil {
		t.Fatal(e)
	}
	s.Maintain(now.Add(time.Minute), nil)
	bad, e := s.Save(Note{Content: "bad deletion #tag"})
	if e != nil {
		t.Fatal(e)
	}
	db.Exec("UPDATE notes SET deleted_at='malformed' WHERE id=?", bad.ID)
	db.Exec("CREATE TRIGGER fail_purge BEFORE DELETE ON notes BEGIN SELECT RAISE(ABORT,'purge blocked'); END")
	if _, e = s.Maintain(now.Add(73*time.Hour), nil); e == nil {
		t.Fatal("purge failure ignored")
	}
	var count int
	for _, q := range []string{"SELECT count(*) FROM notes WHERE id=?", "SELECT count(*) FROM tags WHERE note_id=?", "SELECT count(*) FROM notes_fts WHERE rowid=?"} {
		if e = db.QueryRow(q, n.ID).Scan(&count); e != nil || count != 1 {
			t.Fatal(q, count, e)
		}
	}
	db.Exec("DROP TRIGGER fail_purge")
	if _, e = s.Maintain(now.Add(73*time.Hour), nil); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetNote(bad.ID); e != nil {
		t.Fatal("purged malformed deletion", e)
	}
}
func TestQN028ProtectedAndExcludedNotes(t *testing.T) {
	db := newTestDB(t)
	now := lifecycleNow()
	s := NewSQLiteStoreWithClock(db, fixedClock{now})
	for i := 0; i < 4; i++ {
		n, e := s.Save(Note{Content: "#kill 1 minute"})
		if e != nil {
			t.Fatal(e)
		}
		switch i {
		case 0:
			s.ApplyPinnedCAS(n.ID, n.Revision, true, now)
		case 1:
			db.Exec("UPDATE notes SET expiration_needs_review=1 WHERE id=?", n.ID)
		case 2:
			db.Exec("UPDATE notes SET expires_at=? WHERE id=?", FormatLifecycleTime(now.Add(time.Hour)), n.ID)
		}
	}
	r, e := s.Maintain(now.Add(time.Minute), []NoteID{4})
	if e != nil || len(r.Expired) != 0 {
		t.Fatal(r, e)
	}
}

func TestQN028CodeAndExplicitEdits(t *testing.T) {
	for _, text := range []string{"``#kill 15 mins``", "```inline #kill 15 mins```", "<!-- #kill 15 mins -->", "~~~\n#kill 15 mins\n~~~"} {
		d, e := ParseKillDirective(text)
		if e != nil || d.Present {
			t.Fatal("code/comment scheduled timer", text, d, e)
		}
	}
	now := lifecycleNow()
	n, _ := DeriveExpiration(Note{}, "#kill 15 mins", now)
	same, _ := DeriveExpiration(n, "#KILL   15   MINS", now.Add(time.Hour))
	if same.ExpiresAt != n.ExpiresAt {
		t.Fatal("case/spacing restarted")
	}
	changed, _ := DeriveExpiration(n, "#kill .25 hours", now.Add(time.Hour))
	if changed.ExpiresAt == n.ExpiresAt {
		t.Fatal("explicit directive edit was ignored")
	}
}

func TestQN028ReviewCodeBoundaries(t *testing.T) {
	cases := map[string]string{
		"long_backtick_fence":   "````markdown\n```\n#kill 1 second\n```\n````",
		"long_tilde_fence":      "~~~~text\n~~~\n#kill 1 second\n~~~\n~~~~",
		"multiline_span":        "prefix `code\n#kill 1 minute\nend`",
		"multiline_double_span": "prefix ``code `\n#kill 1 minute\nend``",
		"comment_fence":         "<!--\n```\n-->\n```\n#kill 1 minute\n```",
		"false_closing_fence":   "```text\n``` not a closing fence\n#kill 1 second\n```",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			n, e := DeriveExpiration(Note{}, text, lifecycleNow())
			if e != nil || n.ExpiresAt.Valid {
				t.Fatal("code armed timer", n.ExpiresAt, e)
			}
		})
	}
	for _, text := range []string{"````md\n```\n#kill 1 second\n`````\n#kill 2 hours", "~~~~\n~~~\n#kill 1 second\n~~~~\n#kill 2 hours", "start `code\n#kill 1 minute\nend` #kill 2 hours"} {
		d, e := ParseKillDirective(text)
		if e != nil || !d.Present || d.Duration != 2*time.Hour {
			t.Fatal("outside directive not recognized", d, e)
		}
	}
}
