package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestQN032ReviewScrollBounds(t *testing.T) {
	for _, profile := range []string{"default", "legacy", "custom"} {
		for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
			t.Run(fmt.Sprintf("%s/%dx%d", profile, size[0], size[1]), func(t *testing.T) {
				m, a, _, n := trashModel(t)
				m.keymap = defaultKeymap(profile)
				if profile == "custom" {
					m.keymap = defaultKeymap("default")
					m.keymap.Bindings["commands.open"] = []string{"alt+c"}
				}
				var content strings.Builder
				for i := 0; i < 40; i++ {
					fmt.Fprintf(&content, "%02d 界面 é 👩‍💻 %s\n", i, strings.Repeat("wrapped界", 8))
				}
				content.WriteString("FINAL PREVIEW LINE")
				if _, err := a.db.Exec("UPDATE notes SET content=?,revision=revision+1 WHERE id=?", content.String(), n.ID); err != nil {
					t.Fatal(err)
				}
				m = drainTestCmd(t, m, m.requestOpen(n.ID, true, nil, true))
				m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.undoStack = []textState{{content: "prior history"}}
				before := m.EditorDraft.Clone()
				for i := 0; i < 25; i++ {
					m = updateTest(t, m, keyMessage("pgdown"))
				}
				atEnd := m.View()
				endOffset := m.viewport.YOffset
				m = updateTest(t, m, keyMessage("pgdown"))
				if m.viewport.YOffset != endOffset {
					t.Fatalf("offset grows past EOF: %d -> %d", endOffset, m.viewport.YOffset)
				}
				if size[0] >= 50 {
					if !strings.Contains(atEnd, "FINAL PREVIEW LINE") {
						t.Fatal("last content not visible", atEnd)
					}
					m = updateTest(t, m, keyMessage("up"))
					if m.viewport.YOffset != endOffset-1 || m.View() == atEnd {
						t.Fatal("first Up did not reverse from EOF")
					}
					m = updateTest(t, m, keyMessage("pgdown"))
					m = updateTest(t, m, keyMessage("pgup"))
					if m.viewport.YOffset >= endOffset || m.View() == atEnd {
						t.Fatal("first PageUp did not reverse from EOF")
					}
				} else if endOffset != 0 {
					t.Fatal("no visible body rows must have zero offset", endOffset)
				}
				if lipgloss.Width(m.View()) > size[0] || lipgloss.Height(m.View()) > size[1] {
					t.Fatal("preview overflow")
				}
				if before.buffer.Text() != m.buffer.Text() || before.needsSave != m.needsSave || before.localVersion != m.localVersion || !reflect.DeepEqual(before.undoStack, m.undoStack) {
					t.Fatal("scroll changed content/history")
				}
				m = updateTest(t, m, keyMessage(strings.ToLower(m.shortcut("commands.open"))))
				m = updateTest(t, m, tea.WindowSizeMsg{Width: 1000, Height: 1000})
				m = updateTest(t, m, keyMessage("esc"))
				if m.viewport.YOffset != 0 {
					t.Fatal("resize did not clamp short viewport", m.viewport.YOffset)
				}
				for i := 0; i < 3; i++ {
					m = updateTest(t, m, keyMessage("pgdown"))
				}
				if m.viewport.YOffset != 0 {
					t.Fatal("fully visible content scrolled")
				}
				m.viewport.YOffset = 99999
				_ = m.View()
				if m.viewport.YOffset != 99999 {
					t.Fatal("View mutated model")
				}
			})
		}
	}
}

