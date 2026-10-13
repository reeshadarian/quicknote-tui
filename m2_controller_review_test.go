package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestM2ReviewMaintenancePreservesConfirmedIntent(t *testing.T) {
	for _, action := range []string{"never", "pin", "ghost", "canceled"} {
		t.Run(action, func(t *testing.T) {
			m, s, n := lifecycleModel(t)
			m.clock = fixedClock{lifecycleNow().Add(30 * time.Second)}
			m = typeTest(m, " newest")
			var save tea.Cmd
			switch action {
			case "pin":
				m, save = rawUpdate(m, ActionMsg{"note.pin"})
			case "ghost":
				m = updateTest(t, m, keyMessage("ctrl+g"))
				m = updateTest(t, m, keyMessage("tab"))
				m, save = rawUpdate(m, keyMessage("enter"))
			default:
				m = ttlReview(t, m, 5)
				m = updateTest(t, m, keyMessage("tab"))
				m, save = rawUpdate(m, keyMessage("enter"))
			}
			if save == nil {
				t.Fatal("missing prerequisite save")
			}
			captured := m.buffer.Text()
			if action == "canceled" {
				m = typeTest(m, " after confirmation")
			}
			text, history := m.buffer.Text(), len(m.undoStack)
			m.clock = fixedClock{lifecycleNow().Add(time.Minute)}
			s.clock = m.clock
			m = drainTestCmd(t, m, m.requestMaintenance())
			m = drainTestCmd(t, m, save)
			got, err := s.GetNote(n.ID)
			if err != nil || got.Content != text || got.DeletedAt != "" {
				t.Fatal("maintenance replaced confirmed intent or lost saved text", got, err)
			}
			if len(m.lifecycleQueued) != 0 || len(m.lifecycleRequests) != 0 || len(m.saves) != 0 {
				t.Fatal("intent did not settle")
			}
			d := m.drafts[n.ID]
			if d.buffer.Text() != text || len(d.undoStack) != history {
				t.Fatal("source history changed")
			}
			switch action {
			case "never":
				if got.ExpiresAt.Valid || got.ExpirationSource != ExpirationNone || got.Revision != n.Revision+2 {
					t.Fatal("Never did not win", got)
				}
			case "pin":
				if !got.IsPinned || got.ExpiresAt.Valid || got.Revision != n.Revision+2 {
					t.Fatal("pin did not win", got)
				}
			case "ghost":
				if !m.Note.IsGhost || m.buffer.Text() != captured || got.Revision != n.Revision+1 {
					t.Fatal("captured ghost lost")
				}
			case "canceled":
				if !strings.Contains(m.flashMsg, "Expiration canceled") {
					t.Fatal("missing cancellation feedback", m.flashMsg)
				}
			}
			// A skipped sweep is not permanent protection: ghost originals and
			// canceled intents still expire at the next ordinary maintenance pass.
			m.clock = fixedClock{lifecycleNow().Add(90 * time.Second)}
			s.clock = m.clock
			m = drainTestCmd(t, m, m.requestMaintenance())
			got, err = s.GetNote(n.ID)
			wantExpired := action == "ghost" || action == "canceled"
			if err != nil || (got.DeleteReason == DeleteExpired) != wantExpired || got.Content != text {
				t.Fatal("subsequent maintenance lost protection or starved expiry", got, err)
			}
			if action == "ghost" && (!m.Note.IsGhost || m.buffer.Text() != captured) {
				t.Fatal("source expiry changed ghost")
			}
		})
	}
}

