package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// Requests contain immutable values only. Workers never capture a model,
// editor buffer, session map, or pointer owned by the event loop.
type saveRequest struct {
	Key          int
	Request      uint64
	NoteID       int
	LocalVersion uint64
	Snapshot     Note
}
type saveResult struct {
	Request saveRequest
	Note    Note
	Err     error
}
type loadRequest struct {
	SourceKey     int
	Generation    uint64
	SourceID      int
	SourceVersion uint64
	TargetID      int
	Trash         bool
	Reload        bool
	Match         *Note
}
type loadResult struct {
	Request loadRequest
	Notes   []Note
	Note    Note
	Err     error
}
type searchRequest struct {
	Generation uint64
	Query      string
	Trash      bool
}
type searchDebounceMsg searchRequest
type searchResult struct {
	Request searchRequest
	Notes   []Note
	Err     error
}
type highlightMsg struct {
	Key                           int
	NoteID                        int
	LocalVersion, Generation      uint64
	originalText, highlightedText string
}
type recoveryResult struct {
	Generation uint64
	NoteID     int
	Version    uint64
	Note       Note
	Err        error
	Copy       bool
}
type mutationResult struct {
	Generation uint64
	NoteID     int
	Version    uint64
	Kind       string
	Err        error
}
type helpResult struct {
	Generation    uint64
	Active, Trash int
	Err           error
}

func (m model) activeID() int {
	if !m.draftReady && len(m.notes) > 0 && m.index >= 0 && m.index < len(m.notes) {
		return m.Note.ID
	}
	return m.Note.ID
}
func (m *model) nextRequest() uint64 { m.requestSeq++; return m.requestSeq }
func cloneNote(n Note) Note          { n.Tags = append([]string(nil), n.Tags...); return n }

func (m *model) requestSave() tea.Cmd {
	m.bindDraft()
	if m.activeID() < 0 {
		return nil
	}
	n := cloneNote(m.Note)
	n.Content = bufferToString(m.buffer)
	if n.IsGhost || (n.ID == 0 && n.Content == "") {
		m.Note = n
		m.needsSave = false
		m.saveErr = nil
		return nil
	}
	if _, pending := m.lifecycleRequests[noteKey(n)]; pending {
		return nil
	}
	if _, pending := m.saves[noteKey(n)]; pending {
		return nil
	}
	m.saveErr = nil
	m.stashNote()
	return m.saveSnapshot(n, m.localVersion)
}
func (m *model) saveSnapshot(n Note, version uint64) tea.Cmd {
	if m.saves == nil {
		m.saves = make(map[int]saveRequest)
	}
	r := saveRequest{Key: noteKey(n), Request: m.nextRequest(), NoteID: n.ID, LocalVersion: version, Snapshot: cloneNote(n)}
	m.saves[noteKey(n)] = r
	store := m.store
	return func() tea.Msg { saved, err := store.Save(r.Snapshot); return saveResult{r, saved, err} }
}
func (m *model) receiveSave(msg saveResult) tea.Cmd {
	r := msg.Request
	pending, ok := m.saves[r.Key]
	if !ok || pending.Request != r.Request {
		return nil
	}
	delete(m.saves, r.Key)
	active := m.activeKey() == r.Key
	if active {
		m.stashNote()
	}
	d, ok := m.drafts[r.Key]
	if !ok {
		return nil
	}
	if msg.Err != nil {
		delete(m.lifecycleQueued, r.Key)
		d.needsSave, d.saveErr = true, msg.Err
		m.drafts[r.Key] = d
		if active {
			m.pendingAction = nil
			m.recordSaveError(msg.Err)
			return m.requestConflictRead()
		}
		m.flashMsg = fmt.Sprintf("Note %d save failed; draft kept", r.NoteID)
		return m.finishExit()
	}
	// A successful CAS advances the base even when later edits remain dirty.
	// Never install the response's older content over the live editor.
	if d.Note.Revision <= msg.Note.Revision {
		d.Note = cloneNote(msg.Note)
	}
	deletionGap := false
	if known := m.latestLifecycleSnapshot(d.Note); known.Revision > d.Note.Revision && known.DeletedAt != "" {
		d.Note.DeletedAt, d.Note.DeletedTime, d.Note.DeleteReason = known.DeletedAt, known.DeletedTime, known.DeleteReason
		if combined, ok := withAdjacentDeletion(d.Note, known); ok {
			d.Note = combined
		} else {
			deletionGap = true
		}
	}
	d.needsSave = deletionGap || d.localVersion != r.LocalVersion || d.buffer.Text() != r.Snapshot.Content
	d.saveErr = nil
	if d.Note.DeletedAt != "" && d.needsSave {
		d.saveErr = ErrTrashed
	}
	delete(m.drafts, r.Key)
	m.drafts[msg.Note.ID] = d
	for i := range m.notes {
		if noteKey(m.notes[i]) == r.Key {
			m.notes[i] = cloneNote(d.Note)
		}
	}
	for i := range m.allNotes {
		if noteKey(m.allNotes[i]) == r.Key {
			m.allNotes[i] = cloneNote(d.Note)
		}
	}
	if d.Note.DeletedAt != "" {
		delete(m.lifecycleQueued, r.Key)
		if active {
			m.needsSave, m.saveErr = d.needsSave, d.saveErr
			m.pendingAction = nil
			return m.installLifecycleDraft(msg.Note.ID, d)
		}
		return nil
	}
	if active {
		m.Note = cloneNote(d.Note)
		m.needsSave, m.saveErr = d.needsSave, nil
		if d.needsSave {
			return m.requestSave()
		}
		if intent, ok := m.lifecycleQueued[r.Key]; ok {
			return m.startLifecycle(msg.Note.ID, d, intent)
		}
		m.flashMsg = "Saved"
		if m.pendingAction != nil && m.pendingActionID == r.Key {
			if m.exitPending() {
				return m.finishExit()
			}
			key := *m.pendingAction
			m.pendingAction = nil
			return m.runAction(key)
		}
	} else if d.needsSave {
		n := cloneNote(d.Note)
		n.Content = d.buffer.Text()
		return m.saveSnapshot(n, d.localVersion)
	}
	if intent, ok := m.lifecycleQueued[r.Key]; ok {
		return m.startLifecycle(msg.Note.ID, d, intent)
	}
	return m.finishExit()
}

