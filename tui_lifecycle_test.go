package main

import (
	"database/sql"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"testing"
	"time"
)

func lifecycleModel(t *testing.T) (model, *SQLiteStore, Note) {
	t.Helper()
	db := newTestDB(t)
	now := lifecycleNow()
	s := NewSQLiteStoreWithClock(db, fixedClock{now})
	n, e := s.Save(Note{Content: "#kill 1 minute\noriginal"})
	if e != nil {
		t.Fatal(e)
	}
	m := initialModel(db, now)
	m.startupPending = false
	m.store = s
	m.clock = fixedClock{now.Add(time.Minute)}
	m.notes = []Note{n}
	m.activateNote()
	return m, s, n
}
func TestQN028DirtyDraftExpiry(t *testing.T) {
	m, s, n := lifecycleModel(t)
	m.buffer = stringToBuffer(n.Content + " newest")
	m.needsSave = true
	m.localVersion++
	m = drainTestCmd(t, m, m.requestMaintenance())
	stored, e := s.GetNote(n.ID)
	if e != nil || stored.Content != n.Content+" newest" || stored.DeleteReason != DeleteExpired || m.needsSave {
		t.Fatal(stored, m.needsSave, e)
	}
}
func TestQN028StaleMaintenance(t *testing.T) {
	m, s, n := lifecycleModel(t)
	r, e := s.Maintain(lifecycleNow().Add(time.Minute), nil)
	if e != nil {
		t.Fatal(e)
	}
	m.maintenanceGeneration = 1
	m.maintenancePending = true
	m.buffer = stringToBuffer("newest after sweep started")
	m.localVersion++
	m.needsSave = true
	m.undoStack = []textState{{content: n.Content}}
	m = updateTest(t, m, maintenanceResult{1, r, nil})
	if m.buffer.Text() != "newest after sweep started" || len(m.undoStack) != 1 || !m.needsSave || !errors.Is(m.saveErr, ErrTrashed) {
		t.Fatal("lost raced draft", m.saveErr, m.buffer.Text())
	}
}
func TestQN028MaintenanceInOverlay(t *testing.T) {
	for _, state := range []appState{stateHelp, stateSearch, stateCommands} {
		t.Run(string(rune('a'+state)), func(t *testing.T) {
			m, s, _ := lifecycleModel(t)
			other, e := s.Save(Note{Content: "other #kill 1 minute"})
			if e != nil {
				t.Fatal(e)
			}
			m.state = state
			cmd := m.requestMaintenance()
			if m.requestMaintenance() != nil {
				t.Fatal("overlapping sweep")
			}
			m = drainTestCmd(t, m, cmd)
			n, e := s.GetNote(other.ID)
			if e != nil || n.DeleteReason != DeleteExpired {
				t.Fatal(n, e)
			}
			if m.state != state {
				t.Fatal("overlay lost")
			}
		})
	}
}
func TestQN028LateExpiryKeepsNewerText(t *testing.T) {
	m, s, n := lifecycleModel(t)
	cmd := m.requestLifecycle(lifecycleIntent{Kind: "expire"})
	msg := cmd().(lifecycleResult)
	m.buffer = stringToBuffer("typed while expiration pending")
	m.needsSave = true
	m.localVersion++
	m = updateTest(t, m, msg)
	if m.buffer.Text() != "typed while expiration pending" || !m.needsSave || !errors.Is(m.saveErr, ErrTrashed) {
		t.Fatal("lost late edit", m.saveErr)
	}
	stored, e := s.GetNote(n.ID)
	if e != nil || stored.Content != n.Content || stored.DeleteReason != DeleteExpired {
		t.Fatal(stored, e)
	}
}
func TestQN028FailedDraftSaveDelaysExpiry(t *testing.T) {
	m, s, n := lifecycleModel(t)
	s.db.Exec("CREATE TRIGGER block_save BEFORE UPDATE ON notes BEGIN SELECT RAISE(ABORT,'synthetic save failure'); END")
	m.buffer = stringToBuffer(n.Content + " dirty")
	m.needsSave = true
	m.localVersion++
	m = drainTestCmd(t, m, m.requestMaintenance())
	stored, e := s.GetNote(n.ID)
	if e != nil || stored.DeletedAt != "" || !m.needsSave || m.saveErr == nil {
		t.Fatal(stored, e, m.saveErr)
	}
}
func TestQN028ClockAndGhost(t *testing.T) {
	m, _, n := lifecycleModel(t)
	m.clock = nil
	m.Note.IsGhost = true
	m.Note.ID = 0
	m.Note.Content = n.Content
	m.buffer = stringToBuffer(n.Content)
	m.needsSave = true
	old := m.Note.KillHash
	next, cmd := m.Update(tickMsg(lifecycleNow().Add(2 * time.Hour)))
	m = drainTestCmd(t, next.(model), cmd)
	if m.Note.ID != 0 || m.Note.KillHash != old || !m.Note.IsGhost {
		t.Fatal("ghost timer acted")
	}
	m.clock = fixedClock{lifecycleNow()}
	m.lastMaintenance = lifecycleNow().Add(time.Hour)
	if m.requestMaintenance() == nil {
		t.Fatal("backward clock blocked sweep")
	}
}