func TestM2ReviewSupersededAckSettlesConfirmedNever(t *testing.T) {
	for _, continuation := range []string{"search", "navigate", "exit", "dirty navigate", "other owner"} {
		t.Run(continuation, func(t *testing.T) {
			m, a, b, n, other := ttlModel(t)
			m = ttlReview(t, m, 0)
			m = updateTest(t, m, keyMessage("tab"))
			m, cmd := rawUpdate(m, keyMessage("enter"))
			first := cmd().(lifecycleResult)
			if first.Err != nil {
				t.Fatal(first.Err)
			}
			m = ttlReview(t, m, 5)
			m = updateTest(t, m, keyMessage("tab"))
			m = updateTest(t, m, keyMessage("enter"))
			remote := first.Note
			remote.Content = "remote newer content"
			remote, err := b.Save(remote)
			if err != nil {
				t.Fatal(err)
			}
			m = updateTest(t, m, keyMessage("ctrl+f"))
			if len(m.searchResults) == 0 {
				t.Fatal("public search did not observe remote write")
			}
			var otherSave tea.Cmd
			if continuation != "search" {
				m = updateTest(t, m, keyMessage("esc"))
				switch continuation {
				case "navigate":
					m = updateTest(t, m, keyMessage("ctrl+n"))
				case "exit":
					m = updateTest(t, m, keyMessage("esc"))
				case "dirty navigate":
					m = typeTest(m, " local retained")
					m = updateTest(t, m, keyMessage("ctrl+n"))
				case "other owner":
					m = updateTest(t, m, keyMessage("alt+right"))
					m = typeTest(m, " other dirty")
					m, otherSave = rawUpdate(m, keyMessage("ctrl+n"))
				}
			}
			text, history, base := m.buffer.Text(), len(m.undoStack), m.Note.Revision
			m, cmd = rawUpdate(m, first)
			if continuation == "exit" {
				if cmd == nil {
					t.Fatal("superseded acknowledgment stranded exit")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("expected completed exit")
				}
			} else {
				m = drainTestCmd(t, m, cmd)
			}
			if _, queued := m.lifecycleQueued[n.ID]; queued {
				t.Fatal("confirmed Never orphaned after superseded acknowledgment")
			}
			got, err := a.GetNote(n.ID)
			if err != nil || got.Content != remote.Content || got.Revision != remote.Revision || !got.ExpiresAt.Valid {
				t.Fatal("cancellation changed remote snapshot", got, err)
			}
			switch continuation {
			case "search":
				if m.state != stateSearch || m.buffer.Text() != text || m.Note.Revision != base || !strings.Contains(m.flashMsg, "canceled") || !strings.Contains(m.flashMsg, "retry") {
					t.Fatal("missing recoverable cancellation or changed old content/base", m.flashMsg)
				}
				// Public opening still obtains a complete content/revision pair.
				for i, hit := range m.searchResults {
					if hit.ID == n.ID {
						m.searchIndex = i
					}
				}
				m = updateTest(t, m, keyMessage("enter"))
				if m.buffer.Text() != remote.Content || m.Note.Revision != remote.Revision {
					t.Fatal("open paired stale content with new revision")
				}
				m = ttlReview(t, m, 5)
				m = updateTest(t, m, keyMessage("tab"))
				m = updateTest(t, m, keyMessage("enter"))
				got, err = a.GetNote(n.ID)
				if err != nil || got.ExpiresAt.Valid || got.Content != remote.Content || got.Revision != remote.Revision+1 {
					t.Fatal("explicit retry did not apply Never", got, err)
				}
			case "navigate":
				if m.activeID() != 0 || m.pendingAction != nil {
					t.Fatal("matching navigation stranded")
				}
			case "dirty navigate":
				if m.activeID() != n.ID || m.buffer.Text() != text || m.Note.Revision != base || !m.needsSave || !errors.Is(m.saveErr, ErrConflict) || len(m.undoStack) != history {
					t.Fatal("newest draft/CAS recovery lost", m.saveErr)
				}
			case "other owner":
				if m.activeID() != other.ID || m.pendingAction == nil || m.pendingActionID != other.ID || len(m.saves) != 1 || m.buffer.Text() != text {
					t.Fatal("superseded source consumed another owner's continuation")
				}
				m = drainTestCmd(t, m, otherSave)
				if m.activeID() != 0 || m.pendingAction != nil {
					t.Fatal("other navigation did not complete")
				}
			}
		})
	}
}