func (m *model) requestOpen(id int, trash bool, match *Note, reload bool) tea.Cmd {
	m.stashNote()
	m.openGeneration++
	m.loadPending = true
	r := loadRequest{SourceKey: m.activeKey(), Generation: m.openGeneration, SourceID: m.activeID(), SourceVersion: m.localVersion, TargetID: id, Trash: trash, Reload: reload}
	if match != nil {
		n := cloneNote(*match)
		r.Match = &n
	}
	store := m.store
	return func() tea.Msg {
		notes, err := store.ListChecked(r.Trash)
		if err != nil {
			return loadResult{Request: r, Err: err}
		}
		target := r.TargetID
		if target < 0 {
			if len(notes) > 0 {
				target = notes[0].ID
			}
		}
		var n Note
		found := false
		for _, candidate := range notes {
			if candidate.ID == target {
				n, found = candidate, true
				break
			}
		}
		if !found {
			return loadResult{Request: r, Err: fmt.Errorf("note %d: %w", target, ErrNotFound)}
		}
		if target > 0 {
			n, err = store.GetNote(target)
		}
		if err == nil && target > 0 && (n.DeletedAt != "") != r.Trash {
			err = ErrWrongState
		}
		return loadResult{r, notes, n, err}
	}
}
func (m *model) receiveLoad(msg loadResult) tea.Cmd {
	r := msg.Request
	if r.Generation != m.openGeneration {
		return nil
	}
	m.loadPending = false
	if r.SourceKey != m.activeKey() || r.SourceVersion != m.localVersion {
		m.flashMsg = "Open canceled: draft changed"
		return nil
	}
	if msg.Err != nil {
		m.flashMsg = "Load failed: " + msg.Err.Error()
		if r.Reload {
			m.confirmReload = false
			m.conflictReadErr = msg.Err
		}
		return nil
	}
	m.stashNote()
	// A newer summary proves this full read is stale, but does not supply the
	// corresponding content. Refetch both content and revision in one snapshot.
	known := m.latestLifecycleSnapshot(msg.Note)
	if known.Revision > msg.Note.Revision {
		return m.requestOpen(msg.Note.ID, known.DeletedAt != "", r.Match, r.Reload)
	}
	for i := range msg.Notes {
		msg.Notes[i] = m.latestLifecycleSnapshot(msg.Notes[i])
	}
	if r.Reload {
		delete(m.drafts, msg.Note.ID)
	}
	ephemeral := []Note{}
	for _, n := range m.notes {
		if n.ID == 0 && (n.IsGhost || n.Content != "") {
			ephemeral = append(ephemeral, n)
		}
	}
	m.notes = append(ephemeral, msg.Notes...)
	m.allNotes = append([]Note(nil), m.notes...)
	m.index = 0
	for i := range m.notes {
		if m.notes[i].ID == msg.Note.ID {
			m.index = i
			break
		}
	}
	m.notes[m.index] = msg.Note
	m.activateNote() // Pure installation; I/O has already completed.
	m.clearOverlays(stateTyping)
	var recovery tea.Cmd
	if m.needsSave && m.Note.Revision < msg.Note.Revision {
		var err error = &ConflictError{ID: m.Note.ID, Expected: m.Note.Revision, Actual: msg.Note.Revision}
		if msg.Note.DeletedAt != "" {
			err = ErrTrashed
		}
		m.recordSaveError(err)
		recovery = m.requestConflictRead()
	} else if m.Note.DeletedAt != "" {
		recovery = m.installLifecycleDraft(m.activeKey(), m.EditorDraft)
	} else if r.Trash && m.Note.ID < 0 {
		m.state = stateTrash
	}
	if r.Reload {
		m.saveErr = nil
		m.confirmReload = false
		m.flashMsg = "Reloaded latest note"
	}
	if r.Match != nil {
		if _, resumed := m.drafts[msg.Note.ID]; !resumed {
			m.cursorRow = max(0, min(r.Match.MatchRow, len(m.buffer)-1))
			m.cursorCol = max(0, min(r.Match.MatchCol, len(m.buffer[m.cursorRow])))
			m.virtualPhysCol = m.cursorCol
			*m.viewportTop = max(0, m.cursorRow-m.viewport.Height/2)
		}
	}
	m.searchGeneration++
	m.searchPending = false
	m.searchQuery = ""
	m.searchInput.Reset()
	return recovery
}

