package main

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestQN032RetentionAndDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{time.Date(2026, 3, 7, 12, 0, 0, 0, loc), time.Date(2026, 10, 31, 12, 0, 0, 0, loc)} {
		for _, elapsed := range []time.Duration{72 * time.Hour, 72*time.Hour + time.Millisecond} {
			t.Run(at.String()+elapsed.String(), func(t *testing.T) {
				m, a, _, n, _ := ttlModel(t)
				m.clock = fixedClock{at}
				m.timerNow = at
				m = updateTest(t, m, keyMessage("ctrl+d"))
				got, err := a.GetNote(n.ID)
				if err != nil {
					t.Fatal(err)
				}
				end := at.Add(elapsed)
				m.clock = fixedClock{end}
				m.timerNow = end
				m = updateTest(t, m, ActionMsg{"trash.undoLast"})
				restored, _ := a.GetNote(n.ID)
				if elapsed == 72*time.Hour {
					if restored.DeletedAt != "" || restored.ID != n.ID {
						t.Fatal("exact elapsed boundary must restore", restored, m.flashMsg)
					}
				} else {
					if restored.DeletedAt == "" {
						t.Fatal("late Undo restored")
					}
					if _, err = a.RestoreSnapshotCAS(n.ID, got.Revision, RestoreWithoutTimer, end); !errors.Is(err, ErrNotFound) {
						t.Fatal("store restored past recovery bound", err)
					}
					result, err := a.Maintain(end, nil)
					if err != nil || len(result.Purged) != 1 {
						t.Fatal(result, err)
					}
					result, err = a.Maintain(end, nil)
					if err != nil || len(result.Purged) != 0 {
						t.Fatal("purge not idempotent", result, err)
					}
				}
			})
		}
	}
}
func TestQN032UndoCannotOverwriteRemoteRestore(t *testing.T) {
	m, a, b, n, _ := ttlModel(t)
	m = typeTest(m, " local")
	text := m.buffer.Text()
	history := append([]textState(nil), m.undoStack...)
	m = updateTest(t, m, keyMessage("ctrl+d"))
	trashed, _ := a.GetNote(n.ID)
	remote, err := b.RestoreSnapshotCAS(n.ID, trashed.Revision, RestoreWithoutTimer, lifecycleNow())
	if err != nil {
		t.Fatal(err)
	}
	remote.Content = "external winner"
	remote, err = b.Save(remote)
	if err != nil {
		t.Fatal(err)
	}
	m = updateTest(t, m, ActionMsg{"trash.undoLast"})
	got, _ := a.GetNote(n.ID)
	if got.Content != "external winner" || got.Revision != remote.Revision || m.state != stateConfirm || m.confirmation.Kind != "error" {
		t.Fatal("stale Undo overwrote or hid failure", got, m.state, m.flashMsg)
	}
	d := m.drafts[n.ID]
	if d.buffer.Text() != text || !reflect.DeepEqual(d.undoStack, history) {
		t.Fatal("stale Undo lost history")
	}
}
func TestQN032UndoKeepsHistoryAndCtrlZ(t *testing.T) {
	m, _, _, n, _ := ttlModel(t)
	before := m.buffer.Text()
	m = typeTest(m, " edited")
	edited := m.buffer.Text()
	history := append([]textState(nil), m.undoStack...)
	m = updateTest(t, m, keyMessage("ctrl+d"))
	m = updateTest(t, m, ActionMsg{"trash.undoLast"})
	if m.activeID() != n.ID {
		t.Fatal("Undo did not return to restored note")
	}
	m = drainTestCmd(t, m, m.requestOpen(n.ID, false, nil, false))
	if m.buffer.Text() != edited || !reflect.DeepEqual(m.undoStack, history) {
		t.Fatal("undo trash lost draft history")
	}
	m = updateTest(t, m, keyMessage("ctrl+z"))
	if m.buffer.Text() != before {
		t.Fatal("CtrlZ ceased being text undo", m.buffer.Text())
	}
}
func TestQN032GhostTrashLateUndo(t *testing.T) {
	for _, remoteRestore := range []bool{false, true} {
		t.Run(string(rune('0'+boolInt(remoteRestore))), func(t *testing.T) {
			m, a, b, n, other := ttlModel(t)
			m = updateTest(t, m, keyMessage("ctrl+g"))
			m = updateTest(t, m, keyMessage("tab"))
			m = updateTest(t, m, keyMessage("tab"))
			m, cmd := rawUpdate(m, keyMessage("enter"))
			if cmd == nil {
				t.Fatal("missing conversion")
			}
			result := cmd().(lifecycleResult)
			m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
			if remoteRestore {
				remote, err := b.RestoreSnapshotCAS(n.ID, result.Note.Revision, RestoreWithoutTimer, lifecycleNow())
				if err != nil {
					t.Fatal(err)
				}
				m = drainTestCmd(t, m, m.requestOpen(remote.ID, false, nil, false))
			}
			active := m.activeID()
			m = updateTest(t, m, result)
			if m.trashUndo.ID != n.ID || m.trashUndo.Revision != result.Note.Revision || m.activeID() != active {
				t.Fatal("late manual write not recorded correctly", m.trashUndo)
			}
			m = updateTest(t, m, ActionMsg{"trash.undoLast"})
			got, _ := a.GetNote(n.ID)
			if got.DeletedAt != "" {
				t.Fatal("ghostTrash undo failed", got, m.flashMsg)
			}
			if remoteRestore && (m.state != stateConfirm || m.confirmation.Kind != "error") {
				t.Fatal("stale ghost undo not guarded")
			}
		})
	}
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func TestQN032DeleteForeverRollbackAndRace(t *testing.T) {
	for _, kind := range []string{"note-trigger", "tag-trigger", "remote-restore", "purged"} {
		t.Run(kind, func(t *testing.T) {
			m, a, b, n := trashModel(t)
			// Tags are fixture data unrelated to the note's directive.
			if _, err := a.db.Exec("INSERT INTO tags(note_id,tag) VALUES(?,?)", n.ID, "retained"); err != nil {
				t.Fatal(err)
			}
			m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
			m = updateTest(t, m, keyMessage("tab"))
			m, cmd := rawUpdate(m, keyMessage("enter"))
			if cmd == nil {
				t.Fatal("missing delete worker")
			}
			switch kind {
			case "note-trigger":
				_, err := a.db.Exec("CREATE TRIGGER block_delete BEFORE DELETE ON notes BEGIN SELECT RAISE(ABORT,'blocked'); END")
				if err != nil {
					t.Fatal(err)
				}
			case "tag-trigger":
				_, err := a.db.Exec("CREATE TRIGGER block_delete BEFORE DELETE ON tags BEGIN SELECT RAISE(ABORT,'blocked'); END")
				if err != nil {
					t.Fatal(err)
				}
			case "remote-restore":
				if _, err := b.RestoreSnapshotCAS(n.ID, n.Revision, RestoreWithoutTimer, lifecycleNow()); err != nil {
					t.Fatal(err)
				}
			case "purged":
				if err := b.DeleteTrashCAS(n.ID, n.Revision); err != nil {
					t.Fatal(err)
				}
			}
			m = drainTestCmd(t, m, cmd)
			if m.activeID() != n.ID || m.buffer.Text() != n.Content || m.state != stateConfirm || m.confirmation.Kind != "error" {
				t.Fatal("failed delete lost selection or error", m.state, m.flashMsg)
			}
			if kind == "purged" {
				return
			}
			got, err := a.GetNote(n.ID)
			if err != nil || got.Content != n.Content {
				t.Fatal("failure lost note", got, err)
			}
			var tags, fts int
			if err = a.db.QueryRow("SELECT count(*) FROM tags WHERE note_id=? AND tag='retained'", n.ID).Scan(&tags); err != nil {
				t.Fatal(err)
			}
			if err = a.db.QueryRow("SELECT count(*) FROM notes_fts WHERE rowid=?", n.ID).Scan(&fts); err != nil {
				t.Fatal(err)
			}
			if tags != 1 || fts != 1 {
				t.Fatal("rollback lost tags/FTS", tags, fts)
			}
		})
	}
}
func TestQN032DeleteRestoreConcurrentCAS(t *testing.T) {
	a, b := twoStores(t)
	n, err := a.Save(Note{Content: "same #tag"})
	if err != nil {
		t.Fatal(err)
	}
	n, err = a.TrashSnapshotCAS(n.ID, n.Revision, DeleteManual, lifecycleNow())
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-start; results <- a.DeleteTrashCAS(n.ID, n.Revision) }()
	go func() {
		defer wg.Done()
		<-start
		_, err := b.RestoreSnapshotCAS(n.ID, n.Revision, RestoreWithoutTimer, lifecycleNow())
		results <- err
	}()
	close(start)
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("exactly one CAS winner", wins)
	}
	got, err := a.GetNote(n.ID)
	if err == nil && (got.DeletedAt != "" || got.Content != n.Content || got.Revision != n.Revision+1) {
		t.Fatal(got)
	}
	var integrity string
	if err := a.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatal(integrity, err)
	}
}
func TestQN032ReadonlyScrollAndSearch(t *testing.T) {
	m, a, _, n := trashModel(t)
	// Replace only the synthetic fixture through SQL, then fetch the full snapshot.
	content := strings.Repeat("line\n", 70) + "FINAL PREVIEW LINE"
	if _, err := a.db.Exec("UPDATE notes SET content=?,revision=revision+1 WHERE id=?", content, n.ID); err != nil {
		t.Fatal(err)
	}
	m = drainTestCmd(t, m, m.requestOpen(n.ID, true, nil, true))
	m = updateTest(t, m, keyMessage("ctrl+f"))
	m = updateTest(t, m, keyMessage("enter"))
	if m.state != stateTrash || m.activeID() != n.ID {
		t.Fatal("search identity not Trash", m.state)
	}
	for i := 0; i < 100; i++ {
		m = updateTest(t, m, keyMessage("down"))
	}
	if !strings.Contains(m.View(), "FINAL PREVIEW LINE") {
		t.Fatal("cannot read entire Trash note", m.View())
	}
	if m.buffer.Text() != content || m.needsSave {
		t.Fatal("scroll changed draft")
	}
}

