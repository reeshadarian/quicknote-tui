package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"time"
)

// TrashUndo deliberately holds no content or editor snapshot. The original row
// and the existing per-note draft own recovery and history respectively.
type TrashUndo struct {
	ID           NoteID
	Revision     int64
	RecoverUntil time.Time
}

func trashEnabled(c ActionContext) (bool, string) {
	if c.Note.ID <= 0 {
		return false, "Trash is empty"
	}
	if c.Note.DeletedAt == "" {
		return false, "Only a Trash entry can be changed here"
	}
	return true, ""
}
func resetDirectiveAvailable(n Note) bool {
	d, err := ParseKillDirective(n.Content)
	return err == nil && d.Present && !n.IsPinned && n.ExpirationSource == ExpirationDirective && !n.ExpirationNeedsReview
}
func (m *model) openRestorePicker() tea.Cmd {
	if enabled, reason := trashEnabled(m.actionContext()); !enabled {
		m.flashMsg = reason
		return nil
	}
	body := "Restore without timer keeps the same note and leaves historical #kill text inactive."
	if !resetDirectiveAvailable(m.Note) {
		body += "\nNo validated remembered timer to reset. Set expiration after restoring."
	}
	m.confirm("restore", "Restore from Trash?", body, "")
	return nil
}
func (m model) restoreChoices() []string {
	choices := []string{"Cancel", "Restore without timer"}
	if resetDirectiveAvailable(m.Note) {
		choices = append(choices, "Restore and reset timer")
	}
	return choices
}
func (m *model) requestDeleteForever() tea.Cmd {
	if enabled, reason := trashEnabled(m.actionContext()); !enabled {
		m.flashMsg = reason
		return nil
	}
	m.confirm("deleteForever", "Delete forever?", "Permanently delete this Trash entry?\nThis cannot be undone by Quicknote.", "")
	return nil
}
func (m *model) confirmTrash(c Confirmation) tea.Cmd {
	if c.Revision != m.Note.Revision || c.Key != m.activeKey() || c.Version != m.localVersion {
		m.flashMsg = "Trash action canceled: note changed"
		return nil
	}
	if enabled, reason := trashEnabled(m.actionContext()); !enabled {
		m.flashMsg = reason
		return nil
	}
	if m.needsSave {
		m.showError("Trash action unavailable", fmt.Errorf("recover the unsaved draft first"))
		return nil
	}
	mode := RestoreWithoutTimer
	if c.Kind == "restore" && c.Choice == 2 {
		mode = RestoreResetDirective
	}
	return m.requestLifecycle(lifecycleIntent{Kind: c.Kind, Restore: mode, GuardTrash: true, SourceKey: c.Key, SourceVersion: c.Version, SourceRevision: c.Revision})
}
func (m model) undoTrashReason(now time.Time) string {
	if m.trashUndo.ID <= 0 {
		return "No move to Trash to undo in this session"
	}
	if now.After(m.trashUndo.RecoverUntil) {
		return "Trash recovery window has ended"
	}
	return ""
}
func (m *model) requestUndoTrash() tea.Cmd {
	if reason := m.undoTrashReason(m.ttlNow()); reason != "" {
		m.flashMsg = reason
		return nil
	}
	u := m.trashUndo
	if _, busy := m.lifecycleRequests[u.ID]; busy {
		m.flashMsg = "Trash action pending"
		return nil
	}
	if _, busy := m.saves[u.ID]; busy {
		m.flashMsg = "Save pending; retry Undo"
		return nil
	}
	m.stashNote()
	d, ok := m.drafts[u.ID]
	if !ok {
		d = EditorDraft{Note: Note{ID: u.ID}, viewportTop: new(int)}
	}
	// Always use the recorded deletion revision, even if a remote restore/edit
	// has already been observed. Never adopt that newer revision for Undo.
	d.Note.Revision = u.Revision
	return m.startLifecycle(u.ID, d, lifecycleIntent{Kind: "undoTrash", Restore: RestoreWithoutTimer, SourceKey: m.activeKey(), SourceVersion: m.localVersion})
}
func (m *model) recordTrashUndo(r lifecycleRequest, n Note) {
	if (r.Intent.Kind != "trash" && r.Intent.Kind != "ghostTrash") || n.DeleteReason != DeleteManual {
		return
	}
	at, err := ParseLifecycleTime(n.DeletedAt)
	if err != nil {
		return
	}
	until := at.Add(72 * time.Hour)
	if r.Generation < m.trashUndoGeneration {
		return
	}
	m.trashUndo = TrashUndo{n.ID, n.Revision, until}
	m.trashUndoGeneration = r.Generation
	m.flashMsg = "Moved to Trash · Undo via Commands"
}
func (m model) trashStatus() string {
	if m.Note.ID <= 0 {
		return "No deleted notes · Esc Back"
	}
	reason := string(m.Note.DeleteReason)
	if reason == "" {
		reason = "unknown reason"
	}
	at, err := ParseLifecycleTime(m.Note.DeletedAt)
	if err != nil {
		return "Trash · " + reason + " · deletion time needs review"
	}
	return fmt.Sprintf("Trash · %s · deleted %s · recoverable until %s", reason, at.In(time.Local).Format("2006-01-02 15:04 MST"), at.Add(72*time.Hour).In(time.Local).Format("2006-01-02 15:04 MST"))
}

const trashTitle = "Trash · read-only"

func (m model) trashFooter() string {
	return m.shortcut("note.restore") + " Restore · " + m.shortcut("commands.open") + " Commands\nDelete forever via Commands · Esc Back"
}

func (m model) trashPreview() (rows []string, height int) {
	body := m.trashStatus() + "\n\n" + m.buffer.Text()
	if m.flashMsg != "" {
		body = m.flashMsg + "\n" + body
	}
	width, total, headers, footers := m.panelGeometry(trashTitle, m.trashFooter())
	return wrapDisplay(body, width), max(0, total-len(headers)-len(footers))
}

func (m model) trashScrollLimit() int {
	rows, height := m.trashPreview()
	if height == 0 {
		return 0
	}
	return max(0, len(rows)-height)
}

func (m *model) clampTrashScroll() {
	if m.state == stateTrash {
		m.viewport.YOffset = max(0, min(m.viewport.YOffset, m.trashScrollLimit()))
	}
}

func (m model) trashView() string {
	rows, _ := m.trashPreview()
	top := max(0, min(m.viewport.YOffset, m.trashScrollLimit()))
	return m.panel(trashTitle, strings.Join(rows[top:], "\n"), m.trashFooter())
}

func (m *model) completeDeleteForever(r lifecycleRequest) tea.Cmd {
	if m.trashUndo.ID == r.ID {
		m.trashUndo = TrashUndo{}
	}
	d, retained := m.drafts[r.Key]
	if retained && (d.needsSave || d.localVersion != r.Version || d.Note.Revision > r.Expected) {
		m.flashMsg = "Deleted from Trash; newer draft retained for recovery"
		if m.activeKey() == r.Key {
			m.recordSaveError(ErrNotFound)
			return m.requestConflictRead()
		}
		return nil
	}
	delete(m.drafts, r.Key)
	delete(m.lifecycleQueued, r.Key)
	m.flashMsg = "Deleted forever"
	if m.activeKey() == r.Key && m.localVersion == r.Version {
		// The load owns clean-source cleanup through reconciliation, including
		// intervening events that might snapshot the departing draft again.
		return m.requestLoad(loadRequest{TargetID: -1, Trash: true, SourcePurged: true, PurgedSourceRevision: r.Expected})
	}
	return m.finishExit()
}
