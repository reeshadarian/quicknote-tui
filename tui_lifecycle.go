package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

type lifecycleIntent struct {
	Kind       string
	Expiration ExpirationChoice
	Restore    RestoreMode
	Pinned     bool
}
type lifecycleRequest struct {
	Key                 int
	ID                  NoteID
	Expected            int64
	Version, Generation uint64
	Intent              lifecycleIntent
}
type lifecycleResult struct {
	Request lifecycleRequest
	Note    Note
	Err     error
}
type maintenanceResult struct {
	Generation uint64
	Result     MaintenanceResult
	Err        error
}

func (m *model) lifecycleNow() time.Time {
	if m.clock != nil {
		return m.clock.Now()
	}
	if !m.timerNow.IsZero() {
		return m.timerNow
	}
	return time.Now()
}
func (m *model) requestMaintenance() tea.Cmd {
	if m.store == nil || m.maintenancePending || m.startupPending {
		return nil
	}
	now := m.lifecycleNow()
	if !m.lastMaintenance.IsZero() && now.Sub(m.lastMaintenance) < 30*time.Second && !now.Before(m.lastMaintenance) {
		return nil
	}
	m.stashNote()
	protected := []NoteID{}
	var commands []tea.Cmd
	for key, d := range m.drafts {
		_, saving := m.saves[key]
		_, mutating := m.lifecycleRequests[key]
		if d.Note.ID > 0 && (key == m.activeKey() || d.needsSave || d.saveErr != nil || saving || mutating) {
			protected = append(protected, d.Note.ID)
			if noteDue(d.Note, now) && d.saveErr == nil && !mutating {
				intent := lifecycleIntent{Kind: "expire"}
				if m.lifecycleQueued == nil {
					m.lifecycleQueued = map[int]lifecycleIntent{}
				}
				m.lifecycleQueued[key] = intent
				if !saving {
					if d.needsSave {
						n := cloneNote(d.Note)
						n.Content = d.buffer.Text()
						commands = append(commands, m.saveSnapshot(n, d.localVersion))
					} else {
						commands = append(commands, m.startLifecycle(key, d, intent))
					}
				}
			}
		}
	}
	m.maintenancePending = true
	m.lastMaintenance = now
	m.maintenanceGeneration++
	gen, store := m.maintenanceGeneration, m.store
	commands = append(commands, func() tea.Msg { r, e := store.Maintain(now, protected); return maintenanceResult{gen, r, e} })
	return tea.Batch(commands...)
}
func (m *model) receiveMaintenance(msg maintenanceResult) tea.Cmd {
	if msg.Generation != m.maintenanceGeneration {
		return nil
	}
	m.maintenancePending = false
	if msg.Err != nil {
		m.flashMsg = "Maintenance failed: " + msg.Err.Error()
		return nil
	}
	m.reconcileMaintenance(msg.Result)
	return nil
}
func (m *model) reconcileMaintenance(result MaintenanceResult) {
	m.stashNote()
	for _, c := range result.Expired {
		if d, ok := m.drafts[c.ID]; ok && d.Note.Revision < c.Revision {
			d.Note.Revision = c.Revision
			d.Note.DeletedAt = FormatLifecycleTime(c.DeletedAt)
			d.Note.DeletedTime = c.DeletedAt
			d.Note.DeleteReason = c.Reason
			m.drafts[c.ID] = d
			m.installLifecycleDraft(c.ID, d)
		}
		m.markSummaryTrashed(c.ID, c.Revision, c.DeletedAt, c.Reason)
	}
	for _, id := range result.Purged {
		if d, ok := m.drafts[id]; ok {
			d.saveErr = ErrNotFound
			m.drafts[id] = d
			if m.activeID() == id {
				m.recordSaveError(ErrNotFound)
			}
		}
	}
}
func (m *model) markSummaryTrashed(id NoteID, revision int64, at time.Time, reason DeleteReason) {
	for _, list := range [][]Note{m.notes, m.allNotes, m.searchResults} {
		for i := range list {
			if list[i].ID == id && list[i].Revision <= revision {
				list[i].Revision = revision
				list[i].DeletedAt = FormatLifecycleTime(at)
				list[i].DeletedTime = at
				list[i].DeleteReason = reason
			}
		}
	}
}
func (m *model) requestLifecycle(intent lifecycleIntent) tea.Cmd {
	if intent.Expiration.Deadline != nil {
		deadline := *intent.Expiration.Deadline
		intent.Expiration.Deadline = &deadline
	}
	m.bindDraft()
	if m.store == nil || m.Note.IsGhost || m.activeID() <= 0 {
		return nil
	}
	key := m.activeKey()
	if _, busy := m.lifecycleRequests[key]; busy {
		return nil
	}
	m.stashNote()
	if m.lifecycleQueued == nil {
		m.lifecycleQueued = map[int]lifecycleIntent{}
	}
	m.lifecycleQueued[key] = intent
	if m.needsSave || m.saveErr != nil {
		return m.requestSave()
	}
	if _, saving := m.saves[key]; saving {
		return nil
	}
	return m.startLifecycle(key, m.drafts[key], intent)
}
func (m *model) startLifecycle(key int, d EditorDraft, intent lifecycleIntent) tea.Cmd {
	delete(m.lifecycleQueued, key)
	if intent.Kind == "expire" && !noteDue(d.Note, m.lifecycleNow()) {
		return nil
	}
	if m.lifecycleRequests == nil {
		m.lifecycleRequests = map[int]lifecycleRequest{}
	}
	r := lifecycleRequest{key, d.Note.ID, d.Note.Revision, d.localVersion, m.nextRequest(), intent}
	m.lifecycleRequests[key] = r
	now, store := m.lifecycleNow(), m.store
	return func() tea.Msg {
		var n Note
		var e error
		switch intent.Kind {
		case "expire":
			n, e = store.ExpireNoteCAS(r.ID, r.Expected, now)
		case "expiration":
			n, e = store.ApplyExpirationCAS(r.ID, r.Expected, intent.Expiration, now)
		case "pin":
			n, e = store.ApplyPinnedCAS(r.ID, r.Expected, intent.Pinned, now)
		case "trash":
			n, e = store.TrashSnapshotCAS(r.ID, r.Expected, DeleteManual, now)
		case "restore":
			n, e = store.RestoreSnapshotCAS(r.ID, r.Expected, intent.Restore, now)
		default:
			e = fmt.Errorf("unknown lifecycle operation")
		}
		return lifecycleResult{r, n, e}
	}
}
func (m *model) installLifecycleDraft(key int, d EditorDraft) {
	if m.activeKey() != key {
		return
	}
	m.Note = cloneNote(d.Note)
	if d.Note.DeletedAt != "" {
		if d.needsSave || d.saveErr != nil {
			m.recordSaveError(ErrTrashed)
		} else if m.state == stateTyping {
			m.state = stateTrash
			m.flashMsg = "Expired: recoverable in Trash"
		} else {
			m.flashMsg = "Note expired; recoverable in Trash"
			if m.prevState == stateTyping {
				m.prevState = stateTrash
			}
			if m.commandReturn == stateTyping {
				m.commandReturn = stateTrash
			}
			for i := range m.overlays {
				if m.overlays[i].State == stateTyping {
					m.overlays[i].State = stateTrash
				}
				if m.overlays[i].Previous == stateTyping {
					m.overlays[i].Previous = stateTrash
				}
				if m.overlays[i].CommandReturn == stateTyping {
					m.overlays[i].CommandReturn = stateTrash
				}
			}
		}
	}
}
func (m *model) receiveLifecycle(msg lifecycleResult) tea.Cmd {
	r := msg.Request
	pending, ok := m.lifecycleRequests[r.Key]
	if !ok || pending.Generation != r.Generation {
		return nil
	}
	delete(m.lifecycleRequests, r.Key)
	m.stashNote()
	d, ok := m.drafts[r.Key]
	if !ok {
		return nil
	}
	if msg.Err != nil {
		m.pendingAction = nil
		d.saveErr = msg.Err
		m.drafts[r.Key] = d
		if m.activeKey() == r.Key {
			m.recordSaveError(msg.Err)
			return m.requestConflictRead()
		}
		m.flashMsg = "Lifecycle change failed; draft retained"
		return nil
	}
	// Metadata acknowledgments never replace the draft's buffer or history.
	d.Note = cloneNote(msg.Note)
	d.Note.Content = d.buffer.Text()
	d.needsSave = d.needsSave || d.localVersion != r.Version
	d.saveErr = nil
	if d.Note.DeletedAt != "" && d.needsSave {
		d.saveErr = ErrTrashed
	}
	m.drafts[r.Key] = d
	m.installLifecycleDraft(r.Key, d)
	for _, list := range [][]Note{m.notes, m.allNotes} {
		for i := range list {
			if list[i].ID == r.ID {
				list[i] = cloneNote(d.Note)
			}
		}
	}
	if d.Note.DeletedAt == "" && d.needsSave {
		if m.activeKey() == r.Key {
			return m.requestSave()
		}
		return m.saveSnapshot(d.Note, d.localVersion)
	}
	return m.finishExit()
}