func TestQN028LateSaveAfterSweepPreservesDeletion(t *testing.T) {
	m, s, n := lifecycleModel(t)
	m.buffer = stringToBuffer(n.Content + " saved newest")
	m.needsSave = true
	m.localVersion++
	cmd := m.requestSave()
	saved := cmd().(saveResult)
	r, e := s.Maintain(lifecycleNow().Add(time.Minute), nil)
	if e != nil {
		t.Fatal(e)
	}
	m.maintenanceGeneration = 1
	m = updateTest(t, m, maintenanceResult{1, r, nil})
	m = updateTest(t, m, saved)
	if m.Note.DeletedAt == "" || m.Note.Revision != saved.Note.Revision+1 || m.buffer.Text() != saved.Note.Content {
		t.Fatal("late save revived expired metadata", m.Note)
	}
}

func TestQN028MetadataRequestSnapshotAndExit(t *testing.T) {
	m, _, n := lifecycleModel(t)
	at := lifecycleNow().Add(2 * time.Hour)
	expected := at
	cmd := m.requestLifecycle(lifecycleIntent{Kind: "expiration", Expiration: ExpirationChoice{Deadline: &at, Source: ExpirationPreset}})
	at = at.Add(time.Hour)
	escape := tea.KeyMsg{Type: tea.KeyEsc}
	m.pendingAction = &escape
	if m.finishExit() != nil {
		t.Fatal("quit before lifecycle result")
	}
	result := cmd().(lifecycleResult)
	if result.Err != nil || result.Note.ExpiresAt.String != FormatLifecycleTime(expected) || result.Note.ID != n.ID {
		t.Fatal(result)
	}
	quit := m.receiveLifecycle(result)
	if quit == nil {
		t.Fatal("exit did not resume")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("expected quit")
	}
}

func TestQN028StartupFailureKeepsTypedDraftVisible(t *testing.T) {
	m, _, _ := lifecycleModel(t)
	m.buffer = stringToBuffer("typed before startup completed")
	m.needsSave = true
	m.localVersion++
	m = updateTest(t, m, initDataMsg{err: errors.New("synthetic maintenance failure")})
	if m.buffer.Text() != "typed before startup completed" || !m.needsSave || m.state != stateConfirm {
		t.Fatal("startup error hidden or draft lost")
	}
}

func TestQN028ReviewExpiryConflictReadsRemote(t *testing.T) {
	for _, kind := range []string{"lifecycle", "maintenance"} {
		t.Run(kind, func(t *testing.T) {
			m, s, n := lifecycleModel(t)
			var msg tea.Msg
			if kind == "lifecycle" {
				msg = m.requestLifecycle(lifecycleIntent{Kind: "expire"})()
			} else {
				result, e := s.Maintain(lifecycleNow().Add(time.Minute), nil)
				if e != nil {
					t.Fatal(e)
				}
				m.maintenanceGeneration = 1
				msg = maintenanceResult{1, result, nil}
			}
			m.buffer = stringToBuffer("latest retained local text")
			m.localVersion++
			m.needsSave = true
			m = updateTest(t, m, msg)
			if m.state != stateConflict || m.conflictReadErr != nil || m.conflictRemote.ID != n.ID || m.conflictRemote.DeletedAt == "" {
				t.Fatal("remote comparison not loaded", m.state, m.conflictReadErr, m.conflictRemote)
			}
			if m.buffer.Text() != "latest retained local text" || !m.needsSave {
				t.Fatal("lost local text")
			}
		})
	}
}