func (m *model) requestSearch() tea.Cmd {
	m.searchGeneration++
	m.searchPending = true
	m.searchErr = nil
	m.searchIndex = 0
	m.searchResults = nil
	r := searchRequest{m.searchGeneration, m.searchInput.Value(), m.prevState == stateTrash}
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return searchDebounceMsg(r) })
}
func (m *model) requestHighlight() tea.Cmd {
	m.highlightGeneration++
	m.highlightDirty = false
	content, theme := bufferToString(m.buffer), m.theme
	key := m.activeKey()
	id, version, generation := m.activeID(), m.localVersion, m.highlightGeneration
	m.cachedHighlight = content
	return func() tea.Msg {
		return highlightMsg{Key: key, NoteID: id, LocalVersion: version, Generation: generation, originalText: content, highlightedText: computeHighlight(content, theme)}
	}
}
func (m *model) requestConflictRead() tea.Cmd {
	if m.state != stateConflict {
		return nil
	}
	m.recoveryGeneration++
	generation, id, version, store := m.recoveryGeneration, m.activeID(), m.localVersion, m.store
	return func() tea.Msg {
		n, err := store.GetNote(id)
		return recoveryResult{generation, id, version, n, err, false}
	}
}
func (m *model) requestCopy() tea.Cmd {
	if m.copyPending {
		return nil
	}
	m.copyPending = true
	m.recoveryGeneration++
	generation, id, version, store := m.recoveryGeneration, m.activeID(), m.localVersion, m.store
	n := cloneNote(m.Note)
	n.Content = bufferToString(m.buffer)
	n.ID, n.Revision = 0, 0
	n.DeletedAt = ""
	return func() tea.Msg {
		saved, err := store.SaveCopy(n)
		return recoveryResult{generation, id, version, saved, err, true}
	}
}
func (m *model) mutate(kind string) tea.Cmd {
	if m.actionPending || m.activeID() <= 0 {
		return nil
	}
	m.actionPending = true
	m.openGeneration++
	m.loadPending = false
	m.mutationGeneration = m.nextRequest()
	generation, id, version, revision, store := m.mutationGeneration, m.activeID(), m.localVersion, m.Note.Revision, m.store
	return func() tea.Msg {
		var err error
		switch kind {
		case "trash":
			err = store.TrashCAS(id, revision)
		case "restore":
			err = store.RestoreCAS(id, revision)
		case "purge", "ghost":
			err = store.PurgeCAS(id, revision)
		}
		return mutationResult{generation, id, version, kind, err}
	}
}

