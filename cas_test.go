package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func twoStores(t *testing.T) (*SQLiteStore, *SQLiteStore) {
	t.Helper()
	first := newTestDB(t)
	second, err := sql.Open("sqlite3", filepath.Join(os.Getenv("HOME"), ".local/share/quicknote-tui/notes.sqlite")+"?_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	// Separate pools, each constrained to one physical SQLite connection.
	first.SetMaxOpenConns(1)
	second.SetMaxOpenConns(1)
	if err := second.Ping(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { second.Close() })
	return NewSQLiteStore(first), NewSQLiteStore(second)
}

func TestQN011T17TwoIndependentConnections(t *testing.T) {
	a, b := twoStores(t)
	created, err := a.Save(Note{Content: "original #old"})
	if err != nil {
		t.Fatal(err)
	}
	left, err := a.GetNote(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	right, err := b.GetNote(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	left.Content, right.Content = "left #left", "right #right"
	start := make(chan struct{})
	type result struct {
		note Note
		err  error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for i, store := range []*SQLiteStore{a, b} {
		draft := []Note{left, right}[i]
		wg.Add(1)
		go func(s *SQLiteStore, n Note) {
			defer wg.Done()
			<-start
			saved, e := s.Save(n)
			results <- result{saved, e}
		}(store, draft)
	}
	close(start)
	wg.Wait()
	close(results)
	var winner, loser Note
	successes, conflicts := 0, 0
	for r := range results {
		if r.err == nil {
			successes++
			winner = r.note
		} else if errors.Is(r.err, ErrConflict) {
			conflicts++
			loser = r.note
		} else {
			t.Fatalf("unexpected failure: %v", r.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	if winner.Revision != created.Revision+1 || loser.Revision != created.Revision {
		t.Fatalf("revisions winner=%d loser=%d", winner.Revision, loser.Revision)
	}
	persisted, err := a.GetNote(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Content != winner.Content {
		t.Fatalf("winner lost: %q", persisted.Content)
	}
	var tags, fts int
	if err := a.db.QueryRow("SELECT count(*) FROM tags WHERE note_id=? AND tag=?", created.ID, winner.Tags[0]).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRow("SELECT count(*) FROM notes_fts WHERE rowid=? AND content=?", created.ID, winner.Content).Scan(&fts); err != nil {
		t.Fatal(err)
	}
	if tags != 1 || fts != 1 {
		t.Fatalf("derived data mismatch tags=%d fts=%d", tags, fts)
	}
	loser.ID, loser.Revision = 0, 0
	copy, err := b.Save(loser)
	if err != nil {
		t.Fatal(err)
	}
	if copy.ID == winner.ID || copy.Content == winner.Content {
		t.Fatal("losing version not recovered as independent copy")
	}
	// Use a fresh checker after cross-connection writes. SQLite's FTS5
	// connection cache can report a transient checksum mismatch on an idle
	// handle until a normal FTS access refreshes its cached structure.
	checker, err := sql.Open("sqlite3", filepath.Join(os.Getenv("HOME"), ".local/share/quicknote-tui/notes.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer checker.Close()
	var integrity string
	if err := checker.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%s err=%v", integrity, err)
	}
}

func TestQN011MetadataAndLifecycleCAS(t *testing.T) {
	for _, operation := range []string{"pin", "expiry", "trash", "restore", "purge"} {
		t.Run(operation, func(t *testing.T) {
			a, b := twoStores(t)
			n, err := a.Save(Note{Content: "protected #tag"})
			if err != nil {
				t.Fatal(err)
			}
			if operation == "restore" || operation == "purge" {
				if err = a.TrashCAS(n.ID, n.Revision); err != nil {
					t.Fatal(err)
				}
				n, err = a.GetNote(n.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			stale := n
			switch operation {
			case "pin":
				err = a.SetPinnedCAS(n.ID, n.Revision, true)
			case "expiry":
				err = a.SetExpirationCAS(n.ID, n.Revision, sql.NullString{String: "2030-01-01T00:00:00Z", Valid: true})
			case "trash":
				err = a.TrashCAS(n.ID, n.Revision)
			case "restore":
				err = a.RestoreCAS(n.ID, n.Revision)
			case "purge":
				err = a.PurgeCAS(n.ID, n.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			stale.Content = "stale overwrite"
			_, err = b.Save(stale)
			want := ErrConflict
			if operation == "trash" {
				want = ErrTrashed
			}
			if operation == "purge" {
				want = ErrNotFound
			}
			if !errors.Is(err, want) {
				t.Fatalf("expected %v got %v", want, err)
			}
			if operation != "purge" {
				current, e := a.GetNote(n.ID)
				if e != nil {
					t.Fatal(e)
				}
				if current.Content != n.Content || current.Revision != n.Revision+1 {
					t.Fatalf("lost data/revision: %+v", current)
				}
			}
		})
	}
}

func TestQN011StaleMetadataCannotMutateNewContent(t *testing.T) {
	a, b := twoStores(t)
	n, err := a.Save(Note{Content: "original"})
	if err != nil {
		t.Fatal(err)
	}
	updated := n
	updated.Content = "new content"
	updated, err = b.Save(updated)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func() error{
		func() error { return a.TrashCAS(n.ID, n.Revision) },
		func() error { return a.PurgeCAS(n.ID, n.Revision) },
		func() error { return a.SetPinnedCAS(n.ID, n.Revision, true) },
		func() error { return a.SetExpirationCAS(n.ID, n.Revision, sql.NullString{}) },
	} {
		if err := mutate(); !errors.Is(err, ErrConflict) {
			t.Fatalf("stale metadata: %v", err)
		}
	}
	current, err := a.GetNote(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Content != updated.Content || current.Revision != updated.Revision || current.DeletedAt != "" {
		t.Fatalf("changed current note: %+v", current)
	}
}

func TestQN011RollbackIncludesRevision(t *testing.T) {
	a, _ := twoStores(t)
	n, err := a.Save(Note{Content: "original #old"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.db.Exec("CREATE TRIGGER reject_revision_tag BEFORE INSERT ON tags BEGIN SELECT RAISE(ABORT,'tag failure'); END"); err != nil {
		t.Fatal(err)
	}
	draft := n
	draft.Content = "new #new"
	failed, err := a.Save(draft)
	if err == nil {
		t.Fatal("expected tag error")
	}
	if failed.Content != draft.Content || failed.Revision != n.Revision {
		t.Fatal("failed save altered draft")
	}
	current, err := a.GetNote(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != n.Revision || current.Content != n.Content {
		t.Fatal("rollback advanced revision/content")
	}
}

func conflictModel(t *testing.T) (model, *SQLiteStore, Note) {
	t.Helper()
	a, b := twoStores(t)
	n, err := a.Save(Note{Content: "base"})
	if err != nil {
		t.Fatal(err)
	}
	m := initialModel(a.db, nowForTest())
	m.store = a
	m.notes = []Note{n}
	m.buffer = stringToBuffer("my unsaved draft")
	m.needsSave = true
	m.lastActivity = time.Now().Add(-3 * time.Second)
	m.undoStack = []textState{{content: "base"}}
	remote := n
	remote.Content = "other session"
	remote, err = b.Save(remote)
	if err != nil {
		t.Fatal(err)
	}
	return m, b, remote
}

func TestQN011ConflictPreservedAcrossUITransitions(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyCtrlN}, {Type: tea.KeyCtrlT}, {Type: tea.KeyCtrlF}, {Type: tea.KeyCtrlD},
		{Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}, {Type: tea.KeyRight, Alt: true},
	} {
		t.Run(key.String(), func(t *testing.T) {
			m, b, remote := conflictModel(t)
			before := bufferToString(m.buffer)
			next, cmd := m.Update(key)
			got := next.(model)
			if !got.needsSave || bufferToString(got.buffer) != before || got.notes[got.index].ID != remote.ID || len(got.undoStack) != 1 {
				t.Fatal("conflict discarded draft or history")
			}
			if !errors.Is(got.saveErr, ErrConflict) || got.state != stateConflict {
				t.Fatalf("conflict not visible: state=%d err=%v", got.state, got.saveErr)
			}
			if cmd != nil {
				if _, ok := cmd().(tea.QuitMsg); ok {
					t.Fatal("quit lost draft")
				}
			}
			persisted, err := b.GetNote(remote.ID)
			if err != nil || persisted.Content != remote.Content {
				t.Fatal("remote overwritten")
			}
		})
	}
}

func TestQN011ConflictSaveCopyAndReload(t *testing.T) {
	for _, choice := range []string{"c", "r"} {
		t.Run(choice, func(t *testing.T) {
			m, b, remote := conflictModel(t)
			next, _ := m.Update(tickMsg(time.Now()))
			m = next.(model)
			draft := bufferToString(m.buffer)
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(choice)})
			m = next.(model)
			if choice == "r" {
				if !m.needsSave || bufferToString(m.buffer) != draft {
					t.Fatal("reload lacked discard confirmation")
				}
				next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
				m = next.(model)
				if bufferToString(m.buffer) != remote.Content || m.notes[m.index].Revision != remote.Revision {
					t.Fatal("reload not authoritative")
				}
			} else {
				copy, err := b.GetNote(m.notes[m.index].ID)
				if err != nil || copy.Content != draft || copy.ID == remote.ID {
					t.Fatalf("copy recovery failed: %+v %v", copy, err)
				}
			}
			if m.needsSave || m.saveErr != nil || m.state != stateTyping {
				t.Fatal("recovery did not clear failure")
			}
		})
	}
}

func mustGetTestNote(t *testing.T, db *sql.DB, id int) Note {
	t.Helper()
	n, err := NewSQLiteStore(db).GetNote(id)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

type interceptSaveStore struct {
	NoteStore
	beforeSave func()
}

func (s interceptSaveStore) Save(n Note) (Note, error) { s.beforeSave(); return s.NoteStore.Save(n) }

func TestQN011CLIAppendConflictsAndUpdatesTags(t *testing.T) {
	for _, last := range []bool{false, true} {
		t.Run(fmt.Sprint(last), func(t *testing.T) {
			a, b := twoStores(t)
			n, err := a.Save(Note{Content: "# Project\n\nbase #old"})
			if err != nil {
				t.Fatal(err)
			}
			intercepted := interceptSaveStore{NoteStore: a, beforeSave: func() {
				remote, e := b.GetNote(n.ID)
				if e != nil {
					t.Fatal(e)
				}
				remote.Content += "\nremote #remote"
				if _, e = b.Save(remote); e != nil {
					t.Fatal(e)
				}
			}}
			err = persistCLIInputWithStore(a.db, intercepted, "local #local", "Project", last)
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("append bypassed CAS: %v", err)
			}
			current, err := a.GetNote(n.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(current.Content, "local") || current.Revision != n.Revision+1 {
				t.Fatal("concurrent CLI overwrite")
			}
			if err = persistCLIInput(a.db, "retry #local", "Project", last); err != nil {
				t.Fatal(err)
			}
			current, err = a.GetNote(n.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Revision != n.Revision+2 || !strings.Contains(current.Content, "remote") || !strings.Contains(strings.Join(current.Tags, ","), "#local") {
				t.Fatal("CLI retry lost text/tags/revision")
			}
		})
	}
}

func TestQN011ListSearchAndSessionsCarryRevision(t *testing.T) {
	a, b := twoStores(t)
	n, err := a.Save(Note{Content: "findme #tag"})
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range [][]Note{a.List(false), a.Search("findme", false)} {
		if len(snapshot) != 1 || snapshot[0].Revision != n.Revision {
			t.Fatalf("revision missing: %+v", snapshot)
		}
	}
	m := initialModel(a.db, nowForTest())
	m.notes = []Note{n}
	m.buffer = stringToBuffer("local draft")
	m.needsSave = true
	m.stashNote()
	remote := n
	remote.Content = "remote"
	remote, err = b.Save(remote)
	if err != nil {
		t.Fatal(err)
	}
	m.activateNote()
	if m.notes[0].Revision != n.Revision || !m.needsSave {
		t.Fatal("dirty draft adopted remote revision")
	}
	if m.saveCurrent() || !errors.Is(m.saveErr, ErrConflict) {
		t.Fatal("resumed dirty draft bypassed CAS")
	}
}

func TestQN011ConflictPreviewSizesAndScroll(t *testing.T) {
	m, _, _ := conflictModel(t)
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(model)
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}} {
		m.viewport.Width, m.viewport.Height = size[0], size[1]
		view := m.View()
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("overflow at %v", size)
		}
		if !strings.Contains(view, "C Copy") || !strings.Contains(view, "R Reload") {
			t.Fatalf("choices missing at %v: %s", size, view)
		}
	}
	m.viewport.Width, m.viewport.Height = 80, 12
	m.handleConflictKey("i")
	view := m.View()
	if !strings.Contains(view, "Stored 1:") || !strings.Contains(view, "Local 1:") {
		t.Fatalf("comparison missing: %s", view)
	}
	before := bufferToString(m.buffer)
	m.handleConflictKey("down")
	m.handleConflictKey("pgdown")
	m.handleConflictKey("pgup")
	m.handleConflictKey("up")
	if bufferToString(m.buffer) != before || !m.needsSave {
		t.Fatal("inspection modified draft")
	}
}

func TestQN011ErrorClassification(t *testing.T) {
	a, _ := twoStores(t)
	n, err := a.Save(Note{Content: "protected"})
	if err != nil {
		t.Fatal(err)
	}
	stale := n
	stale.Revision = 0
	_, err = a.Save(stale)
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.ID != n.ID || conflict.Expected != 0 || conflict.Actual != n.Revision {
		t.Fatalf("wrong conflict payload: %v", err)
	}
	if _, err = a.Save(Note{ID: n.ID + 100, Revision: 1, Content: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing note: %v", err)
	}
	if err = a.RestoreCAS(n.ID, n.Revision); !errors.Is(err, ErrWrongState) {
		t.Fatalf("restore active state: %v", err)
	}
	if err = a.TrashCAS(n.ID, n.Revision); err != nil {
		t.Fatal(err)
	}
	if err = a.SetPinnedCAS(n.ID, n.Revision, true); !errors.Is(err, ErrTrashed) {
		t.Fatalf("pin trashed note: %v", err)
	}
	if err = a.db.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = a.Save(n)
	if err == nil || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		t.Fatalf("SQLite failure misclassified: %v", err)
	}
}

func TestQN011FailedCopyAndFreshReloadKeepDraftSafe(t *testing.T) {
	m, b, remote := conflictModel(t)
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(model)
	draft := bufferToString(m.buffer)
	if _, err := b.db.Exec("CREATE TRIGGER reject_copy BEFORE INSERT ON notes BEGIN SELECT RAISE(ABORT,'copy failure'); END"); err != nil {
		t.Fatal(err)
	}
	m.handleConflictKey("c")
	if m.state != stateConflict || !m.needsSave || bufferToString(m.buffer) != draft {
		t.Fatal("failed copy lost draft")
	}
	m.handleConflictKey("r")
	remote.Content = "latest at confirmation"
	if _, err := b.Save(remote); err != nil {
		t.Fatal(err)
	}
	m.handleConflictKey("r")
	if bufferToString(m.buffer) != remote.Content {
		t.Fatal("reload used stale conflict preview")
	}
}

func TestQN011DeletedRemoteCanBeRecoveredAsCopy(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(fmt.Sprint(purge), func(t *testing.T) {
			m, b, remote := conflictModel(t)
			var err error
			if purge {
				err = b.PurgeCAS(remote.ID, remote.Revision)
			} else {
				err = b.TrashCAS(remote.ID, remote.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			draft := bufferToString(m.buffer)
			next, _ := m.Update(tickMsg(time.Now()))
			m = next.(model)
			if m.state != stateConflict || !m.needsSave {
				t.Fatal("deleted remote discarded local draft")
			}
			m.handleConflictKey("r")
			m.handleConflictKey("r")
			if m.state != stateConflict || bufferToString(m.buffer) != draft {
				t.Fatal("unavailable reload lost draft")
			}
			m.handleConflictKey("c")
			if m.state != stateTyping || m.needsSave || m.notes[m.index].ID == remote.ID {
				t.Fatal("copy of deleted note failed")
			}
			copy, err := b.GetNote(m.notes[m.index].ID)
			if err != nil || copy.Content != draft {
				t.Fatalf("lost copy: %+v %v", copy, err)
			}
		})
	}
}

func TestQN011BusyWriterDoesNotMasqueradeAsConflict(t *testing.T) {
	a, b := twoStores(t)
	n, err := a.Save(Note{Content: "base"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.db.Exec("PRAGMA busy_timeout=20"); err != nil {
		t.Fatal(err)
	}
	held, err := a.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	n.Content = "retryable draft"
	failed, err := b.Save(n)
	if err == nil || errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		t.Fatalf("lock misclassified: %v", err)
	}
	if failed.Content != n.Content || failed.Revision != n.Revision {
		t.Fatal("busy write changed draft")
	}
	if err = held.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err = b.Save(failed); err != nil {
		t.Fatalf("busy retry failed: %v", err)
	}
}

func TestQN011EmptyConflictCopyIsPersisted(t *testing.T) {
	m, b, remote := conflictModel(t)
	m.buffer = stringToBuffer("")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(model)
	if m.state != stateConflict {
		t.Fatal("stale empty delete should conflict")
	}
	m.handleConflictKey("c")
	copy, err := b.GetNote(m.notes[m.index].ID)
	if err != nil || copy.ID == remote.ID || copy.Content != "" {
		t.Fatalf("empty copy not persisted: %+v %v", copy, err)
	}
}
