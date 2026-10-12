package main

import (
	tea "github.com/charmbracelet/bubbletea"
	"os"
	"strings"
	"testing"
)

func TestQN015T16DefaultLegacyAndGeneratedHelp(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.buffer = stringToBuffer("one\ntwo")
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})
	if m.state != stateCommands || m.buffer.Text() != "one\ntwo" {
		t.Fatal("default CtrlK did not open Commands")
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	m = updateTest(t, m, keyMessage("alt+k"))
	if m.buffer.Text() != "two" {
		t.Fatalf("AltK did not delete line: %q", m.buffer.Text())
	}
	m.keymap = defaultKeymap("legacy")
	m.buffer = stringToBuffer("one\ntwo")
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlK})
	if m.buffer.Text() != "two" || m.state != stateTyping {
		t.Fatal("legacy CtrlK changed")
	}
	help := m.generateHelpText()
	if !strings.Contains(help, "ctrl+k  Delete line") {
		t.Fatal("help does not reflect profile")
	}
}
func TestQN015ValidationAndRemapping(t *testing.T) {
	for _, profile := range []string{"default", "legacy"} {
		if err := defaultKeymap(profile).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	k := defaultKeymap("default")
	k.Bindings["note.new"] = []string{"ctrl+s"}
	if k.Validate() == nil {
		t.Fatal("collision accepted")
	}
	k = defaultKeymap("default")
	k.Bindings["note.new"] = []string{"alt+n"}
	if err := k.Validate(); err != nil {
		t.Fatal(err)
	}
	m, _, _, _, _ := asyncModel(t)
	m.keymap = k
	m = updateTest(t, m, keyMessage("ctrl+n"))
	if m.activeID() == 0 {
		t.Fatal("old binding still executed")
	}
	m = updateTest(t, m, keyMessage("alt+n"))
	if m.activeID() != 0 {
		t.Fatal("custom binding not executed")
	}
}
func TestQN015ConfigMigrationAndMalformedFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	k, notice := loadKeymap()
	if k.Profile != "default" || !strings.Contains(notice, "Ctrl+K") {
		t.Fatal("migration notice missing")
	}
	_, notice = loadKeymap()
	if notice != "" {
		t.Fatal("migration notice repeated")
	}
	if err := os.WriteFile(keyboardPath(), []byte(`{"version":999,"profile":"default"}`), 0600); err != nil {
		t.Fatal(err)
	}
	k, notice = loadKeymap()
	if k.Profile != "default" || notice == "" {
		t.Fatal("invalid config silently used")
	}
	k = defaultKeymap("legacy")
	if err := writeKeymap(k); err != nil {
		t.Fatal(err)
	}
	k, notice = loadKeymap()
	if k.Profile != "legacy" || notice != "" {
		t.Fatal("profile persistence failed")
	}
}
func TestQN015CommandsKeyboardOnlyDispatchAndCancel(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	before := m.buffer.Text()
	m = updateTest(t, m, ActionMsg{"commands.open"})
	m = updateTest(t, m, keyMessage("esc"))
	if m.buffer.Text() != before || m.state != stateTyping {
		t.Fatal("menu cancel mutated draft")
	}
	m = updateTest(t, m, ActionMsg{"commands.open"})
	for i, a := range m.commandActions() {
		if a.ID == "view.lines" {
			m.commandIndex = i
			break
		}
	}
	m = updateTest(t, m, keyMessage("enter"))
	if m.showLineNums || m.state != stateTyping {
		t.Fatal("command menu dispatch failed")
	}
}