func TestQN032TrashWhileMetadataPending(t *testing.T) {
	m, a, _, n, _ := ttlModel(t)
	m, cmd := rawUpdate(m, ActionMsg{"note.pin"})
	if cmd == nil {
		t.Fatal("no pin worker")
	}
	m, _ = rawUpdate(m, keyMessage("ctrl+d"))
	m = drainTestCmd(t, m, cmd)
	got, err := a.GetNote(n.ID)
	if err != nil || got.DeletedAt == "" || got.DeleteReason != DeleteManual {
		t.Fatal("public Trash lost behind metadata write", got, err, m.flashMsg)
	}
}
func TestQN032LateTrashPreservesNewerDraft(t *testing.T) {
	m, a, _, n, _ := ttlModel(t)
	m, cmd := rawUpdate(m, keyMessage("ctrl+d"))
	if cmd == nil {
		t.Fatal("no Trash worker")
	}
	result := cmd().(lifecycleResult)
	m = typeTest(m, " newest")
	text := m.buffer.Text()
	history := append([]textState(nil), m.undoStack...)
	m = updateTest(t, m, result)
	if m.buffer.Text() != text || !reflect.DeepEqual(m.undoStack, history) || !m.needsSave || m.state != stateConflict || m.trashUndo.ID != n.ID {
		t.Fatal("late Trash lost newest recovery", m.state, m.flashMsg)
	}
	got, _ := a.GetNote(n.ID)
	if got.Content != n.Content || got.DeletedAt == "" {
		t.Fatal("unexpected persisted snapshot", got)
	}
}
func TestQN032RestoreReadonlyFailure(t *testing.T) {
	m, a, _, n := trashModel(t)
	if _, err := a.db.Exec("PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	m = restoreChoice(t, m, 1)
	got, err := a.GetNote(n.ID)
	if err != nil || got.DeletedAt == "" || got.Revision != n.Revision || m.activeID() != n.ID || m.confirmation.Kind != "error" {
		t.Fatal("read-only restore lost note/error", got, err, m.state)
	}
}

func TestQN032PendingUnsavedSaveThenTrash(t *testing.T) {
	m, a, _, _, _ := ttlModel(t)
	m = updateTest(t, m, keyMessage("ctrl+n"))
	m = typeTest(m, "first save pending")
	oldKey := m.activeKey()
	cmd := m.requestSave()
	if cmd == nil {
		t.Fatal("no create worker")
	}
	m, _ = rawUpdate(m, keyMessage("ctrl+d"))
	m = drainTestCmd(t, m, cmd)
	var id int
	if err := a.db.QueryRow("SELECT id FROM notes WHERE content='first save pending'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	n, err := a.GetNote(id)
	if err != nil || n.DeletedAt == "" || m.trashUndo.ID != id {
		t.Fatal("save acknowledgment did not soft-Trash assigned ID", n, err, m.flashMsg)
	}
	if _, ok := m.drafts[oldKey]; ok {
		t.Fatal("old unsaved identity retained")
	}
}

func TestQN032UndoTracksLatestIntentAcrossLateAcknowledgments(t *testing.T) {
	m, _, _, n, other := ttlModel(t)
	m, first := rawUpdate(m, keyMessage("ctrl+d"))
	held := first().(lifecycleResult)
	m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
	// Clock corrections do not make an earlier deletion the latest user action.
	m.clock = fixedClock{lifecycleNow().Add(-time.Minute)}
	m = updateTest(t, m, keyMessage("ctrl+d"))
	latest := m.trashUndo
	m = updateTest(t, m, held)
	if m.trashUndo.ID != other.ID || m.trashUndo != latest {
		t.Fatal("older held deletion stole Undo", n.ID, m.trashUndo)
	}
	m = updateTest(t, m, ActionMsg{"trash.undoLast"})
	// Duplicate delivery must not offer already consumed or older deletion again.
	m = updateTest(t, m, held)
	if m.trashUndo.ID != 0 {
		t.Fatal("Undo target resurrected")
	}
}

func TestQN032ConfirmedLifecycleUsesActualActionTime(t *testing.T) {
	t.Run("reset", func(t *testing.T) {
		m, a, _, n := trashModel(t)
		m.clock = nil
		m.timerNow = time.Now().Add(-time.Hour)
		m = updateTest(t, m, keyMessage("ctrl+r"))
		m = updateTest(t, m, keyMessage("tab"))
		m = updateTest(t, m, keyMessage("tab"))
		before := time.Now()
		m = updateTest(t, m, keyMessage("enter"))
		after := time.Now()
		got, err := a.GetNote(n.ID)
		if err != nil {
			t.Fatal(err)
		}
		deadline, err := ParseLifecycleTime(got.ExpiresAt.String)
		if err != nil {
			t.Fatal(err)
		}
		if deadline.Before(before.Add(15*time.Minute)) || deadline.After(after.Add(15*time.Minute+time.Millisecond)) {
			t.Fatal("reset used old display tick instead of actual confirmation time", deadline, before, after)
		}
	})
	t.Run("manual Trash", func(t *testing.T) {
		m, a, _, n, _ := ttlModel(t)
		m.clock = nil
		m.timerNow = time.Now().Add(-time.Hour)
		before := time.Now()
		m = updateTest(t, m, keyMessage("ctrl+d"))
		after := time.Now()
		got, err := a.GetNote(n.ID)
		if err != nil {
			t.Fatal(err)
		}
		at, err := ParseLifecycleTime(got.DeletedAt)
		if err != nil {
			t.Fatal(err)
		}
		if at.Before(before) || at.After(after.Add(time.Millisecond)) {
			t.Fatal("manual Trash shortened recovery using old display tick", at, before, after)
		}
	})
}
