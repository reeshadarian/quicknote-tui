package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

// Drive finite worker commands as Bubble Tea would, excluding recurring clocks
// and text-input blink timers. Async-specific tests deliver results manually.
func drainTestCmd(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 200 {
			t.Fatal("command cycle")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case exportResult, exportCheckResult, keymapResult, saveResult, loadResult, searchDebounceMsg, searchResult, highlightMsg, recoveryResult, mutationResult, helpResult, initDataMsg:
			next, cmd := m.Update(msg)
			m = next.(model)
			queue = append(queue, cmd)
		}
	}
	return m
}
func updateTest(t *testing.T, m model, msg tea.Msg) model {
	t.Helper()
	next, cmd := m.Update(msg)
	return drainTestCmd(t, next.(model), cmd)
}
