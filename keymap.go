package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type Keymap struct {
	Version  int                 `json:"version"`
	Profile  string              `json:"profile"`
	Bindings map[string][]string `json:"bindings,omitempty"`
}

func defaultKeymap(profile string) Keymap {
	return Keymap{Version: 1, Profile: profile, Bindings: map[string][]string{}}
}
func (k Keymap) keys(a Action) []string {
	if keys, ok := k.Bindings[a.ID]; ok {
		return keys
	}
	if a.ID == "commands.open" {
		if k.Profile == "legacy" {
			return []string{"alt+p"}
		}
		return []string{"ctrl+k"}
	}
	if a.ID == "edit.deleteLine" && k.Profile != "legacy" {
		return []string{"alt+k"}
	}
	return a.DefaultKeys
}
func (k Keymap) Validate() error {
	if k.Version != 1 {
		return fmt.Errorf("keyboard config version %d is unsupported", k.Version)
	}
	if k.Profile != "default" && k.Profile != "legacy" {
		return fmt.Errorf("unknown keyboard profile %q", k.Profile)
	}
	for id, keys := range k.Bindings {
		a, ok := findAction(id)
		if !ok {
			return fmt.Errorf("unknown action %q", id)
		}
		if strings.HasPrefix(id, "confirmation.") {
			return fmt.Errorf("confirmation keys are fixed modal controls")
		}
		if id == "edit.codePair" || id == "edit.evaluate" {
			return fmt.Errorf("literal formatting keys cannot be rebound")
		}
		if id == "search.cancel" || id == "help.close" || id == "conflict.edit" || id == "file.cancel" || id == "pipe.cancel" {
			hasEsc := false
			for _, key := range keys {
				hasEsc = hasEsc || key == "esc"
			}
			if !hasEsc {
				return fmt.Errorf("modal cancel action %s must retain Esc", id)
			}
		}
		if len(keys) == 0 {
			return fmt.Errorf("action %s requires a keyboard binding", id)
		}
		for _, key := range keys {
			msg := keyMessage(key)
			if key == "" || key != strings.ToLower(key) || msg.String() != key || (msg.Type == tea.KeyRunes && len(msg.Runes) != 1) {
				return fmt.Errorf("invalid key %q", key)
			}
			if len([]rune(key)) == 1 && containsState(a.States, stateTyping) {
				return fmt.Errorf("plain typing character %q cannot bind an editor command", key)
			}
		}
	}
	for state := stateTyping; state <= stateConfirm; state++ {
		seen := map[string]string{}
		for _, a := range actionRegistry() {
			if !a.Available(ActionContext{State: state}) {
				continue
			}
			for _, key := range k.keys(a) {
				if prev, ok := seen[key]; ok {
					return fmt.Errorf("key %s collides between %s and %s in context %d", key, prev, a.ID, state)
				}
				seen[key] = a.ID
			}
		}
	}
	return nil
}
func containsState(states []appState, state appState) bool {
	for _, s := range states {
		if s == state {
			return true
		}
	}
	return false
}
func (m model) keyboard() Keymap {
	if m.keymap.Version == 0 {
		return defaultKeymap("default")
	}
	return m.keymap
}
func (m model) boundAction(key string, c ActionContext) (Action, bool) {
	for _, a := range actionRegistry() {
		if a.Available(c) {
			for _, binding := range m.keyboard().keys(a) {
				if key == binding {
					return a, true
				}
			}
		}
	}
	return Action{}, false
}
func keyboardPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "quicknote-tui", "keyboard.json")
}
func writeKeymap(k Keymap) error {
	if err := k.Validate(); err != nil {
		return err
	}
	path := keyboardPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".keyboard-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func loadKeymap() (Keymap, string) {
	k := defaultKeymap("default")
	data, err := os.ReadFile(keyboardPath())
	if os.IsNotExist(err) {
		err = writeKeymap(k)
		notice := "v2: Ctrl+K Commands; Alt+K delete line. Legacy profile is in Commands."
		if err != nil {
			notice += " Config not saved: " + err.Error()
		}
		return k, notice
	}
	if err != nil {
		return k, "Keyboard config error: " + err.Error()
	}
	if err = json.Unmarshal(data, &k); err == nil {
		err = k.Validate()
	}
	if err != nil {
		return defaultKeymap("default"), "Keyboard config error: " + err.Error()
	}
	return k, ""
}

type keymapResult struct {
	Generation uint64
	Keymap     Keymap
	Err        error
}

func (m *model) switchKeyboard() tea.Cmd {
	k := defaultKeymap("legacy")
	if m.keyboard().Profile == "legacy" {
		k = defaultKeymap("default")
	}
	m.keyboardGeneration++
	generation := m.keyboardGeneration
	return func() tea.Msg { return keymapResult{generation, k, writeKeymap(k)} }
}
func (m model) commandActions() []Action {
	context := m.actionContext()
	context.State = m.commandReturn
	out := []Action{}
	for _, a := range actionRegistry() {
		if a.Available(context) && a.Category != "Cursor" && a.ID != "commands.open" {
			out = append(out, a)
		}
	}
	return out
}
func (m *model) openCommands() {
	m.commandReturn = m.state
	m.pushOverlay(stateCommands)
	m.commandIndex = 0
	m.openGeneration++
	m.loadPending = false
	m.pendingAction = nil
	m.pendingOpen = nil
}
func (m model) commandView() string {
	actions := m.commandActions()
	var lines []string
	lines = append(lines, "Commands · "+m.keyboard().Profile, "Esc Cancel · ↑↓ Choose · Enter Run")
	height := max(1, m.viewport.Height)
	visible := max(1, height-2)
	start := max(0, m.commandIndex-visible+1)
	for i := start; i < min(len(actions), start+visible); i++ {
		a := actions[i]
		prefix := "  "
		if i == m.commandIndex {
			prefix = "> "
		}
		enabled, reason := a.Enabled(ActionContext{State: m.commandReturn, Note: m.Note, Dirty: m.needsSave})
		line := prefix + a.Title + " [" + strings.Join(m.keyboard().keys(a), "/") + "]"
		if !enabled {
			line += " — " + reason
		}
		lines = append(lines, line)
	}
	return m.panel(lines[0], strings.Join(lines[2:], "\n"), lines[1])
}

func (m model) shortcut(id string) string {
	a, ok := findAction(id)
	if !ok {
		return ""
	}
	keys := m.keyboard().keys(a)
	if len(keys) == 0 {
		return ""
	}
	return strings.ToUpper(keys[0])
}
