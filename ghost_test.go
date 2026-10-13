package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Observe the actual model/store boundary while delegating legitimate saves to
// SQLite. A ghost payload must never reach either write entry point.
type ghostWriteSpy struct {
	NoteStore
	t        *testing.T
	sentinel string
}

func (s ghostWriteSpy) Save(n Note) (Note, error)     { s.reject(n); return s.NoteStore.Save(n) }
func (s ghostWriteSpy) SaveCopy(n Note) (Note, error) { s.reject(n); return s.NoteStore.SaveCopy(n) }
func (s ghostWriteSpy) reject(n Note) {
	s.t.Helper()
	if n.IsGhost || strings.Contains(n.Content, s.sentinel) {
		s.t.Fatal("ghost payload crossed persistence boundary")
	}
}

func TestQN031IndependentPrivacyAudit(t *testing.T) {
	const sentinel = "QN031MEMORYSENTINEL"
	m, a, _, first, second := asyncModel(t)
	m.store = ghostWriteSpy{a, t, sentinel}
	var captured bytes.Buffer
	old := log.Writer()
	log.SetOutput(&captured)
	defer log.SetOutput(old)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(sentinel + " #qn031private\n#kill 1s\n2+2"), Paste: true})
	m = updateTest(t, m, keyMessage("="))
	m = updateTest(t, m, ActionMsg{"edit.undo"})
	m = updateTest(t, m, ActionMsg{"edit.redo"})
	m = drainTestCmd(t, m, m.requestSave())
	if cmd := m.saveSnapshot(m.Note, m.localVersion); cmd != nil {
		m = drainTestCmd(t, m, cmd)
	}
	m = drainTestCmd(t, m, m.requestMaintenance())
	m = updateTest(t, m, ActionMsg{"search.open"})
	m = updateTest(t, m, keyMessage("esc"))
	m = updateTest(t, m, ActionMsg{"note.promote"})
	m = updateTest(t, m, ActionMsg{"app.exit"})
	for _, original := range []Note{first, second} {
		n, err := a.GetNote(original.ID)
		if err != nil || n.Content != original.Content || n.Revision != original.Revision {
			t.Fatal("privacy flow altered persisted source")
		}
	}
	for _, q := range []string{"SELECT count(*) FROM notes WHERE content LIKE '%QN031MEMORYSENTINEL%'", "SELECT count(*) FROM notes_fts WHERE notes_fts MATCH 'QN031MEMORYSENTINEL'", "SELECT count(*) FROM tags WHERE tag='qn031private'"} {
		var count int
		if err := a.db.QueryRow(q).Scan(&count); err != nil || count != 0 {
			t.Fatal("sentinel in persistent data", count, err)
		}
	}
	var seq int
	var name, path string
	if err := a.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(filepath.Dir(path), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(sentinel)) {
			return fmt.Errorf("sentinel in synthetic file %s", entry.Name())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(captured.String(), sentinel) {
		t.Fatal("sentinel logged")
	}
	restarted := initialModel(a.db, nowForTest())
	restarted = drainTestCmd(t, restarted, restarted.Init())
	for _, n := range restarted.notes {
		if n.IsGhost || strings.Contains(n.Content, sentinel) {
			t.Fatal("ghost survived restart")
		}
	}
}