func TestM2ReviewCanceledSaveQueueFeedback(t *testing.T) {
	for _, action := range []string{"stay", "navigate", "exit", "ordinary save", "valid TTL"} {
		t.Run(action, func(t *testing.T) {
			m, a, _, n, _ := ttlModel(t)
			m = typeTest(m, " first")
			var save tea.Cmd
			if action == "ordinary save" {
				save = m.requestSave()
			} else {
				m = ttlReview(t, m, 0)
				m = updateTest(t, m, keyMessage("tab"))
				m, save = rawUpdate(m, keyMessage("enter"))
				if action != "valid TTL" {
					m = typeTest(m, " invalidates TTL")
				}
			}
			text := m.buffer.Text()
			if action == "navigate" {
				m = updateTest(t, m, keyMessage("ctrl+n"))
			}
			if action == "exit" {
				m = updateTest(t, m, keyMessage("esc"))
			}
			m = drainTestCmd(t, m, save)
			got, err := a.GetNote(n.ID)
			if err != nil || got.Content != text || got.ExpiresAt.Valid != (action == "valid TTL") || m.pendingAction != nil || len(m.saves) != 0 || len(m.lifecycleQueued) != 0 {
				t.Fatal("content/continuation lost", got, err)
			}
			if action == "ordinary save" {
				if m.flashMsg != "Saved" {
					t.Fatal("normal save feedback lost", m.flashMsg)
				}
			} else if action == "valid TTL" {
				if m.flashMsg != "Expiration updated" {
					t.Fatal("normal metadata completion feedback lost", m.flashMsg)
				}
			} else if !strings.Contains(m.flashMsg, "Expiration canceled") {
				t.Fatal("Saved replaced actionable cancellation", m.flashMsg)
			}
			if action == "navigate" && m.activeID() != 0 {
				t.Fatal("navigation did not finish")
			}
		})
	}
}

func TestM2ReviewZeroRevisionPurgeOwnership(t *testing.T) {
	for _, interleave := range []string{"clean", "maintenance", "refetch", "dirty", "version", "revision", "load error"} {
		t.Run(interleave, func(t *testing.T) {
			m, a, _, n := trashModel(t)
			if _, err := a.db.Exec("UPDATE notes SET revision=0 WHERE id=?", n.ID); err != nil {
				t.Fatal(err)
			}
			m.Note.Revision = 0
			for _, list := range [][]Note{m.notes, m.allNotes, m.searchResults} {
				for i := range list {
					if list[i].ID == n.ID {
						list[i].Revision = 0
					}
				}
			}
			delete(m.drafts, n.ID)
			m.undoStack = []textState{{content: "source history"}}
			var next Note
			if interleave == "refetch" {
				var err error
				next, err = a.Save(Note{Content: "next old"})
				if err != nil {
					t.Fatal(err)
				}
				next, err = a.TrashSnapshotCAS(next.ID, next.Revision, DeleteManual, lifecycleNow())
				if err != nil {
					t.Fatal(err)
				}
			}
			m = updateTest(t, m, ActionMsg{"trash.deleteForever"})
			m = updateTest(t, m, keyMessage("tab"))
			m, cmd := rawUpdate(m, keyMessage("enter"))
			m, cmd = rawUpdate(m, cmd())
			held := cmd().(loadResult)
			if interleave == "maintenance" || interleave == "refetch" {
				m = updateTest(t, m, maintenanceResult{Generation: m.maintenanceGeneration})
			}
			switch interleave {
			case "refetch":
				if _, err := a.db.Exec("UPDATE notes SET content='next fresh',revision=revision+1 WHERE id=?", next.ID); err != nil {
					t.Fatal(err)
				}
				next, _ = a.GetNote(next.ID)
				summary := next
				summary.Content = ""
				m.searchResults = []Note{summary}
			case "dirty":
				m.needsSave = true
			case "version":
				m.localVersion++
			case "revision":
				m.Note.Revision++
			case "load error":
				held.Err = errors.New("synthetic read failure")
			}
			m = updateTest(t, m, held)
			_, retained := m.drafts[n.ID]
			clean := interleave == "clean" || interleave == "maintenance" || interleave == "refetch"
			if clean && retained {
				t.Fatal("revision-zero clean deleted source restashed")
			}
			if !clean && m.activeID() == n.ID && (m.buffer.Text() != n.Content || len(m.undoStack) != 1) {
				t.Fatal("newer/error source recovery lost")
			}
			if interleave == "revision" && (!retained || len(m.drafts[n.ID].undoStack) != 1) {
				t.Fatal("newer revision/history forgotten")
			}
			if interleave == "refetch" && (m.Note.Revision != next.Revision || m.buffer.Text() != next.Content) {
				t.Fatal("refetch content/revision mismatch")
			}
			if _, err := a.GetNote(n.ID); !errors.Is(err, ErrNotFound) {
				t.Fatal("selected deletion did not commit", err)
			}
		})
	}
}