// All persistence/navigation keys go through this gate. While a save runs,
// typing/resize remain available; a transition waits for the newest draft.
func (m *model) afterSave(key tea.KeyMsg) tea.Cmd {
	m.pendingAction = &key
	m.pendingActionID = m.activeKey()
	if m.exitPending() && !m.needsSave && m.saveErr == nil {
		return m.finishExit()
	}
	_, saving := m.saves[m.activeKey()]
	if m.needsSave || m.saveErr != nil || saving {
		cmd := m.requestSave()
		_, saving = m.saves[m.activeKey()]
		if m.needsSave || m.saveErr != nil || saving {
			return cmd
		}
	}
	m.pendingAction = nil
	return m.runAction(key)
}
func (m *model) runAction(key tea.KeyMsg) tea.Cmd {
	switch key.String() {
	case "enter":
		if m.pendingOpen != nil {
			n := *m.pendingOpen
			m.pendingOpen = nil
			return m.requestOpen(n.ID, m.prevState == stateTrash, &n, false)
		}
	case "esc", "ctrl+c":
		if m.searchQuery != "" {
			return m.requestOpen(-1, false, nil, false)
		}
		return tea.Quit
	case "ctrl+f":
		m.pendingAction = nil
		m.pendingOpen = nil
		m.openGeneration++
		m.loadPending = false
		m.stashNote()
		previous := m.state
		m.pushOverlay(stateSearch)
		m.prevState = previous
		m.searchInput.Focus()
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.CursorEnd()
		return tea.Batch(textinput.Blink, m.requestSearch())
	case "ctrl+t":
		return m.requestOpen(-1, true, nil, false)
	case "ctrl+u":
		return m.requestOpen(-1, false, nil, false)
	case "ctrl+n":
		m.stashNote()
		n := Note{Content: "", CreatedTime: time.Now(), UpdatedTime: time.Now()}
		m.notes = append([]Note{n}, m.notes...)
		m.allNotes = append([]Note(nil), m.notes...)
		m.index = 0
		m.activateNote()
		m.openGeneration++
		m.loadPending = false
	case "ctrl+s":
		m.pushOverlay(statePromptFile)
		m.fileInput.SetValue(extractTitle(bufferToString(m.buffer)) + ".md")
		m.fileInput.CursorEnd()
		m.fileInput.Focus()
		return textinput.Blink
	case "ctrl+d":
		if m.activeID() <= 0 || m.Note.IsGhost {
			return m.requestOpen(-1, false, nil, false)
		}
		if strings.TrimSpace(bufferToString(m.buffer)) == "" {
			m.confirm("purge", "Permanently delete empty note?", "This cannot be undone by Quicknote.", "")
			return nil
		}
		return m.mutate("trash")
	case "ctrl+g":
		if !m.Note.IsGhost && m.activeID() > 0 {
			m.confirm("ghost", "Delete original and continue as ghost?", "This removes the persisted original. Previous disk copies/backups may remain. Cancel keeps the original.", "")
			return nil
		}
		m.Note.IsGhost = !m.Note.IsGhost
		m.needsSave = true
		return m.requestSave()
	case "alt+right", "alt+left":
		target := m.index + 1
		if key.String() == "alt+left" {
			target = m.index - 1
		}
		if target >= 0 && target < len(m.notes) {
			if m.notes[target].ID == 0 {
				m.stashNote()
				m.index = target
				m.activateNote()
				m.openGeneration++
				return nil
			}
			return m.requestOpen(m.notes[target].ID, m.state == stateTrash, nil, false)
		}
	case "f1":
		previous := m.state
		m.pushOverlay(stateHelp)
		m.prevState = previous
		m.openGeneration++
		m.loadPending = false
		generation, store := m.openGeneration, m.store
		m.viewport.SetContent(m.generateHelpText())
		m.viewport.GotoTop()
		return func() tea.Msg {
			active, err := store.ListChecked(false)
			if err != nil {
				return helpResult{Generation: generation, Err: err}
			}
			trash, err := store.ListChecked(true)
			count := func(notes []Note) int {
				n := 0
				for _, note := range notes {
					if note.ID > 0 {
						n++
					}
				}
				return n
			}
			return helpResult{generation, count(active), count(trash), err}
		}
	}
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.bindDraft()
	m.clampDraftCursor()
	if intent, ok := msg.(ActionMsg); ok && m.state == stateConfirm {
		if a, found := findAction(intent.ID); found && a.Available(m.actionContext()) {
			msg = a.Execute()
		} else {
			m.flashMsg = "Action unavailable"
			return m, nil
		}
	}
	if mouse, ok := msg.(tea.MouseMsg); ok && isOverlay(m.state) {
		if m.state == stateHelp {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(mouse)
			return m, cmd
		}
		return m, nil
	}
	if key, ok := msg.(tea.KeyMsg); ok && m.state == stateConfirm {
		before := m.buffer.Text()
		cmd := m.confirmationKey(key.String())
		if m.buffer.Text() != before {
			m.versionSeq++
			m.localVersion = m.versionSeq
			cmd = tea.Batch(cmd, m.requestHighlight())
		}
		m.clampDraftCursor()
		m.updateEditorScroll()
		m.syncSummary()
		return m, cmd
	}
	explicit := false
	if intent, ok := msg.(ActionMsg); ok {
		a, found := findAction(intent.ID)
		if !found || !a.Available(m.actionContext()) {
			m.flashMsg = "Action unavailable"
			return m, nil
		}
		if enabled, reason := a.Enabled(m.actionContext()); !enabled {
			m.flashMsg = reason
			return m, nil
		}
		if intent.ID == "commands.open" {
			m.openCommands()
			return m, nil
		}
		if intent.ID == "keyboard.profile" {
			cmd := m.switchKeyboard()
			return m, cmd
		}
		msg = a.Execute()
		explicit = true
	}
	if key, ok := msg.(tea.KeyMsg); ok && !explicit && m.state != stateCommands {
		if a, found := m.boundAction(key.String(), m.actionContext()); found {
			if enabled, reason := a.Enabled(m.actionContext()); !enabled {
				m.flashMsg = reason
				return m, nil
			}
			if a.ID == "commands.open" {
				m.openCommands()
				return m, nil
			}
			if a.ID == "keyboard.profile" {
				cmd := m.switchKeyboard()
				return m, cmd
			}
			msg = a.Execute()
		} else if _, old := actionForKey(key.String(), m.actionContext()); old {
			return m, nil
		}
	}
	if key, ok := msg.(tea.KeyMsg); ok && m.state == stateCommands {
		switch key.String() {
		case "esc", "ctrl+c":
			m.popOverlay()
		case "up":
			m.commandIndex = max(0, m.commandIndex-1)
		case "down":
			m.commandIndex = min(max(0, len(m.commandActions())-1), m.commandIndex+1)
		case "enter":
			actions := m.commandActions()
			if m.commandIndex >= 0 && m.commandIndex < len(actions) {
				a := actions[m.commandIndex]
				ctx := m.actionContext()
				ctx.State = m.commandReturn
				if enabled, reason := a.Enabled(ctx); !enabled {
					m.flashMsg = reason
					return m, nil
				}
				m.popOverlay()
				return m.Update(ActionMsg{a.ID})
			}
		}
		return m, nil
	}
	beforeCursor := textState{m.buffer.Text(), m.cursorRow, m.cursorCol}
	beforeUndo := append([]textState(nil), m.undoStack...)
	beforeRedo := append([]textState(nil), m.redoStack...)
	beforeID, beforeContent := m.activeKey(), bufferToString(m.buffer)
	var cmd tea.Cmd
	m, cmd = m.updateAsync(msg)
	_, keyEdit := msg.(tea.KeyMsg)
	if keyEdit && m.activeKey() == beforeID && bufferToString(m.buffer) != beforeContent {
		key := msg.(tea.KeyMsg)
		if key.String() != "ctrl+z" && key.String() != "alt+z" && key.String() != "ctrl+y" && key.String() != "alt+y" {
			m.undoStack = beforeUndo
			m.redoStack = beforeRedo
			m.recordEdit(beforeCursor, key.Type == tea.KeyRunes && len(key.Runes) == 1 && !strings.ContainsAny(key.String(), "`=:"), time.Now())
		} else {
			m.typingGroup = false
		}
		m.versionSeq++
		m.localVersion = m.versionSeq
		if m.state == stateTyping {
			m.needsSave = true
		}
		m.highlightDirty = true
	}
	if m.highlightDirty {
		cmd = tea.Batch(cmd, m.requestHighlight())
	}
	m.clampDraftCursor()
	m.updateEditorScroll()
	m.syncSummary()
	return m, cmd
}
func (m model) updateAsync(msg tea.Msg) (model, tea.Cmd) {
	switch msg := msg.(type) {
	case maintenanceResult:
		cmd := m.receiveMaintenance(msg)
		return m, cmd
	case lifecycleResult:
		cmd := m.receiveLifecycle(msg)
		return m, cmd
	case exportCheckResult:
		cmd := m.receiveExportCheck(msg)
		return m, cmd
	case exportResult:
		m.receiveExport(msg)
		return m, nil
	case keymapResult:
		if msg.Generation == m.keyboardGeneration {
			if msg.Err != nil {
				m.flashMsg = "Keyboard config failed: " + msg.Err.Error()
			} else {
				m.keymap = msg.Keymap
				m.flashMsg = "Keyboard profile: " + msg.Keymap.Profile
			}
		}
		return m, nil
	case saveResult:
		cmd := m.receiveSave(msg)
		return m, cmd
	case loadResult:
		cmd := m.receiveLoad(msg)
		return m, cmd
	case searchDebounceMsg:
		r := searchRequest(msg)
		if r.Generation != m.searchGeneration || m.state != stateSearch {
			return m, nil
		}
		store := m.store
		return m, func() tea.Msg {
			notes, err := store.SearchChecked(r.Query, r.Trash)
			return searchResult{r, notes, err}
		}
	case searchResult:
		r := msg.Request
		if m.state == stateSearch && r.Generation == m.searchGeneration && r.Query == m.searchInput.Value() && r.Trash == (m.prevState == stateTrash) {
			m.searchPending = false
			m.searchErr = msg.Err
			m.searchResults = msg.Notes
			m.searchIndex = 0
		}
		return m, nil
	case highlightMsg:
		if msg.Key == m.activeKey() && msg.NoteID == m.activeID() && msg.LocalVersion == m.localVersion && msg.Generation == m.highlightGeneration && msg.originalText == bufferToString(m.buffer) {
			m.cachedHighlight = msg.highlightedText
		}
		return m, nil
	case initDataMsg:
		m.startupPending = false
		recovery := m.reconcileMaintenance(msg.maintenance)
		if m.keyboardGeneration == 0 && msg.keyboard.Version != 0 {
			m.keymap = msg.keyboard
			if msg.keyboardNotice != "" {
				m.flashMsg = msg.keyboardNotice
			}
		}
		if msg.theme.BgDark != "" {
			m.theme = msg.theme
			m.highlightDirty = true
		}
		if msg.err != nil {
			m.showError("Load failed", msg.err)
			return m, recovery
		}
		if m.openGeneration != 0 || m.localVersion != 0 || m.buffer.Text() != "" {
			return m, recovery
		}
		m.notes = msg.notes
		m.allNotes = append([]Note(nil), msg.notes...)
		m.index = 0
		m.activateNote()
		return m, recovery
	case recoveryResult:
		if msg.Copy {
			m.copyPending = false
		}
		if msg.Generation != m.recoveryGeneration || msg.NoteID != m.activeID() || msg.Version != m.localVersion || m.state != stateConflict {
			if msg.Copy && msg.Err == nil {
				m.flashMsg = fmt.Sprintf("Saved snapshot as copy %d; current draft kept", msg.Note.ID)
			}
			return m, nil
		}
		if !msg.Copy {
			m.conflictRemote = msg.Note
			m.conflictReadErr = msg.Err
			return m, nil
		}
		if msg.Err != nil {
			m.conflictReadErr = fmt.Errorf("save copy failed: %w", msg.Err)
			return m, nil
		}
		m.stashNote()
		original := m.drafts[m.activeKey()]
		original.recovered = true
		m.drafts[m.activeKey()] = original
		m.notes = append(m.notes, msg.Note)
		m.allNotes = append([]Note(nil), m.notes...)
		m.index = len(m.notes) - 1
		m.Note = cloneNote(msg.Note)
		m.draftReady = true
		// Copy recovery preserves the current cursor/history on the new identity.
		m.saveErr = nil
		m.needsSave = false
		m.clearOverlays(stateTyping)
		m.versionSeq++
		m.localVersion = m.versionSeq
		m.stashNote()
		m.flashMsg = "Saved as a new copy; original kept"
		return m, nil
	case mutationResult:
		if msg.Generation != m.mutationGeneration {
			return m, nil
		}
		m.actionPending = false
		if msg.NoteID != m.activeID() {
			return m, nil
		}
		if msg.Err != nil {
			m.recordSaveError(msg.Err)
			cmd := m.requestConflictRead()
			return m, cmd
		}
		if msg.Version != m.localVersion {
			m.recordSaveError(ErrNotFound)
			cmd := m.requestConflictRead()
			return m, cmd
		}
		if msg.Kind == "ghost" {
			delete(m.drafts, msg.NoteID)
			m.Note.ID = 0
			m.Note.Revision = 0
			m.Note.IsGhost = true
			m.sessionSeq++
			m.Note.SessionID = m.sessionSeq
			for i, n := range m.notes {
				if n.ID == msg.NoteID {
					m.notes[i] = cloneNote(m.Note)
					m.index = i
				}
			}
			m.needsSave = false
			return m, nil
		}
		delete(m.drafts, msg.NoteID)
		if msg.Kind == "restore" {
			load := m.requestOpen(msg.NoteID, false, nil, false)
			return m, load
		}
		cmd := m.requestOpen(-1, false, nil, false)
		return m, cmd
	case helpResult:
		if m.state == stateHelp && msg.Generation == m.openGeneration {
			if msg.Err != nil {
				m.flashMsg = "Load failed: " + msg.Err.Error()
			} else {
				m.statActive = msg.Active
				m.statTrash = msg.Trash
				m.viewport.SetContent(m.generateHelpText())
			}
		}
		return m, nil
	case tickMsg:
		m.timerNow = time.Time(msg)
		maintenance := m.requestMaintenance()
		var save tea.Cmd
		if m.state == stateTyping && m.saveErr == nil && m.needsSave && time.Since(m.lastActivity) > 2*time.Second {
			save = m.requestSave()
		}
		return m, tea.Batch(tickCmd(), maintenance, save)
	case tea.KeyMsg:
		m.lastActivity = time.Now()
		key := msg.String()
		if key == "f1" && m.state != stateHelp && m.state != stateConflict {
			cmd := m.runAction(msg)
			return m, cmd
		}
		if m.state == stateConflict {
			switch key {
			case "c":
				if m.loadPending {
					return m, nil
				}
				cmd := m.requestCopy()
				return m, cmd
			case "r":
				if !m.confirmReload {
					m.confirmReload = true
					m.confirm("reload", "Discard local draft?", "Reload the latest stored version. Local unsaved edits will be discarded.", "")
					return m, nil
				}
				cmd := m.requestOpen(m.activeID(), false, nil, true)
				return m, cmd
			default:
				if key == "esc" {
					m.openGeneration++
					m.loadPending = false
					m.recoveryGeneration++
				}
				m.handleConflictKey(key)
				return m, nil
			}
		}
		if m.state == stateSearch {
			switch key {
			case "esc", "ctrl+c":
				m.pendingAction = nil
				m.pendingOpen = nil
				m.popOverlay()
				m.searchInput.Reset()
				m.searchGeneration++
				m.searchPending = false
				m.openGeneration++
				m.loadPending = false
			case "up", "ctrl+k":
				m.searchIndex = max(0, m.searchIndex-1)
			case "down", "ctrl+j":
				m.searchIndex = min(max(0, len(m.searchResults)-1), m.searchIndex+1)
			case "enter":
				if !m.searchPending && m.searchErr == nil && m.searchIndex >= 0 && m.searchIndex < len(m.searchResults) {
					n := m.searchResults[m.searchIndex]
					m.pendingOpen = &n
					cmd := m.afterSave(msg)
					return m, cmd
				}
			default:
				before := m.searchInput.Value()
				var cmd tea.Cmd
				m.searchInput, cmd = m.searchInput.Update(msg)
				if before != m.searchInput.Value() {
					m.pendingAction = nil
					m.pendingOpen = nil
					m.openGeneration++
					m.loadPending = false
					searchCmd := m.requestSearch()
					return m, tea.Batch(cmd, searchCmd)
				}
				return m, cmd
			}
			return m, nil
		}
		if m.state == stateTrash {
			switch key {
			case "esc", "ctrl+t":
				cmd := m.requestOpen(-1, false, nil, false)
				return m, cmd
			case "ctrl+r":
				cmd := m.mutate("restore")
				return m, cmd
			case "ctrl+f", "f1", "alt+left", "alt+right":
				cmd := m.runAction(msg)
				return m, cmd
			}
		}
		if m.state == stateHelp && (key == "esc" || key == "ctrl+c" || key == "enter" || key == "f1") {
			m.popOverlay()
			m.openGeneration++
			return m, nil
		}
		if m.state == stateTyping {
			if key == "ctrl+f" && !m.actionPending {
				cmd := m.runAction(msg)
				return m, cmd
			}
			if m.actionPending { // An irreversible operation already owns this snapshot.
				if key == "esc" {
					m.flashMsg = "Operation pending; draft kept"
					return m, nil
				}
			}
			if m.saveErr != nil && key == "enter" {
				cmd := m.requestSave()
				return m, cmd
			}
			switch key {
			case "ctrl+u":
				if m.activeID() > 0 {
					m.needsSave = true
				}
				cmd := m.afterSave(msg)
				return m, cmd
			case "ctrl+d":
				if strings.TrimSpace(bufferToString(m.buffer)) == "" {
					cmd := m.runAction(msg)
					return m, cmd
				}
				cmd := m.afterSave(msg)
				return m, cmd
			case "ctrl+g", "ctrl+n", "ctrl+t", "ctrl+f", "ctrl+s", "esc", "ctrl+c", "alt+left", "alt+right", "f1":
				if m.actionPending {
					return m, nil
				}
				cmd := m.afterSave(msg)
				return m, cmd
			}
		}
	}
	next, cmd := m.updateEditor(msg)
	return next.(model), cmd
}
func (m model) saveStatus() string {
	if m.saveErr != nil {
		if errors.Is(m.saveErr, ErrConflict) {
			return "Conflict — Enter"
		}
		return "Save failed — Enter"
	}
	if m.actionPending {
		return "Working…"
	}
	if _, ok := m.saves[m.activeKey()]; ok {
		return "Saving…"
	}
	if m.activeID() >= 0 && m.Note.IsGhost {
		return "Ghost"
	}
	if m.needsSave {
		return "Unsaved"
	}
	if m.loadPending {
		return "Loading…"
	}
	return "Saved"
}

