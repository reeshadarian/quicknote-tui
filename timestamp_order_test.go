package main

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

// Preserve the stored bytes as well as the parsed snapshot when checking that
// ordering reads and appending to another note leave historical rows untouched.
type orderingSnapshot struct {
	Note             Note
	Created, Updated string
	Deleted          sql.NullString
}

func orderingSnapshotFor(t *testing.T, db *sql.DB, id int) orderingSnapshot {
	t.Helper()
	s := orderingSnapshot{Note: mustGetTestNote(t, db, id)}
	if err := db.QueryRow(`SELECT CAST(created_at AS TEXT), CAST(updated_at AS TEXT), CAST(deleted_at AS TEXT) FROM notes WHERE id=?`, id).Scan(&s.Created, &s.Updated, &s.Deleted); err != nil {
		t.Fatal(err)
	}
	return s
}

func orderingNote(t *testing.T, db *sql.DB, stamp, token string, trash bool) int {
	t.Helper()
	n, err := NewSQLiteStore(db).Save(Note{Content: "# Project\n\n" + token + " #ordertag"})
	if err != nil {
		t.Fatal(err)
	}
	var deleted any
	if trash {
		deleted = stamp
	}
	if _, err := db.Exec(`UPDATE notes SET created_at=?, updated_at=?, deleted_at=? WHERE id=?`, stamp, stamp, deleted, n.ID); err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func TestQN028MixedTimestampCLIAppend(t *testing.T) {
	cases := []struct {
		name, first, second string
		wantSecond          bool
	}{
		{"legacy newer", "2026-10-12 12:00:00", "2026-10-12T11:00:00.000Z", false},
		{"canonical newer", "2026-10-12T12:00:00.000Z", "2026-10-12 11:00:00", false},
		{"positive offset", "2026-10-12T12:00:00.000Z", "2026-10-12T13:00:00+02:00", false},
		{"negative offset", "2026-10-12T11:00:00Z", "2026-10-12T08:00:00-04:00", true},
		{"millisecond", "2026-10-12T12:00:00.001Z", "2026-10-12T12:00:00.000Z", false},
		{"equal instant ID tie", "2026-10-12T12:00:00Z", "2026-10-12 12:00:00", true},
		{"offset ID tie", "2026-10-12T14:00:00+02:00", "2026-10-12T08:00:00-04:00", true},
	}
	for _, tc := range cases {
		for _, last := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/last=%t", tc.name, last), func(t *testing.T) {
				db := newTestDB(t)
				ids := []int{orderingNote(t, db, tc.first, "firsttoken", false), orderingNote(t, db, tc.second, "secondtoken", false)}
				ids = append(ids, orderingNote(t, db, "2026-10-13T12:00:00Z", "trashtoken", true))
				before := map[int]orderingSnapshot{}
				for _, id := range ids {
					before[id] = orderingSnapshotFor(t, db, id)
				}
				want := ids[0]
				if tc.wantSecond {
					want = ids[1]
				}
				if err := persistCLIInput(db, "appendtoken", "Project", last); err != nil {
					t.Fatal(err)
				}
				for _, id := range ids {
					after := orderingSnapshotFor(t, db, id)
					if id != want {
						if !reflect.DeepEqual(after, before[id]) {
							t.Errorf("non-target ID %d changed; intended ID %d", id, want)
						}
					} else if after.Note.Content != before[id].Note.Content+"\nappendtoken" || after.Note.Revision != before[id].Note.Revision+1 || !reflect.DeepEqual(after.Note.Tags, before[id].Note.Tags) || after.Created != before[id].Created {
						t.Errorf("target ID %d did not receive only the expected append/revision: %+v", id, after)
					}
				}
				var indexedID, count int
				if err := db.QueryRow(`SELECT rowid FROM notes_fts WHERE notes_fts MATCH 'appendtoken'`).Scan(&indexedID); err != nil || indexedID != want {
					t.Errorf("appended FTS owner = %d, error %v; want %d", indexedID, err, want)
				}
				if err := db.QueryRow(`SELECT count(*) FROM notes`).Scan(&count); err != nil || count != len(ids) {
					t.Fatalf("note count %d, error %v", count, err)
				}
				if _, err := db.Exec(`INSERT INTO notes_fts(notes_fts,rank) VALUES('integrity-check',1)`); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestQN028MixedTimestampLists(t *testing.T) {
	for _, trash := range []bool{false, true} {
		t.Run(fmt.Sprintf("trash=%t", trash), func(t *testing.T) {
			db := newTestDB(t)
			stamps := []string{
				"2026-10-12 12:00:00",       // noon UTC, lower ID
				"2026-10-12T11:00:00.000Z",  // 11:00
				"2026-10-12T13:00:00+02:00", // 11:00, higher ID
				"2026-10-12T08:00:00-04:00", // noon UTC, higher ID
				"2026-10-12T12:00:00.001Z",  // one millisecond after noon
				"2026-10-11T23:30:00-14:00", // 13:30 UTC on October 12
			}
			var ids []int
			before := map[int]orderingSnapshot{}
			for i, stamp := range stamps {
				id := orderingNote(t, db, stamp, fmt.Sprintf("token%d", i), trash)
				ids = append(ids, id)
				before[id] = orderingSnapshotFor(t, db, id)
			}
			orderingNote(t, db, "2026-10-13T12:00:00Z", "otherscope", !trash)
			want := []int{ids[5], ids[4], ids[3], ids[0], ids[2], ids[1]}
			store := NewSQLiteStore(db)
			for _, read := range []func() ([]Note, error){func() ([]Note, error) { return store.ListChecked(trash) }, func() ([]Note, error) { return store.SearchChecked("", trash) }} {
				notes, err := read()
				if err != nil {
					t.Fatal(err)
				}
				var got []int
				for _, n := range notes {
					got = append(got, n.ID)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("chronological IDs = %v; want %v", got, want)
				}
			}
			for _, id := range ids {
				if after := orderingSnapshotFor(t, db, id); !reflect.DeepEqual(after, before[id]) {
					t.Errorf("read changed ID %d", id)
				}
			}
		})
	}
}
