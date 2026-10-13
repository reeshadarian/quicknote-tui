package main

import tea "github.com/charmbracelet/bubbletea"

type GhostDisposition int

const (
	KeepOriginal GhostDisposition = iota
	TrashOriginal
)

// Identity is allocated before content can receive editor events. Ghosts never
// inherit persisted lifecycle fields, tags, revision or deletion metadata.
func (m *model) newGhostDraft(content string) { m.addGhostDraft(content, true) }
func (m *model) addGhostDraft(content string, activate bool) {
	m.stashNote()
	m.sessionSeq++
	n := Note{SessionID: m.sessionSeq, IsGhost: true, Content: content}
	m.notes = append(m.notes, n)
	m.allNotes = append(m.allNotes, n)
	m.versionSeq++
	d := EditorDraft{Note: n, buffer: stringToBuffer(content), localVersion: m.versionSeq, viewportTop: new(int)}
	m.drafts[noteKey(n)] = d
	if activate {
		m.index = len(m.notes) - 1
		m.activateNote()
		m.clearOverlays(stateTyping)
		m.openGeneration++
		m.loadPending = false
		m.cachedHighlight = ""
		m.highlightDirty = true
	}
}
func (m *model) requestGhostConversion(disposition GhostDisposition) tea.Cmd {
	if m.Note.IsGhost || m.Note.DeletedAt != "" {
		return nil
	}
	if m.Note.ID == 0 && m.buffer.Text() == "" {
		m.newGhostDraft("")
		return nil
	}
	intent := lifecycleIntent{Kind: "ghost", GuardGhost: true, SourceKey: m.activeKey(), SourceVersion: m.localVersion, GhostContent: m.buffer.Text()}
	if disposition == TrashOriginal {
		intent.Kind = "ghostTrash"
	}
	return m.requestLifecycle(intent)
}
func (m *model) openGhostConfirmation() {
	if m.Note.IsGhost {
		m.confirm("ghostPersist", "Persist this ghost?", "Create an ordinary saved note. Its content will leave memory-only mode and may remain in database files and backups.", "")
	} else {
		m.confirm("ghost", "Continue as a ghost?", "Duplicate keeps the original. Trash keeps it recoverable for 72 hours. Previously persisted copies and backups may remain.", "")
	}
}
func (m *model) completeGhostConversion(r lifecycleRequest) {
	unchanged := m.activeKey() == r.Key && m.localVersion == r.Version && m.pendingAction == nil && !m.loadPending && !isOverlay(m.state)
	// Resolve an unavailable arrow against the original list. Appending the
	// captured ghost must not create a destination for an already-pending key,
	// including a continuation that will resume after a newer source save.
	if m.pendingAction != nil && m.pendingActionID == r.Key && m.activeKey() == r.Key {
		key := m.pendingAction.String()
		if (key == "alt+left" && m.index == 0) || (key == "alt+right" && m.index+1 >= len(m.notes)) {
			m.pendingAction = nil
		}
	}
	m.addGhostDraft(r.Intent.GhostContent, unchanged)
	if unchanged {
		m.flashMsg = "Ghost · unsaved by design"
	} else {
		m.flashMsg = "Ghost snapshot retained; newer source draft kept"
	}
}

// A successful Trash conversion may leave newer unsaved source text. Keep its
// recoverable draft while completing a requested navigation; exit still uses
// the ordinary all-draft recovery gate. Active-note navigation must not inherit
// the source's newly installed Trash/Conflict context.
func (m *model) resumeGhostTransition(r lifecycleRequest) tea.Cmd {
	if m.pendingAction != nil && m.pendingActionID == r.Key && m.activeKey() == r.Key && !m.exitPending() {
		key := *m.pendingAction
		if m.Note.DeletedAt != "" {
			m.pendingAction = nil
			m.clearOverlays(stateTyping)
			return m.runAction(key)
		}
		return m.afterSave(key)
	}
	return m.finishExit()
}

// Discard drops owned references, including filter previews and obsolete export
// checks. It does not promise erasure of OS memory or already-confirmed output.
func (m *model) discardGhost() {
	if !m.Note.IsGhost {
		return
	}
	m.discardMemoryDraft()
}

func (m *model) discardMemoryDraft() {
	if m.Note.ID != 0 {
		return
	}
	wasGhost := m.Note.IsGhost
	key := m.activeKey()
	delete(m.drafts, key)
	delete(m.saves, key)
	delete(m.lifecycleQueued, key)
	delete(m.lifecycleRequests, key)
	filter := func(notes []Note) []Note {
		kept := make([]Note, 0, len(notes))
		for _, n := range notes {
			if noteKey(n) != key {
				kept = append(kept, n)
			}
		}
		return kept
	}
	m.notes = filter(m.notes)
	m.allNotes = filter(m.allNotes)
	m.searchResults = filter(m.searchResults)
	m.confirmation = Confirmation{}
	m.pendingAction = nil
	m.pendingOpen = nil
	m.conflictRemote = Note{}
	m.cachedHighlight = ""
	m.highlightGeneration++
	m.openGeneration++
	m.exportGeneration++
	m.searchGeneration++
	m.recoveryGeneration++
	m.loadPending = false
	m.searchPending = false
	m.fileInput.Reset()
	m.pipeInput.Reset()
	m.searchInput.Reset()
	m.searchQuery = ""
	m.viewport.SetContent("")
	m.clearOverlays(stateTyping)
	m.EditorDraft = EditorDraft{}
	m.notes = append(m.notes, Note{})
	m.index = len(m.notes) - 1
	m.activateNote()
	m.flashMsg = "Unsaved draft discarded"
	if wasGhost {
		m.flashMsg = "Ghost discarded"
	}
}

type clipboardResult struct {
	Key     int
	Version uint64
}

func (m *model) requestClipboard() tea.Cmd {
	if m.Note.IsGhost {
		m.confirm("ghostCopy", "Copy ghost outside Quicknote?", "Clipboard content leaves memory-only mode. Clipboard tools and history may retain it.", "")
		return nil
	}
	return m.copySnapshot()
}
func (m *model) copySnapshot() tea.Cmd {
	key, version, content := m.activeKey(), m.localVersion, m.buffer.Text()
	return func() tea.Msg { copyToClipboard(content); return clipboardResult{key, version} }
}
