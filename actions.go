package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
)

type ActionContext struct {
	State appState
	Note  Note
	Dirty bool
}
type Action struct {
	ID, Title, Description, Category string
	DefaultKeys                      []string
	States                           []appState
	Dangerous                        bool
	Available                        func(ActionContext) bool
	Enabled                          func(ActionContext) (bool, string)
	Execute                          func() tea.Msg
}
type ActionMsg struct{ ID string }

func keyMessage(key string) tea.KeyMsg {
	switch key {
	case "ctrl+n":
		return tea.KeyMsg{Type: tea.KeyCtrlN}
	case "ctrl+t":
		return tea.KeyMsg{Type: tea.KeyCtrlT}
	case "ctrl+f":
		return tea.KeyMsg{Type: tea.KeyCtrlF}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+s":
		return tea.KeyMsg{Type: tea.KeyCtrlS}
	case "ctrl+g":
		return tea.KeyMsg{Type: tea.KeyCtrlG}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "ctrl+z":
		return tea.KeyMsg{Type: tea.KeyCtrlZ}
	case "ctrl+y":
		return tea.KeyMsg{Type: tea.KeyCtrlY}
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "ctrl+x":
		return tea.KeyMsg{Type: tea.KeyCtrlX}
	case "ctrl+o":
		return tea.KeyMsg{Type: tea.KeyCtrlO}
	case "ctrl+a":
		return tea.KeyMsg{Type: tea.KeyCtrlA}
	case "ctrl+e":
		return tea.KeyMsg{Type: tea.KeyCtrlE}
	case "ctrl+j":
		return tea.KeyMsg{Type: tea.KeyCtrlJ}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case "ctrl+left":
		return tea.KeyMsg{Type: tea.KeyCtrlLeft}
	case "ctrl+right":
		return tea.KeyMsg{Type: tea.KeyCtrlRight}
	case "f1":
		return tea.KeyMsg{Type: tea.KeyF1}
	case "f2":
		return tea.KeyMsg{Type: tea.KeyF2}
	case "alt+left":
		return tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	case "alt+right":
		return tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	case "alt+enter":
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}
	}
	if strings.HasPrefix(key, "ctrl+") && len(key) == 6 && key[5] >= 'a' && key[5] <= 'z' {
		return tea.KeyMsg{Type: tea.KeyType(key[5] - 'a' + 1)}
	}
	if strings.HasPrefix(key, "alt+") {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.TrimPrefix(key, "alt+")), Alt: true}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
}
func makeAction(id, title, description, category string, states []appState, keys ...string) Action {
	a := Action{ID: id, Title: title, Description: description, Category: category, States: states, DefaultKeys: keys}
	a.Available = func(c ActionContext) bool {
		for _, s := range states {
			if c.State == s {
				return true
			}
		}
		return false
	}
	a.Enabled = func(c ActionContext) (bool, string) { return true, "" }
	if len(keys) == 0 {
		a.Execute = func() tea.Msg { return ActionMsg{id} }
		return a
	}
	canonical := keys[0]
	a.Execute = func() tea.Msg { return keyMessage(canonical) }
	return a
}
func actionRegistry() []Action {
	edit := []appState{stateTyping}
	nav := []appState{stateTyping, stateTrash}
	search := []appState{stateSearch}
	conflict := []appState{stateConflict}
	a := []Action{
		makeAction("confirmation.cancel", "Cancel confirmation", "Keep the original draft and action unchanged", "Confirmation", []appState{stateConfirm}, "esc", "ctrl+c"),
		makeAction("confirmation.choose", "Choose confirmation", "Change the default Cancel selection", "Confirmation", []appState{stateConfirm}, "tab", "left", "right"),
		makeAction("confirmation.accept", "Accept selected choice", "Run only the explicitly selected confirmation", "Confirmation", []appState{stateConfirm}, "enter"),
		makeAction("commands.open", "Commands", "Open the basic command menu (fuzzy palette is QN-023)", "Application", []appState{stateTyping, stateTrash, stateHelp}),
		makeAction("keyboard.profile", "Switch keyboard profile", "Switch default/legacy profile and save keyboard.json", "Settings", nav),
		makeAction("note.new", "New note", "Create a titleless scratch draft", "Note", edit, "ctrl+n"),
		makeAction("note.trash", "Move to Trash", "Keep a persisted note recoverable for 72 hours", "Note", edit, "ctrl+d"),
		makeAction("note.ghost", "Toggle ghost mode", "Change memory-only mode; persisted conversion needs confirmation", "Note", edit, "ctrl+g"),
		makeAction("note.restore", "Restore note", "Restore this Trash entry", "Note", []appState{stateTrash}, "ctrl+r"),
		makeAction("note.promote", "Promote note", "Save and move the note to the recent front", "Note", edit, "ctrl+u"),
		makeAction("note.export", "Export note", "Write the draft to a Markdown file", "Files", edit, "ctrl+s"),
		makeAction("clipboard.copy", "Copy note", "Copy the complete draft on explicit request", "Clipboard", edit, "alt+c"),
		makeAction("shell.pipe", "Filter through command", "Run an explicitly entered local shell filter", "Shell", edit, "alt+x"),
		makeAction("search.open", "Find notes", "Search active notes or Trash by context", "Navigation", nav, "ctrl+f"),
		makeAction("trash.open", "Open Trash", "Browse recoverable deleted notes", "Navigation", edit, "ctrl+t"),
		makeAction("trash.close", "Return to notes", "Leave read-only Trash", "Navigation", []appState{stateTrash}, "esc", "ctrl+t"),
		makeAction("note.next", "Next note", "Open the next stable note identity", "Navigation", nav, "alt+right"),
		makeAction("note.previous", "Previous note", "Open the previous stable note identity", "Navigation", nav, "alt+left"),
		makeAction("help.open", "Help and shortcuts", "Show commands from the action registry", "Help", []appState{stateTyping, stateTrash, stateSearch, statePromptFile, statePipe}, "f1"),
		makeAction("help.close", "Close help", "Restore the previous editor or overlay", "Help", []appState{stateHelp}, "esc", "ctrl+c", "enter", "f1"),
		makeAction("view.lines", "Toggle line numbers", "Change the editor gutter", "View", edit, "f2"),
		makeAction("app.exit", "Exit safely", "Wait for saves and preserve failed drafts", "Application", edit, "esc", "ctrl+c"),
		makeAction("edit.undo", "Undo", "Undo only this draft's transaction", "Editing", edit, "ctrl+z", "alt+z"),
		makeAction("edit.redo", "Redo", "Redo only this draft's transaction", "Editing", edit, "ctrl+y", "alt+y"),
		makeAction("edit.deleteLine", "Delete line", "Remove this logical line as one undo transaction", "Editing", edit, "ctrl+k"),
		makeAction("edit.bold", "Bold", "Toggle Markdown bold", "Formatting", edit, "ctrl+b"),
		makeAction("edit.italic", "Italic", "Toggle Markdown italic", "Formatting", edit, "alt+i"),
		makeAction("edit.checkbox", "Checkbox or strikethrough", "Toggle task/strike formatting", "Formatting", edit, "ctrl+x"),
		makeAction("edit.mathMode", "Math mode", "Toggle existing math: behavior", "Modes", edit, "alt+m"),
		makeAction("edit.codeMode", "Code mode", "Toggle existing code: behavior", "Modes", edit, "alt+v"),
		makeAction("edit.openBelow", "Open line below", "Insert a line without splitting this line", "Editing", edit, "ctrl+o", "alt+enter"),
		makeAction("edit.indent", "Indent", "Indent with four spaces", "Editing", edit, "tab"),
		makeAction("edit.unindent", "Unindent", "Remove one indent level", "Editing", edit, "shift+tab"),
		makeAction("edit.backspace", "Delete previous character", "Delete an entire grapheme or join lines", "Editing", edit, "backspace"),
		makeAction("edit.delete", "Delete next character", "Delete an entire grapheme or join lines", "Editing", edit, "delete"),
		makeAction("edit.newline", "New line", "Split a line with existing list/code behavior", "Editing", edit, "enter"),
		makeAction("edit.codePair", "Code delimiter", "Pair inline/fenced backticks", "Formatting", edit, "`"),
		makeAction("edit.evaluate", "Evaluate expression", "Apply existing inline math behavior", "Modes", edit, "="),
		makeAction("search.cancel", "Cancel search", "Keep the underlying draft unchanged", "Navigation", search, "esc", "ctrl+c"),
		makeAction("search.openResult", "Open result", "Open the selected stable note ID", "Navigation", search, "enter"),
		makeAction("search.up", "Previous result", "Move the search selection up", "Navigation", search, "up", "ctrl+k"),
		makeAction("search.down", "Next result", "Move the search selection down", "Navigation", search, "down", "ctrl+j"),
		makeAction("conflict.copy", "Save conflicting draft as copy", "Keep both versions with independent identities", "Recovery", conflict, "c"),
		makeAction("conflict.reload", "Reload stored note", "Confirm discarding the local conflicting draft", "Recovery", conflict, "r"),
		makeAction("conflict.compare", "Compare versions", "Inspect local and stored lines", "Recovery", conflict, "i"),
		makeAction("conflict.edit", "Keep editing", "Keep the draft and persistent error", "Recovery", conflict, "esc"),
		makeAction("file.cancel", "Cancel export", "Return without changing the draft", "Files", []appState{statePromptFile}, "esc", "ctrl+c"),
		makeAction("file.confirm", "Export to entered path", "Write the draft to the selected file", "Files", []appState{statePromptFile}, "enter"),
		makeAction("pipe.cancel", "Cancel shell prompt", "Return without running a command", "Shell", []appState{statePipe}, "esc", "ctrl+c"),
		makeAction("pipe.confirm", "Run entered filter", "Execute the explicitly entered command", "Shell", []appState{statePipe}, "enter"),
	}
	for _, key := range []string{"left", "right", "up", "down", "pgup", "pgdown", "home", "end", "ctrl+left", "ctrl+right"} {
		a = append(a, makeAction("cursor."+key, "Move "+key, "Move cursor using source coordinates", "Cursor", edit, key))
	}
	a = append(a, makeAction("cursor.start", "Line start", "Move to first non-space or start", "Cursor", edit, "ctrl+a"), makeAction("cursor.smartEnd", "Line end", "Move to wrapped/logical end", "Cursor", edit, "ctrl+e"))

	for _, planned := range []struct {
		id, title, description, category, task string
		states                                 []appState
	}{
		{"ghost.new", "New ghost draft", "Create a memory-only draft", "Note", "QN-031", edit},
		{"ttl.set", "Set expiration", "Choose a durable expiration preset", "Lifecycle", "QN-029", edit},
		{"note.pin", "Pin or unpin", "Control automatic expiration", "Lifecycle", "QN-030", edit},
		{"trash.deleteForever", "Delete forever", "Confirm permanently deleting this Trash entry", "Note", "QN-032", []appState{stateTrash}},
		{"view.sidebar", "Toggle sidebar", "Show recent notes and tags", "View", "QN-024", nav},
		{"theme.pick", "Change theme", "Preview and choose a validated theme", "Settings", "QN-033", nav},
		{"view.motion", "Reduce motion", "Control optional UI transitions", "Settings", "QN-037", nav},
		{"notes.switcher", "Quick switcher", "Find and open a recent stable note ID", "Navigation", "QN-022", nav},
	} {
		future := makeAction(planned.id, planned.title, planned.description, planned.category, planned.states)
		task := planned.task
		future.Enabled = func(ActionContext) (bool, string) { return false, "Planned in " + task }
		a = append(a, future)
	}
	for i := range a {
		if a[i].ID == "note.restore" {
			a[i].Enabled = func(c ActionContext) (bool, string) {
				if c.Note.ID <= 0 {
					return false, "Trash is empty"
				}
				return true, ""
			}
		}
		if a[i].ID == "note.ghost" || a[i].ID == "conflict.reload" {
			a[i].Dangerous = true
		}
	}
	return a
}
func findAction(id string) (Action, bool) {
	for _, a := range actionRegistry() {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}
func (m model) actionContext() ActionContext { return ActionContext{m.state, m.Note, m.needsSave} }
func actionForKey(key string, c ActionContext) (Action, bool) {
	for _, a := range actionRegistry() {
		if a.Available(c) {
			for _, k := range a.DefaultKeys {
				if key == k {
					return a, true
				}
			}
		}
	}
	return Action{}, false
}
func (m model) actionHelp(c ActionContext) string {
	var b strings.Builder
	category := ""
	for _, a := range actionRegistry() {
		if !a.Available(c) {
			continue
		}
		if a.Category != category {
			fmt.Fprintf(&b, "\n%s\n", a.Category)
			category = a.Category
		}
		enabled, reason := a.Enabled(c)
		suffix := ""
		if !enabled {
			suffix = " — disabled: " + reason
		}
		fmt.Fprintf(&b, "%s  %s%s\n", strings.Join(m.keyboard().keys(a), " / "), a.Title, suffix)
	}
	return b.String()
}