func TestQN032ReviewDeleteCleanup(t *testing.T) {
	for _, kind := range []string{"clean", "maintenance-during-load", "dirty-before-ack", "version-before-ack", "dirty-during-load", "delete-error", "load-error"} {
		t.Run(kind, func(t *testing.T) {
			m, a, _, n := trashModel(t)
			unrelated := EditorDraft{Note: Note{ID: 9001}, buffer: stringToBuffer("unrelated"), undoStack: []textState{{content: "unrelated history"}}, viewportTop: new(int)}
			m.drafts[9001] = unrelated.Clone()
			m.undoStack = []textState{{content: "deleted note history"}}
			m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
			m = updateTest(t, m, keyMessage("tab"))
			m, cmd := rawUpdate(m, keyMessage("enter"))
			if kind == "delete-error" {
				if _, err := a.db.Exec("CREATE TRIGGER fail_delete BEFORE DELETE ON notes BEGIN SELECT RAISE(ABORT,'fixture error'); END"); err != nil {
					t.Fatal(err)
				}
			}
			result := cmd().(lifecycleResult)
			edit := func() {
				m.buffer = stringToBuffer("newer recoverable content")
				m.localVersion++
				m.versionSeq = m.localVersion
				m.needsSave = true
			}
			if kind == "dirty-before-ack" {
				edit()
			}
			if kind == "version-before-ack" {
				m.localVersion++
				m.versionSeq = m.localVersion
			}
			m, cmd = rawUpdate(m, result)
			switch kind {
			case "clean", "maintenance-during-load", "dirty-during-load", "load-error":
				if cmd == nil {
					t.Fatal("missing followup load")
				}
				load := cmd().(loadResult)
				if kind == "maintenance-during-load" {
					m = updateTest(t, m, maintenanceResult{Generation: m.maintenanceGeneration})
				}
				if kind == "dirty-during-load" {
					edit()
				}
				if kind == "load-error" {
					load.Err = errors.New("fixture read failure")
				}
				m = updateTest(t, m, load)
			default:
				m = drainTestCmd(t, m, cmd)
			}
			if kind == "clean" || kind == "maintenance-during-load" {
				if _, ok := m.drafts[n.ID]; ok {
					t.Fatal("successful followup load re-created deleted clean draft")
				}
				if m.activeID() == n.ID || m.state != stateTrash {
					t.Fatal("did not leave deleted identity")
				}
			} else if kind == "dirty-before-ack" || kind == "dirty-during-load" {
				if m.buffer.Text() != "newer recoverable content" || !m.needsSave || len(m.undoStack) != 1 {
					t.Fatal("newer draft/history lost")
				}
			} else if m.buffer.Text() != n.Content || len(m.undoStack) != 1 {
				t.Fatal("source/history lost on version change or error")
			}
			if !reflect.DeepEqual(unrelated, m.drafts[9001]) {
				t.Fatal("unrelated draft/history changed")
			}
			_, err := a.GetNote(n.ID)
			if kind == "delete-error" {
				if err != nil {
					t.Fatal("failed deletion lost row", err)
				}
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatal("fixture deletion did not commit", err)
			}
		})
	}
}

func TestQN032ReviewDeleteCleanupRefetch(t *testing.T) {
	m, a, _, n := trashModel(t)
	next, err := a.Save(Note{Content: "next original"})
	if err != nil {
		t.Fatal(err)
	}
	next, err = a.TrashSnapshotCAS(next.ID, next.Revision, DeleteManual, lifecycleNow())
	if err != nil {
		t.Fatal(err)
	}
	m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
	m = updateTest(t, m, keyMessage("tab"))
	m, cmd := rawUpdate(m, keyMessage("enter"))
	m, cmd = rawUpdate(m, cmd())
	held := cmd().(loadResult)
	if held.Note.ID != next.ID {
		t.Fatal("wrong followup target", held.Note)
	}
	if _, err = a.db.Exec("UPDATE notes SET content='newer full snapshot',revision=revision+1 WHERE id=?", next.ID); err != nil {
		t.Fatal(err)
	}
	next, err = a.GetNote(next.ID)
	if err != nil {
		t.Fatal(err)
	}
	summary := next
	summary.Content = ""
	m.searchResults = []Note{summary}
	m = updateTest(t, m, maintenanceResult{Generation: m.maintenanceGeneration})
	m, cmd = rawUpdate(m, held)
	if cmd == nil {
		t.Fatal("stale target did not refetch")
	}
	m = drainTestCmd(t, m, cmd)
	if m.activeID() != next.ID || m.Note.Revision != next.Revision || m.buffer.Text() != next.Content {
		t.Fatal("refetch mixed content/revision")
	}
	if _, ok := m.drafts[n.ID]; ok {
		t.Fatal("refetch lost cleanup ownership")
	}
}

func TestQN032ReviewDeleteCleanupRetainsNewerRevision(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(fmt.Sprint(dirty), func(t *testing.T) {
			m, _, _, n := trashModel(t)
			m.undoStack = []textState{{content: "source history"}}
			m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
			m = updateTest(t, m, keyMessage("tab"))
			m, cmd := rawUpdate(m, keyMessage("enter"))
			m, cmd = rawUpdate(m, cmd())
			held := cmd().(loadResult)
			// A newer full observation or dirty snapshot must not be forgotten even
			// when it retains the same local version as the departing source.
			m.buffer = stringToBuffer("newer source snapshot")
			m.needsSave = dirty
			if !dirty {
				m.Note.Revision++
			}
			m = updateTest(t, m, held)
			d, ok := m.drafts[n.ID]
			if !ok || d.buffer.Text() != "newer source snapshot" || d.needsSave != dirty || len(d.undoStack) != 1 {
				t.Fatal("newer revision/dirty draft forgotten", d)
			}
		})
	}
}
