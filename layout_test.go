package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"strings"
	"testing"
)

func TestQN016T25EveryOverlayFitsSizes(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.buffer = stringToBuffer("你好 e\u0301 👩‍💻\tlong line " + strings.Repeat("x", 80))
	m.conflictRemote = Note{Content: "remote"}
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
		for _, state := range []appState{stateTyping, stateTrash, stateSearch, stateHelp, statePromptFile, statePipe, stateConflict, stateCommands} {
			m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.state = state
			m.commandReturn = stateTyping
			m.viewport.SetContent(m.generateHelpText())
			before := m.buffer.Text()
			view := m.View()
			if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
				t.Fatalf("size %v state %d overflow %d×%d", size, state, lipgloss.Width(view), lipgloss.Height(view))
			}
			if before != m.buffer.Text() {
				t.Fatal("render mutated buffer")
			}
			if size[0] >= 25 && state != stateTyping && state != stateTrash && !strings.Contains(view, "Esc") {
				t.Fatalf("cancel missing %v %d: %q", size, state, view)
			}
		}
	}
}
func TestQN016SharedMouseCellsAndVerticalMovement(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = updateTest(t, m, tea.WindowSizeMsg{Width: 25, Height: 12})
	m.buffer = stringToBuffer("你e\u0301👩‍💻\tX\nabcdefghij")
	m.cursorCol = 0
	m.placeMouse(2, 0)
	if m.cursorCol != 1 {
		t.Fatalf("wide cell mapped to wrong rune %d", m.cursorCol)
	}
	m.placeMouse(3, 0)
	if m.cursorCol != 3 {
		t.Fatal("combining cell split")
	}
	m.placeMouse(4, 0)
	if m.cursorCol != 3 {
		t.Fatal("emoji cluster split")
	}
	m.cursorRow, m.cursorCol = 0, 3
	m.virtualPhysCol = 3
	m.moveVisual(1)
	if m.cursorRow != 1 || m.cursorCol != 3 {
		t.Fatalf("preferred visual cell lost %d/%d", m.cursorRow, m.cursorCol)
	}
}
func TestQN016WrapNeverSplitsGraphemesOrDropsWhitespace(t *testing.T) {
	text := "你 e\u0301 👩‍💻  x"
	line := []rune(text)
	for width := 1; width < 12; width++ {
		rows := lineRows(line, width)
		last := 0
		for _, row := range rows {
			if row.Start != last || !isGraphemeBoundary(line, row.Start) || !isGraphemeBoundary(line, row.End) {
				t.Fatalf("bad source boundary width %d %+v", width, row)
			}
			last = row.End
			if row.Width > width {
				t.Fatal("row overflow")
			}
		}
		if last != len(line) {
			t.Fatal("lost source text")
		}
	}
}
func TestQN016RenderingNeutralizesControlsWithoutChangingData(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.buffer = stringToBuffer("hello\x1b[2J\x07world")
	m.cachedHighlight = m.buffer.Text()
	m = updateTest(t, m, tea.WindowSizeMsg{Width: 25, Height: 5})
	before := m.buffer.Text()
	view := m.View()
	if strings.Contains(view, "\x1b[2J") || strings.Contains(view, "\x07") || m.buffer.Text() != before {
		t.Fatal("unsafe display or changed stored text")
	}
}
func TestQN016ValidViewportSurvivesNavigation(t *testing.T) {
	m, _, _, first, second := asyncModel(t)
	m = updateTest(t, m, tea.WindowSizeMsg{Width: 80, Height: 4})
	m.buffer = stringToBuffer(strings.Repeat("line\n", 20))
	m.cursorRow, m.cursorCol = 6, 1
	*m.viewportTop = 4
	m.needsSave = true
	m = updateTest(t, m, keyMessage("alt+right"))
	if m.activeID() != second.ID {
		t.Fatal("B not opened")
	}
	m = updateTest(t, m, keyMessage("alt+left"))
	if m.activeID() != first.ID || *m.viewportTop != 4 || m.cursorRow != 6 || m.cursorCol != 1 {
		t.Fatalf("valid viewport lost top=%d cursor=%d/%d", *m.viewportTop, m.cursorRow, m.cursorCol)
	}
}
func FuzzQN016SourceRows(f *testing.F) {
	for _, s := range []string{"", "你 e\u0301 👩‍💻", "\tblank\n\n", "\x1b[2J", "\u0301"} {
		f.Add(s, uint8(25))
	}
	f.Fuzz(func(t *testing.T, text string, n uint8) {
		if len(text) > 1024 {
			return
		}
		b := stringToBuffer(text)
		width := int(n%80) + 1
		for _, line := range b {
			rows := lineRows(line, width)
			end := 0
			for _, row := range rows {
				if row.Start != end || !isGraphemeBoundary(line, row.Start) || !isGraphemeBoundary(line, row.End) || row.Width > width {
					t.Fatal("invalid source/wrap mapping")
				}
				end = row.End
			}
			if end != len(line) {
				t.Fatal("lost source range")
			}
		}
	})
}