func TestQN028ReviewLoadAfterSweep(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprint(retained), func(t *testing.T) {
			m, s, b := lifecycleModel(t)
			if retained {
				m.stashNote()
			}
			a, e := s.Save(Note{Content: "active untimed"})
			if e != nil {
				t.Fatal(e)
			}
			m.notes = []Note{a, b}
			m.index = 0
			m.activateNote()
			sweep := m.requestMaintenance()
			loaded := m.requestOpen(b.ID, false, nil, false)().(loadResult)
			if loaded.Err != nil {
				t.Fatal(loaded.Err)
			}
			m = drainTestCmd(t, m, sweep)
			m = updateTest(t, m, loaded)
			stored, e := s.GetNote(b.ID)
			if e != nil {
				t.Fatal(e)
			}
			if m.Note.Revision != stored.Revision || m.Note.DeletedAt == "" || m.state != stateTrash {
				t.Fatal("stale load revived expired target", m.Note, m.state, stored)
			}
		})
	}
}

func TestQN028ReviewLateLifecycleAfterRestore(t *testing.T) {
	m, s, n := lifecycleModel(t)
	result := m.requestLifecycle(lifecycleIntent{Kind: "expire"})().(lifecycleResult)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	var path string
	if e := s.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite3", path+"?_busy_timeout=5000&_txlock=immediate&_foreign_keys=on")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	restored, e := NewSQLiteStore(db).RestoreSnapshotCAS(n.ID, result.Note.Revision, RestoreWithoutTimer, lifecycleNow().Add(2*time.Minute))
	if e != nil {
		t.Fatal(e)
	}
	m = updateTest(t, m, m.requestOpen(n.ID, false, nil, false)())
	if m.Note.Revision != restored.Revision {
		t.Fatal("setup", m.Note)
	}
	m.pushOverlay(stateHelp)
	m.pushOverlay(stateCommands)
	frames := append([]OverlayFrame(nil), m.overlays...)
	m = updateTest(t, m, result)
	if m.Note.Revision != restored.Revision || m.Note.DeletedAt != "" || m.state != stateCommands || m.needsSave || len(m.overlays) != len(frames) {
		t.Fatal("late ack regressed restored owner", m.Note, m.state)
	}
	for i := range frames {
		if m.overlays[i].State != frames[i].State || m.overlays[i].Previous != frames[i].Previous {
			t.Fatal("late ack changed parent overlay")
		}
	}
	m.popOverlay()
	m.popOverlay()
	if m.state != stateTyping {
		t.Fatal("parent focus corrupted", m.state)
	}
}

func TestQN028ReviewLateLifecycleAfterSweep(t *testing.T) {
	m, s, n := lifecycleModel(t)
	at := lifecycleNow().Add(2 * time.Hour)
	result := m.requestLifecycle(lifecycleIntent{Kind: "expiration", Expiration: ExpirationChoice{Deadline: &at, Source: ExpirationPreset}})().(lifecycleResult)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	sweep, e := s.Maintain(at, nil)
	if e != nil {
		t.Fatal(e)
	}
	m.maintenanceGeneration = 1
	m = updateTest(t, m, maintenanceResult{1, sweep, nil})
	m = updateTest(t, m, result)
	stored, e := s.GetNote(n.ID)
	if e != nil || m.Note.Revision != stored.Revision || m.Note.DeletedAt == "" || m.state != stateTrash {
		t.Fatal("old lifecycle ack replaced sweep", m.Note, m.state, e)
	}
}