func (m model) exitPending() bool {
	return m.pendingAction != nil && (m.pendingAction.String() == "esc" || m.pendingAction.String() == "ctrl+c")
}

// Quit waits for every outstanding draft, including notes no longer active.
// A failed inactive save reopens its retained session for explicit recovery.
func (m *model) finishExit() tea.Cmd {
	if !m.exitPending() || len(m.saves) > 0 || len(m.lifecycleRequests) > 0 {
		return nil
	}
	m.stashNote()
	for id, d := range m.drafts {
		if !d.needsSave || d.Note.IsGhost || d.recovered {
			continue
		}
		if d.saveErr == nil {
			n := cloneNote(d.Note)
			n.Content = d.buffer.Text()
			return m.saveSnapshot(n, d.localVersion)
		}
		m.pendingAction = nil
		found := -1
		for i, n := range m.notes {
			if noteKey(n) == id {
				found = i
				break
			}
		}
		if found < 0 {
			m.notes = append(m.notes, d.Note)
			found = len(m.notes) - 1
		}
		m.index = found
		m.notes[m.index] = d.Note
		m.activateNote()
		m.recordSaveError(d.saveErr)
		return m.requestConflictRead()
	}
	key := *m.pendingAction
	m.pendingAction = nil
	return m.runAction(key)
}