func TestQN031ExplicitOutputWritesOnlyConfirmedSnapshot(t *testing.T) {
	shim := t.TempDir()
	capture := filepath.Join(shim, "clipboard.txt")
	if err := os.WriteFile(filepath.Join(shim, "pbcopy"), []byte("#!/bin/sh\n/bin/cat > \"$QN031_CLIPBOARD\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim)
	t.Setenv("QN031_CLIPBOARD", capture)
	m, a, _, _, _ := asyncModel(t)
	m = updateTest(t, m, ActionMsg{"ghost.new"})
	m = typeTest(m, "QN031EXPLICIT")
	for _, action := range []string{"clipboard.copy", "note.export"} {
		path := capture
		if action == "note.export" {
			path = filepath.Join(t.TempDir(), "note.md")
		}
		m = updateTest(t, m, ActionMsg{action})
		if action == "note.export" {
			m.fileInput.SetValue(path)
			m = updateTest(t, m, keyMessage("enter"))
		}
		m = updateTest(t, m, keyMessage("enter"))
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("default Cancel invoked external writer")
		}
		if action == "clipboard.copy" {
			m = updateTest(t, m, ActionMsg{action})
		} else {
			m = updateTest(t, m, keyMessage("enter"))
		}
		m = updateTest(t, m, keyMessage("tab"))
		m = updateTest(t, m, keyMessage("enter"))
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "QN031EXPLICIT" {
			t.Fatal("confirmed output mismatch", err)
		}
		if !m.Note.IsGhost || m.Note.ID != 0 {
			t.Fatal("output persisted ghost as note")
		}
	}
	var count int
	if err := a.db.QueryRow("SELECT count(*) FROM notes WHERE content='QN031EXPLICIT'").Scan(&count); err != nil || count != 0 {
		t.Fatal("output inserted ghost")
	}
}
func TestQN031StaleOutputConfirmation(t *testing.T) {
	for _, action := range []string{"clipboard.copy", "note.export"} {
		m, _, _, _, _ := asyncModel(t)
		m = updateTest(t, m, ActionMsg{"ghost.new"})
		m = typeTest(m, "old")
		m = updateTest(t, m, ActionMsg{action})
		path := filepath.Join(t.TempDir(), "stale.md")
		if action == "note.export" {
			m.fileInput.SetValue(path)
			m = updateTest(t, m, keyMessage("enter"))
		}
		m.localVersion++
		m.buffer = stringToBuffer("newer")
		m = updateTest(t, m, keyMessage("tab"))
		m, cmd := rawUpdate(m, keyMessage("enter"))
		if cmd != nil {
			t.Fatal("stale confirmation launched output")
		}
		if m.buffer.Text() != "newer" {
			t.Fatal("stale output changed content")
		}
	}
}
func TestQN031TrashRetentionAndIndex(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	first.Content = "retained #qn031tag"
	var err error
	first, err = a.Save(first)
	if err != nil {
		t.Fatal(err)
	}
	m.notes[0] = first
	m.drafts = nil
	m.index = 0
	m.activateNote()
	m = ghostChoice(t, m, 2)
	n, err := a.GetNote(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	at, err := ParseLifecycleTime(n.DeletedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Maintain(at.Add(72*time.Hour), nil); err != nil {
		t.Fatal(err)
	}
	retained, err := a.GetNote(first.ID)
	if err != nil || retained.Content != first.Content || len(retained.Tags) != 1 {
		t.Fatal("original not retained through exact 72h boundary", err)
	}
	restored, err := a.RestoreSnapshotCAS(n.ID, n.Revision, RestoreWithoutTimer, at.Add(72*time.Hour))
	if err != nil || restored.ID != first.ID || restored.Content != first.Content {
		t.Fatal("original cannot restore same identity", err)
	}
	var count int
	if err = a.db.QueryRow("SELECT count(*) FROM notes_fts WHERE notes_fts MATCH 'retained'").Scan(&count); err != nil || count != 1 {
		t.Fatal("FTS original lost")
	}
}
func TestQN031KeyboardAndLayouts(t *testing.T) {
	for _, profile := range []string{"default", "legacy"} {
		m, _, _, _, _ := asyncModel(t)
		m.keymap = defaultKeymap(profile)
		if err := m.keymap.Validate(); err != nil {
			t.Fatal(err)
		}
		m = updateTest(t, m, keyMessage("alt+g"))
		if !m.Note.IsGhost {
			t.Fatal("keyboard New Ghost unavailable")
		}
		m = updateTest(t, m, keyMessage("ctrl+g"))
		m = updateTest(t, m, keyMessage("esc"))
		m = updateTest(t, m, ActionMsg{"note.new"})
		m = typeTest(m, "source")
		m = updateTest(t, m, keyMessage("ctrl+g"))
		for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
			m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			for i := 0; i < 3; i++ {
				v := m.View()
				if lipgloss.Width(v) > size[0] || lipgloss.Height(v) > size[1] {
					t.Fatal("ghost dialog overflow")
				}
				if size[0] >= 25 && (!strings.Contains(v, "Cancel") || !strings.Contains(v, []string{"Cancel", "Duplicate", "Trash"}[i])) {
					t.Fatal("ghost choice invisible", size, i, v)
				}
				t.Logf("%s %dx%d choice%d:\n%s", profile, size[0], size[1], i, v)
				m = updateTest(t, m, keyMessage("tab"))
			}
		}
		m = updateTest(t, m, keyMessage("esc"))
		if m.state != stateTyping {
			t.Fatal("Escape not reachable")
		}
		m.keymap.Bindings["ghost.new"] = []string{"alt+j"}
		if err := m.keymap.Validate(); err != nil {
			t.Fatal(err)
		}
		m = updateTest(t, m, keyMessage("alt+j"))
		if !m.Note.IsGhost {
			t.Fatal("custom ghost binding did not execute")
		}
	}
}
