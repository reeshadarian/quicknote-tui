package main

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ttlModel(t *testing.T) (model, *SQLiteStore, *SQLiteStore, Note, Note) {
	m, a, b, n, other := asyncModel(t)
	m.startupPending = false
	m.clock = fixedClock{lifecycleNow()}
	m.timerNow = lifecycleNow()
	m.flashMsg = ""
	return m, a, b, n, other
}
func ttlReview(t *testing.T, m model, index int) model {
	m = updateTest(t, m, ActionMsg{"ttl.set"})
	for i := 0; i < index; i++ {
		m = updateTest(t, m, keyMessage("down"))
	}
	return updateTest(t, m, keyMessage("enter"))
}
func TestQN029PickerCancelNoMutation(t *testing.T) {
	m, a, _, n, _ := ttlModel(t)
	m = typeTest(m, " dirty")
	before := m.EditorDraft.Clone()
	m = ttlReview(t, m, 0)
	if m.state != stateConfirm || m.confirmation.Confirm {
		t.Fatal("review must default Cancel")
	}
	m = updateTest(t, m, keyMessage("enter"))
	if m.state != stateTTLPicker {
		t.Fatal("cancel lost picker parent")
	}
	m = updateTest(t, m, keyMessage("esc"))
	if !reflect.DeepEqual(before, m.EditorDraft.Clone()) {
		t.Fatal("picker cancel changed draft")
	}
	stored, e := a.GetNote(n.ID)
	if e != nil || stored.Revision != n.Revision {
		t.Fatal(stored, e)
	}
}
func TestQN029ConfirmTimeAndDirtySave(t *testing.T) {
	m, a, _, n, _ := ttlModel(t)
	m = typeTest(m, " dirty")
	text := m.buffer.Text()
	m = ttlReview(t, m, 0)
	final := lifecycleNow().Add(2 * time.Hour)
	m.clock = fixedClock{final}
	m.timerNow = final
	if !strings.Contains(m.View(), final.Add(15*time.Minute).In(time.Local).Format("2006-01-02 15:04:05")) {
		t.Fatal("stale relative preview", m.View())
	}
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	stored, e := a.GetNote(n.ID)
	at, _ := ParseLifecycleTime(stored.ExpiresAt.String)
	if e != nil || stored.Content != text || !at.Equal(final.Add(15*time.Minute)) || stored.Revision != n.Revision+2 || m.state != stateTyping || m.needsSave {
		t.Fatal(stored, e, m.state, m.needsSave)
	}
}
func TestQN029StaleTTLResult(t *testing.T) {
	m, a, _, n, other := ttlModel(t)
	m = ttlReview(t, m, 0)
	m = updateTest(t, m, keyMessage("tab"))
	m, cmd := rawUpdate(m, keyMessage("enter"))
	if cmd == nil {
		t.Fatal("no metadata command")
	}
	result := cmd().(lifecycleResult)
	m = typeTest(m, " newer")
	text := m.buffer.Text()
	undo := len(m.undoStack)
	m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
	active := m.buffer.Text()
	m = updateTest(t, m, result)
	if m.activeID() != other.ID || m.buffer.Text() != active {
		t.Fatal("late result crossed notes")
	}
	d := m.drafts[n.ID]
	if d.buffer.Text() != text || len(d.undoStack) != undo || !d.Note.ExpiresAt.Valid {
		t.Fatal("owner snapshot/history lost")
	}
	stored, e := a.GetNote(n.ID)
	if e != nil || stored.Content != text {
		t.Fatal(stored, e)
	}
}
func TestQN029TTLStatusHitRegion(t *testing.T) {
	m, _, _, _, _ := ttlModel(t)
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
		m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		r := m.ttlStatusRegion()
		if size[0] <= 2 {
			if r.Width != 0 {
				t.Fatal("tiny hit region", r)
			}
			continue
		}
		if r.ActionID != "ttl.set" || r.Width <= 0 || r.Y != size[1]-1 || r.X+r.Width > size[0] {
			t.Fatal(size, r)
		}
		clicked := updateTest(t, m, tea.MouseMsg{X: r.X, Y: r.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if clicked.state != stateTTLPicker {
			t.Fatal("status click did not route")
		}
		outside := updateTest(t, m, tea.MouseMsg{X: r.X + r.Width, Y: r.Y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if outside.state == stateTTLPicker {
			t.Fatal("offscreen/unrendered hit")
		}
	}
}
func TestQN029T25T27PickerLayoutAndKeymap(t *testing.T) {
	for _, profile := range []string{"default", "legacy"} {
		m, _, _, _, _ := ttlModel(t)
		m.keymap = defaultKeymap(profile)
		m.keymap.Bindings["ttl.set"] = []string{"alt+e"}
		if e := m.keymap.Validate(); e != nil {
			t.Fatal(e)
		}
		m = updateTest(t, m, keyMessage("alt+e"))
		if m.state != stateTTLPicker {
			t.Fatal("custom shortcut")
		}
		m = updateTest(t, m, keyMessage("esc"))
		m = updateTest(t, m, ActionMsg{"commands.open"})
		for i, a := range m.commandActions() {
			if a.ID == "ttl.set" {
				m.commandIndex = i
			}
		}
		m = updateTest(t, m, keyMessage("enter"))
		if m.state != stateTTLPicker {
			t.Fatal("Commands route")
		}
		for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
			m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			v := m.View()
			if lipgloss.Width(v) > size[0] || lipgloss.Height(v) > size[1] {
				t.Fatal(size, v)
			}
			if size[0] >= 25 && (!strings.Contains(strings.ToLower(v), "esc") || !strings.Contains(v, "> 15 minutes")) {
				t.Fatal("hidden choice/cancel", size, v)
			}
			t.Logf("picker %dx%d:\n%s", size[0], size[1], v)
		}
		m = updateTest(t, m, tea.WindowSizeMsg{Width: 25, Height: 5})
		m = updateTest(t, m, keyMessage("enter"))
		v := m.View()
		if !strings.Contains(strings.ToLower(v), "esc") || !strings.Contains(v, "Cancel") {
			t.Fatal(v)
		}
		t.Logf("confirm 25x5:\n%s", v)
	}
	k := defaultKeymap("default")
	k.Bindings["ttl.cancel"] = []string{"ctrl+c"}
	if k.Validate() == nil {
		t.Fatal("unreachable Esc accepted")
	}
	k.Bindings = map[string][]string{"ttl.down": {"up"}}
	if k.Validate() == nil {
		t.Fatal("modal collision accepted")
	}
}
func TestQN029PinnedGhostTrashAndReview(t *testing.T) {
	m, _, _, _, _ := ttlModel(t)
	a, _ := findAction("ttl.set")
	for _, n := range []Note{{ID: 0, IsGhost: true}, {ID: 2, DeletedAt: "trashed"}, {ID: 0}} {
		if enabled, _ := a.Enabled(ActionContext{State: stateTyping, Note: n}); enabled {
			t.Fatal("enabled invalid owner", n)
		}
	}
	m.Note.IsPinned = true
	m = ttlReview(t, m, 0)
	if m.state != stateTTLPicker || !strings.Contains(m.View(), "Unpin") {
		t.Fatal("no pinned explanation", m.View())
	}
	m = updateTest(t, m, keyMessage("esc"))
	m.Note.ExpirationNeedsReview = true
	if !strings.Contains(m.lifecycleStatus(), "Pinned") || !strings.Contains(m.lifecycleStatus(), "review") {
		t.Fatal(m.lifecycleStatus())
	}
}
func TestQN029TTLConflictAndStaleConfirmation(t *testing.T) {
	for _, change := range []string{"edit", "switch", "expire", "conflict"} {
		t.Run(change, func(t *testing.T) {
			m, a, b, n, other := ttlModel(t)
			at := lifecycleNow().Add(time.Hour)
			n, e := a.ApplyExpirationCAS(n.ID, n.Revision, ExpirationChoice{Deadline: &at, Source: ExpirationPreset}, lifecycleNow())
			if e != nil {
				t.Fatal(e)
			}
			m.Note = n
			m.syncSummary()
			m = ttlReview(t, m, 0)
			m = updateTest(t, m, keyMessage("tab"))
			confirmation := m.confirmation
			switch change {
			case "edit":
				m.buffer = stringToBuffer("newest")
				m.needsSave = true
				m.localVersion++
			case "switch":
				m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
			case "expire":
				expired, e := a.ExpireNoteCAS(n.ID, n.Revision, at)
				if e != nil {
					t.Fatal(e)
				}
				m.maintenanceGeneration = 1
				m = updateTest(t, m, maintenanceResult{Generation: 1, Result: MaintenanceResult{Expired: []LifecycleChange{{ID: n.ID, Revision: expired.Revision, DeletedAt: lifecycleNow(), Reason: DeleteExpired}}}})
			case "conflict":
				n.Content = "external"
				if _, e = b.Save(n); e != nil {
					t.Fatal(e)
				}
			}
			text := m.buffer.Text()
			if change == "switch" {
				m = drainTestCmd(t, m, m.confirmTTL(confirmation))
			} else {
				m = updateTest(t, m, keyMessage("enter"))
			}
			stored, e := a.GetNote(n.ID)
			if e != nil || stored.ExpiresAt.String != FormatLifecycleTime(at) || m.buffer.Text() != text {
				t.Fatal("stale intent/deadline/text", stored, e)
			}
			if change == "conflict" && !errors.Is(m.saveErr, ErrConflict) {
				t.Fatal("conflict invisible", m.saveErr)
			}
		})
	}
}
func TestQN029T28PurePersistentStatus(t *testing.T) {
	m, _, _, _, _ := ttlModel(t)
	at := lifecycleNow().Add(time.Hour)
	m.Note.ExpiresAt = sql.NullString{Valid: true, String: FormatLifecycleTime(at)}
	m.saveErr = fmt.Errorf("disk failed")
	m.flashMsg = "Copied!"
	m = updateTest(t, m, tea.WindowSizeMsg{Width: 25, Height: 5})
	before := m.EditorDraft.Clone()
	v := m.View()
	if !strings.Contains(v, "Save failed") || !reflect.DeepEqual(before, m.EditorDraft.Clone()) || v != m.View() {
		t.Fatal("persistent mono status/purity", v)
	}
	m.timerNow = m.timerNow.Add(2 * time.Hour)
	if !strings.Contains(m.lifecycleStatus(), "Expires") || m.Note.ExpiresAt.String != FormatLifecycleTime(at) {
		t.Fatal("jump changed deadline")
	}
}

func TestQN029CustomAndModalRegistry(t *testing.T) {
	m, a, _, n, _ := ttlModel(t)
	m = updateTest(t, m, ActionMsg{"ttl.set"})
	for i := 0; i < 6; i++ {
		m = updateTest(t, m, ActionMsg{"ttl.down"})
	}
	if m.ttlPicker.Index != 6 {
		t.Fatal("typed modal action not dispatched", m.ttlPicker.Index)
	}
	m = updateTest(t, m, ActionMsg{"ttl.review"})
	if m.state != stateTTLCustom {
		t.Fatal(m.state)
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0m")})
	m = updateTest(t, m, keyMessage("enter"))
	if m.state != stateTTLCustom || m.ttlPicker.Error == "" {
		t.Fatal("invalid custom accepted")
	}
	m.ttlPicker.Input.SetValue("2026-11-01T01:30:00-07:00")
	m = updateTest(t, m, keyMessage("enter"))
	if m.state != stateConfirm || !strings.Contains(m.View(), "2026-11-01") {
		t.Fatal(m.View())
	}
	m = updateTest(t, m, keyMessage("esc"))
	if m.state != stateTTLCustom || m.ttlPicker.Input.Value() != "2026-11-01T01:30:00-07:00" {
		t.Fatal("custom parent input lost")
	}
	m = updateTest(t, m, keyMessage("enter"))
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	stored, e := a.GetNote(n.ID)
	if e != nil || stored.ExpiresAt.String != "2026-11-01T08:30:00.000Z" || m.state != stateTyping {
		t.Fatal(stored, e, m.state)
	}
	m = ttlReview(t, m, 5)
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	stored, e = a.GetNote(n.ID)
	if e != nil || stored.ExpiresAt.Valid || stored.ExpirationSource != ExpirationNone || stored.Revision != n.Revision+2 {
		t.Fatal(stored, e)
	}
}
func TestQN029QueuedTTLInvalidatedByEditOrSwitch(t *testing.T) {
	for _, change := range []string{"edit", "switch"} {
		t.Run(change, func(t *testing.T) {
			m, a, _, n, other := ttlModel(t)
			m = typeTest(m, " dirty")
			m = ttlReview(t, m, 0)
			m = updateTest(t, m, keyMessage("tab"))
			m, cmd := rawUpdate(m, keyMessage("enter"))
			saved := cmd().(saveResult)
			if change == "edit" {
				m = typeTest(m, " newer")
			} else {
				m = drainTestCmd(t, m, m.requestOpen(other.ID, false, nil, false))
			}
			m = updateTest(t, m, saved)
			stored, e := a.GetNote(n.ID)
			if e != nil || stored.ExpiresAt.Valid {
				t.Fatal("stale queued TTL wrote", stored, e)
			}
		})
	}
}
func TestQN029ConfirmationTickPreviewAndUnboundModal(t *testing.T) {
	m, _, _, _, _ := ttlModel(t)
	m = ttlReview(t, m, 0)
	final := lifecycleNow().Add(-30 * time.Minute)
	m.clock = fixedClock{final}
	m.lastMaintenance = final
	m = updateTest(t, m, tickMsg(final))
	if !strings.Contains(m.View(), final.Add(15*time.Minute).In(time.Local).Format("2006-01-02 15:04:05")) {
		t.Fatal("clock-jump preview stale", m.View())
	}
	m = updateTest(t, m, keyMessage("esc"))
	m.keymap = defaultKeymap("default")
	m.keymap.Bindings["ttl.down"] = []string{"ctrl+j"}
	m = updateTest(t, m, keyMessage("down"))
	if m.ttlPicker.Index != 0 {
		t.Fatal("rebound old modal key still active")
	}
	m = updateTest(t, m, keyMessage("ctrl+j"))
	if m.ttlPicker.Index != 1 {
		t.Fatal("custom modal binding inactive")
	}
	m = updateTest(t, m, ActionMsg{"ttl.down"})
	if m.ttlPicker.Index != 2 {
		t.Fatal("typed action depends on configured chord")
	}
}

func TestQN029ConfirmationAndCustomLayout(t *testing.T) {
	m, _, _, _, _ := ttlModel(t)
	m = updateTest(t, m, keyMessage("alt+t"))
	if m.state != stateTTLPicker {
		t.Fatal("default shortcut")
	}
	m.ttlPicker.Index = 6
	m = updateTest(t, m, keyMessage("enter"))
	m.ttlPicker.Input.SetValue("2h")
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
		m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := m.View()
		if lipgloss.Width(v) > size[0] || lipgloss.Height(v) > size[1] {
			t.Fatal(size, v)
		}
		if size[0] >= 25 && !strings.Contains(strings.ToLower(v), "esc") {
			t.Fatal("missing cancel", v)
		}
	}
	m = updateTest(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updateTest(t, m, keyMessage("enter"))
	before := m.EditorDraft.Clone()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {50, 12}, {25, 5}, {1, 1}, {2, 2}} {
		m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		v := m.View()
		if lipgloss.Width(v) > size[0] || lipgloss.Height(v) > size[1] {
			t.Fatal(size, v)
		}
		if size[0] >= 25 && (!strings.Contains(v, "Esc") || !strings.Contains(v, "> Cancel")) {
			t.Fatal("missing cancel/focus", v)
		}
		m = updateTest(t, m, tea.MouseMsg{X: 0, Y: 0, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
		if !reflect.DeepEqual(before, m.EditorDraft.Clone()) {
			t.Fatal("modal mouse changed draft")
		}
	}
}
func TestQN029MetadataPendingStatus(t *testing.T) {
	m, _, _, _, _ := ttlModel(t)
	m = ttlReview(t, m, 0)
	m = updateTest(t, m, keyMessage("tab"))
	m, cmd := rawUpdate(m, keyMessage("enter"))
	if m.saveStatus() != "Saving…" {
		t.Fatal("metadata shown Saved", m.saveStatus())
	}
	m = drainTestCmd(t, m, cmd)
	if strings.Contains(m.flashMsg, "Setting expiration") {
		t.Fatal("stale pending toast after success")
	}
}

func TestQN029ReviewBracketedPasteAndCallbacks(t *testing.T) {
	m, a, _, n, _ := ttlModel(t)
	m = updateTest(t, m, ActionMsg{"ttl.set"})
	m.ttlPicker.Index = 6
	m = updateTest(t, m, keyMessage("enter"))
	value := "2026-11-01T01:30:00-07:00"
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value), Paste: true})
	if m.ttlPicker.Input.Value() != value {
		t.Fatalf("paste bytes changed: %q", m.ttlPicker.Input.Value())
	}
	// Generate the library's private clipboard-error callback with clipboard I/O
	// disabled, then verify it reaches this input rather than the editor.
	unsupported := clipboard.Unsupported
	clipboard.Unsupported = true
	callback := textinput.Paste()
	clipboard.Unsupported = unsupported
	m, _ = rawUpdate(m, callback)
	if m.ttlPicker.Input.Err == nil {
		t.Fatal("clipboard callback not forwarded")
	}
	m, _ = rawUpdate(m, tea.BlurMsg{})
	if !m.ttlPicker.Input.Cursor.Blink {
		t.Fatal("blur callback not forwarded")
	}
	m, _ = rawUpdate(m, tea.FocusMsg{})
	if m.ttlPicker.Input.Cursor.Blink {
		t.Fatal("focus callback not forwarded")
	}
	_, cmd := rawUpdate(m, cursor.Blink())
	if cmd == nil {
		t.Fatal("blink callback not forwarded")
	}
	m = updateTest(t, m, keyMessage("enter"))
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	stored, e := a.GetNote(n.ID)
	if e != nil || stored.ExpiresAt.String != "2026-11-01T08:30:00.000Z" || stored.Content != n.Content {
		t.Fatal(stored, e)
	}
}
func TestQN029ReviewSecondConfirmedChoiceSerialized(t *testing.T) {
	for _, holdResult := range []bool{false, true} {
		t.Run(fmt.Sprint(holdResult), func(t *testing.T) {
			m, a, _, n, _ := ttlModel(t)
			m = ttlReview(t, m, 0)
			m = updateTest(t, m, keyMessage("tab"))
			m, first := rawUpdate(m, keyMessage("enter"))
			var result tea.Msg
			if holdResult {
				result = first()
			}
			m = ttlReview(t, m, 5)
			if m.state != stateConfirm {
				t.Fatal("second choice not reviewed")
			}
			m = updateTest(t, m, keyMessage("tab"))
			m = updateTest(t, m, keyMessage("enter"))
			if holdResult {
				m = updateTest(t, m, result)
			} else {
				m = drainTestCmd(t, m, first)
			}
			stored, e := a.GetNote(n.ID)
			if e != nil || stored.ExpiresAt.Valid || stored.ExpirationSource != ExpirationNone || stored.Revision != n.Revision+2 || stored.Content != n.Content || m.state != stateTyping || m.flashMsg != "Expiration updated" {
				t.Fatal("second confirmed Never was not applied", stored, e, m.flashMsg)
			}
		})
	}
}
func TestQN029ReviewLongCustomFocusAfterTypingPasteResize(t *testing.T) {
	value := strings.Repeat("1m", 59) + "2h"
	for _, paste := range []bool{false, true} {
		t.Run(fmt.Sprint(paste), func(t *testing.T) {
			m, _, _, _, _ := ttlModel(t)
			m = updateTest(t, m, tea.WindowSizeMsg{Width: 25, Height: 5})
			m = updateTest(t, m, ActionMsg{"ttl.set"})
			m.ttlPicker.Index = 6
			m = updateTest(t, m, keyMessage("enter"))
			if paste {
				m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value), Paste: true})
			} else {
				for _, r := range value {
					m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				}
			}
			for _, size := range [][2]int{{25, 5}, {120, 40}, {80, 24}, {50, 12}, {25, 5}} {
				m = updateTest(t, m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				v := m.View()
				if m.ttlPicker.Input.Value() != value || m.ttlPicker.Input.Position() != len(value) || !strings.Contains(v, "2h") || lipgloss.Width(m.ttlPicker.Input.View()) > size[0] {
					t.Fatalf("input value/focus hidden %v: %q\n%s", size, m.ttlPicker.Input.Value(), v)
				}
			}
			if _, e := ResolveTTLInput(m.ttlPicker.Input.Value(), m.timerNow, time.Local); e != nil {
				t.Fatal(e)
			}
		})
	}
}
