package main

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"time"
)

type lifecycleIntent struct {
	Kind          string
	Expiration    ExpirationChoice
	Restore       RestoreMode
	Pinned        bool
	GuardTTL      bool
	GuardPin      bool
	GuardGhost    bool
	GhostContent  string
	SourceKey     int
	SourceVersion uint64
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
	return m.reconcileMaintenance(msg.Result)
}
func (m *model) reconcileMaintenance(result MaintenanceResult) tea.Cmd {
	var recovery tea.Cmd
	m.stashNote()
	for _, c := range result.Expired {
		if m.lifecycleChanges == nil {
			m.lifecycleChanges = map[NoteID]LifecycleChange{}
		}
		if old, ok := m.lifecycleChanges[c.ID]; !ok || old.Revision < c.Revision {
			m.lifecycleChanges[c.ID] = c
		}
		if d, ok := m.drafts[c.ID]; ok && d.Note.Revision < c.Revision {
			// Only an adjacent revision proves this deletion was the sole change.
			// A gap may hide another writer's content; keep its original CAS base.
			if c.Revision == d.Note.Revision+1 {
				d.Note.Revision = c.Revision
			}
			d.Note.DeletedAt = FormatLifecycleTime(c.DeletedAt)
			d.Note.DeletedTime = c.DeletedAt
			d.Note.DeleteReason = c.Reason
			m.drafts[c.ID] = d
			if cmd := m.installLifecycleDraft(c.ID, d); cmd != nil {
				recovery = cmd
			}
		}
		m.markSummaryTrashed(c.ID, c.Revision, c.DeletedAt, c.Reason)
	}
	for _, id := range result.Purged {
		if d, ok := m.drafts[id]; ok {
			d.saveErr = ErrNotFound
			m.drafts[id] = d
			if m.activeID() == id {
				m.recordSaveError(ErrNotFound)
				recovery = m.requestConflictRead()
			}
		}
	}
	return recovery
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
	if m.store == nil || m.Note.IsGhost || (m.activeID() <= 0 && !intent.GuardGhost) {
		return nil
	}
	key := m.activeKey()
	if _, busy := m.lifecycleRequests[key]; busy {
		if intent.GuardTTL || intent.GuardPin || intent.GuardGhost {
			if m.lifecycleQueued == nil {
				m.lifecycleQueued = map[int]lifecycleIntent{}
			}
			m.lifecycleQueued[key] = intent
		}
		return nil
	}
	m.stashNote()
	if m.lifecycleQueued == nil {
		m.lifecycleQueued = map[int]lifecycleIntent{}
	}
	m.lifecycleQueued[key] = intent
	if m.needsSave || m.saveErr != nil || m.activeID() == 0 {
		return m.requestSave()
	}
	if _, saving := m.saves[key]; saving {
		return nil
	}
	return m.startLifecycle(key, m.drafts[key], intent)
}
func (m *model) startLifecycle(key int, d EditorDraft, intent lifecycleIntent) tea.Cmd {
	delete(m.lifecycleQueued, key)
	if intent.GuardGhost && (m.activeKey() != key || d.localVersion != intent.SourceVersion || d.Note.DeletedAt != "" || d.Note.IsGhost) {
		m.flashMsg = "Ghost conversion canceled: draft changed"
		return nil
	}
	if intent.GuardTTL && (key != intent.SourceKey || m.activeKey() != key || d.localVersion != intent.SourceVersion || d.Note.DeletedAt != "") {
		m.flashMsg = "Expiration canceled: draft changed"
		return nil
	}
	if intent.GuardPin && (key != intent.SourceKey || m.activeKey() != key || d.localVersion != intent.SourceVersion || d.Note.DeletedAt != "" || d.Note.IsGhost || d.Note.IsPinned == intent.Pinned) {
		m.flashMsg = "Pin change canceled: draft changed"
		return nil
	}
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
		case "ghost":
			n, e = store.GetNote(r.ID)
			if e == nil && n.Revision != r.Expected {
				e = &ConflictError{ID: r.ID, Expected: r.Expected, Actual: n.Revision}
			}
			if e == nil && n.DeletedAt != "" {
				e = ErrTrashed
			}
		case "trash", "ghostTrash":
			n, e = store.TrashSnapshotCAS(r.ID, r.Expected, DeleteManual, now)
		case "restore":
			n, e = store.RestoreSnapshotCAS(r.ID, r.Expected, intent.Restore, now)
		default:
			e = fmt.Errorf("unknown lifecycle operation")
		}
		return lifecycleResult{r, n, e}
	}
}
func (m *model) installLifecycleDraft(key int, d EditorDraft) tea.Cmd {
	if m.activeKey() != key {
		return nil
	}
	m.Note = cloneNote(d.Note)
	if d.Note.DeletedAt != "" {
		if d.needsSave || d.saveErr != nil {
			m.recordSaveError(ErrTrashed)
			return m.requestConflictRead()
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
	return nil
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
		delete(m.lifecycleQueued, r.Key)
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
	// A successful write is acknowledged by removing its request, but a later
	// observed revision owns the lifecycle state (including an external restore).
	if known := m.latestLifecycleSnapshot(msg.Note); known.Revision > msg.Note.Revision {
		// The held acknowledgment can establish the content base immediately before
		// a later deletion; larger gaps still require a fresh full read.
		if d.Note.Revision >= known.Revision {
			if r.Intent.GuardGhost {
				m.completeGhostConversion(r)
			}
			return m.finishExit()
		}
		combined, ok := withAdjacentDeletion(msg.Note, known)
		if !ok {
			if r.Intent.GuardGhost {
				m.completeGhostConversion(r)
			}
			return m.finishExit()
		}
		msg.Note = combined
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
	recovery := m.installLifecycleDraft(r.Key, d)
	if r.Intent.GuardTTL && m.activeKey() == r.Key && m.flashMsg == "Setting expiration…" {
		m.flashMsg = "Expiration updated"
	}
	if r.Intent.GuardPin && m.activeKey() == r.Key && m.flashMsg == "Updating pin…" {
		if d.Note.IsPinned {
			m.flashMsg = "Pinned; expiration cleared"
		} else {
			m.flashMsg = "Unpinned with no timer"
		}
	}
	for _, list := range [][]Note{m.notes, m.allNotes, m.searchResults} {
		for i := range list {
			if list[i].ID == r.ID {
				preview := list[i].Preview
				list[i] = cloneNote(d.Note)
				list[i].Preview = preview
			}
		}
	}
	if r.Intent.GuardGhost {
		m.completeGhostConversion(r)
	}
	if recovery != nil {
		return recovery
	}
	if d.Note.DeletedAt == "" && d.needsSave {
		if m.activeKey() == r.Key {
			return m.requestSave()
		}
		return m.saveSnapshot(d.Note, d.localVersion)
	}
	if intent, queued := m.lifecycleQueued[r.Key]; queued {
		if cmd := m.startLifecycle(r.Key, d, intent); cmd != nil {
			return cmd
		}
	}
	// A declined queued mutation has no worker left to resume a continuation.
	// Resume only the action that still belongs to this acknowledged source.
	if m.pendingAction != nil && m.pendingActionID == r.Key && m.activeKey() == r.Key && !m.exitPending() {
		key := *m.pendingAction
		m.pendingAction = nil
		return m.runAction(key)
	}
	return m.finishExit()
}

// Read results must not erase lifecycle changes already observed in a summary
// merely because the target has never been retained as an editor draft.
func (m model) latestLifecycleSnapshot(n Note) Note {
	latest := n
	consider := func(candidate Note) {
		if candidate.ID == n.ID && candidate.Revision > latest.Revision {
			latest = candidate
		}
	}
	if change, ok := m.lifecycleChanges[n.ID]; ok {
		consider(Note{ID: change.ID, Revision: change.Revision, DeletedAt: FormatLifecycleTime(change.DeletedAt), DeletedTime: change.DeletedAt, DeleteReason: change.Reason})
	}
	consider(m.Note)
	if d, ok := m.drafts[n.ID]; ok {
		consider(d.Note)
	}
	for _, list := range [][]Note{m.notes, m.allNotes, m.searchResults} {
		for _, candidate := range list {
			consider(candidate)
		}
	}
	return cloneNote(latest)
}

// Store deletion mutations do not change content. Only an immediately following
// deletion can therefore advance this full snapshot's base without a new read.
func withAdjacentDeletion(base, known Note) (Note, bool) {
	if known.Revision != base.Revision+1 || known.DeletedAt == "" || (known.DeleteReason != DeleteExpired && known.DeleteReason != DeleteManual) {
		return base, false
	}
	base.Revision = known.Revision
	base.DeletedAt, base.DeletedTime, base.DeleteReason = known.DeletedAt, known.DeletedTime, known.DeleteReason
	return base, true
}

// Pin changes share the existing content-save and metadata dispatcher. Unpin
// needs explicit confirmation because its safe outcome is an active no-timer note.
func (m *model) requestPinToggle() tea.Cmd {
	if enabled, reason := pinEnabled(m.actionContext()); !enabled {
		m.flashMsg = reason
		return nil
	}
	if m.Note.IsPinned {
		m.confirm("pin", "Unpin with no timer?", "Continue with no timer.\nThe cleared deadline will not restart.", "")
		return nil
	}
	m.flashMsg = "Updating pin…"
	return m.requestLifecycle(lifecycleIntent{Kind: "pin", Pinned: true, GuardPin: true, SourceKey: m.activeKey(), SourceVersion: m.localVersion})
}
func (m *model) confirmPin(c Confirmation) tea.Cmd {
	if c.Key != m.activeKey() || c.Version != m.localVersion || !m.Note.IsPinned {
		m.flashMsg = "Pin change canceled: draft changed"
		return nil
	}
	if enabled, reason := pinEnabled(m.actionContext()); !enabled {
		m.flashMsg = reason
		return nil
	}
	m.flashMsg = "Updating pin…"
	return m.requestLifecycle(lifecycleIntent{Kind: "pin", Pinned: false, GuardPin: true, SourceKey: c.Key, SourceVersion: c.Version})
}

// A search row is a summary, never an editor/save snapshot. Refresh metadata
// from a newer observed revision without changing this result's content,
// preview or match coordinates. Opening still reads a full content/revision
// pair, and receiveLoad refetches when a summary proves that read was stale.
func (m model) reconcileSearchLifecycle(n Note) Note {
	n = cloneNote(n)
	known := m.latestLifecycleSnapshot(n)
	if known.Revision <= n.Revision {
		return n
	}
	n.Revision = known.Revision
	n.IsPinned = known.IsPinned
	n.ExpiresAt = known.ExpiresAt
	n.ExpirationSource = known.ExpirationSource
	n.ExpirationNeedsReview = known.ExpirationNeedsReview
	n.KillHash, n.KillStart = known.KillHash, known.KillStart
	n.DeletedAt, n.DeletedTime, n.DeleteReason = known.DeletedAt, known.DeletedTime, known.DeleteReason
	n.UpdatedAt, n.UpdatedTime = known.UpdatedAt, known.UpdatedTime
	return n
}
