package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func asyncModel(t *testing.T) (model, *SQLiteStore, *SQLiteStore, Note, Note) {
	t.Helper()
	a, b := twoStores(t)
	first, err := a.Save(Note{Content: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Save(Note{Content: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	m := initialModel(a.db, nowForTest())
	m.store = a
	m.notes = []Note{first, second}
	m.allNotes = append([]Note(nil), m.notes...)
	m.index = 0
	m.activateNote()
	m.highlightDirty = false
	return m, a, b, first, second
}
func rawUpdate(m model, msg tea.Msg) (model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(model), cmd
}
func typeTest(m model, text string) model {
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text)})
	return m
}

// T-02: A's save and an older B highlight complete after B becomes active.
func TestQN012T02SaveAndHighlightNeverCrossNotes(t *testing.T) {
	m, a, _, first, second := asyncModel(t)
	m = typeTest(m, "A draft")
	textA := bufferToString(m.buffer)
	save := m.requestSave()
	load := m.requestOpen(second.ID, false, nil, false)
	m, _ = rawUpdate(m, load())
	if m.activeID() != second.ID {
		t.Fatal("B did not open")
	}
	staleHighlight := m.requestHighlight()()
	m = typeTest(m, "new B")
	before := bufferToString(m.buffer)
	version := m.localVersion
	m, _ = rawUpdate(m, save())
	m, _ = rawUpdate(m, staleHighlight)
	if m.activeID() != second.ID || bufferToString(m.buffer) != before || !m.needsSave || m.localVersion != version || m.saveErr != nil {
		t.Fatal("late A result affected B draft/status")
	}
	if m.cachedHighlight != before {
		t.Fatal("old B highlight replaced newer text")
	}
	got, err := a.GetNote(first.ID)
	if err != nil || got.Content != textA {
		t.Fatalf("A save lost: %+v %v", got, err)
	}
	if d := m.drafts[first.ID]; d.needsSave || d.Note.Revision != first.Revision+1 {
		t.Fatal("A session not reconciled")
	}
}

func TestQN012SaveKeepsNewEditsDirtyAndChainsCAS(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m = typeTest(m, "one")
	firstVersion := m.localVersion
	oldText := bufferToString(m.buffer)
	cmd := m.requestSave()
	m = typeTest(m, "two")
	newest := bufferToString(m.buffer)
	result := cmd().(saveResult)
	if result.Request.LocalVersion != firstVersion || result.Request.Snapshot.Content != oldText {
		t.Fatal("save did not capture immutable snapshot")
	}
	m, next := rawUpdate(m, result)
	if !m.needsSave || bufferToString(m.buffer) != newest || m.notes[m.index].Revision != first.Revision+1 || m.saveStatus() != "Saving…" {
		t.Fatal("newer draft falsely marked saved or overwritten")
	}
	m = drainTestCmd(t, m, next)
	got, err := a.GetNote(first.ID)
	if err != nil || got.Content != newest || got.Revision != first.Revision+2 || m.needsSave {
		t.Fatalf("followup CAS lost draft: %+v %v", got, err)
	}
}

func TestQN012OldSaveCompletionCannotAcknowledgeNewRequest(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = typeTest(m, "one")
	old := m.requestSave()().(saveResult)
	m = typeTest(m, "two")
	m, _ = rawUpdate(m, old)
	pending := m.saves[m.activeID()]
	version := m.localVersion
	m, _ = rawUpdate(m, old)
	if m.saves[m.activeID()].Request != pending.Request || m.localVersion != version || !m.needsSave {
		t.Fatal("duplicate completion consumed new request")
	}
}

func TestQN012InactiveConflictPreservesSession(t *testing.T) {
	m, _, b, first, second := asyncModel(t)
	m = typeTest(m, "local")
	local := bufferToString(m.buffer)
	save := m.requestSave()
	m, _ = rawUpdate(m, m.requestOpen(second.ID, false, nil, false)())
	remote := first
	remote.Content = "remote"
	if _, err := b.Save(remote); err != nil {
		t.Fatal(err)
	}
	m, _ = rawUpdate(m, save())
	if m.activeID() != second.ID || m.state != stateTyping || m.needsSave || m.saveErr != nil {
		t.Fatal("inactive conflict changed B save state")
	}
	d := m.drafts[first.ID]
	if !d.needsSave || !errors.Is(d.saveErr, ErrConflict) || d.buffer.Text() != local || d.Note.Revision != first.Revision {
		t.Fatal("inactive conflict lost base/draft")
	}
	m, _ = rawUpdate(m, m.requestOpen(first.ID, false, nil, false)())
	if !m.needsSave || !errors.Is(m.saveErr, ErrConflict) || bufferToString(m.buffer) != local {
		t.Fatal("failed draft not restored")
	}
}

func TestQN012LoadRejectsOutOfOrderAndEditedSource(t *testing.T) {
	m, _, _, first, second := asyncModel(t)
	old := m.requestOpen(first.ID, false, nil, false)()
	latest := m.requestOpen(second.ID, false, nil, false)()
	m, _ = rawUpdate(m, latest)
	m, _ = rawUpdate(m, old)
	if m.activeID() != second.ID {
		t.Fatal("old load replaced latest target")
	}
	cmd := m.requestOpen(first.ID, false, nil, false)
	m = typeTest(m, "keep me")
	before := bufferToString(m.buffer)
	m, _ = rawUpdate(m, cmd())
	if m.activeID() != second.ID || bufferToString(m.buffer) != before || !m.needsSave || m.loadPending {
		t.Fatal("load discarded edit made while worker ran")
	}
}

func TestQN012LoadCannotRegressAcknowledgedSessionRevision(t *testing.T) {
	m, _, _, first, second := asyncModel(t)
	m = typeTest(m, "updated")
	text := bufferToString(m.buffer)
	save := m.requestSave()
	m, _ = rawUpdate(m, m.requestOpen(second.ID, false, nil, false)())
	// Fetch A before its worker commits, but deliver this load afterward.
	oldLoad := m.requestOpen(first.ID, false, nil, false)()
	m, _ = rawUpdate(m, save())
	// A newer retained revision now triggers a fresh, complete snapshot read.
	m = updateTest(t, m, oldLoad)
	if bufferToString(m.buffer) != text || m.notes[m.index].Revision != first.Revision+1 || m.needsSave {
		t.Fatal("old read regressed acknowledged save")
	}
}

func TestQN012HighlightUsesIdentityVersionAndGeneration(t *testing.T) {
	m, _, _, _, second := asyncModel(t)
	old := m.requestHighlight()().(highlightMsg)
	m.notes[1].Content = bufferToString(m.buffer)
	m.index = 1
	m.activateNote()
	m.highlightDirty = false
	m.cachedHighlight = "keep B"
	old.highlightedText = "wrong note"
	m, _ = rawUpdate(m, old)
	if m.activeID() != second.ID || m.cachedHighlight != "keep B" {
		t.Fatal("equal-text highlight crossed identities")
	}
	a := m.requestHighlight()().(highlightMsg)
	b := m.requestHighlight()().(highlightMsg)
	a.highlightedText = "old generation"
	b.highlightedText = "new generation"
	m, _ = rawUpdate(m, b)
	m, _ = rawUpdate(m, a)
	if m.cachedHighlight != "new generation" {
		t.Fatal("old same-text highlight crossed generations")
	}
	// Edit then undo to identical text still has a new local version.
	stale := m.requestHighlight()().(highlightMsg)
	m = typeTest(m, "x")
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	stale.highlightedText = "old local version"
	m, _ = rawUpdate(m, stale)
	if m.cachedHighlight == "old local version" {
		t.Fatal("old highlight accepted after undo")
	}
}

// T-24: all three searches are dispatched before their results are deliberately
// delivered in reverse order. Enter must select the ID from the newest result.
func TestQN012T24SearchOutOfOrderAndEnterIdentity(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	targets := make([]Note, 3)
	for i, text := range []string{"a only", "ab only", "abc only"} {
		n, err := a.Save(Note{Content: text})
		if err != nil {
			t.Fatal(err)
		}
		targets[i] = n
	}
	m.state = stateSearch
	m.prevState = stateTyping
	results := make([]searchResult, 3)
	for i, q := range []string{"a", "ab", "abc"} {
		m.searchInput.SetValue(q)
		debounce := m.requestSearch()()
		var cmd tea.Cmd
		m, cmd = rawUpdate(m, debounce)
		results[i] = cmd().(searchResult)
	}
	for _, i := range []int{2, 1, 0} {
		m, _ = rawUpdate(m, results[i])
	}
	if m.searchPending || len(m.searchResults) != 1 || m.searchResults[0].ID != targets[2].ID {
		t.Fatalf("stale query results displayed: %+v", m.searchResults)
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.activeID() != targets[2].ID || bufferToString(m.buffer) != targets[2].Content {
		t.Fatal("Enter opened stale result")
	}
}

func TestQN012DebounceSkipsSupersededSearchAndCancelInvalidatesLoad(t *testing.T) {
	m, _, _, _, second := asyncModel(t)
	m.state = stateSearch
	m.prevState = stateTyping
	m.searchInput.SetValue("a")
	old := m.requestSearch()()
	m.searchInput.SetValue("ab")
	m.requestSearch()
	_, cmd := rawUpdate(m, old)
	if cmd != nil {
		t.Fatal("superseded debounce dispatched SQL")
	}
	m.searchPending = false
	m.searchResults = []Note{second}
	m, cmd = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEnter})
	load := cmd()
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	id, text := m.activeID(), bufferToString(m.buffer)
	m, _ = rawUpdate(m, load)
	if m.activeID() != id || bufferToString(m.buffer) != text || m.state != stateTyping {
		t.Fatal("canceled search open applied late")
	}
}

func TestQN012SearchCancelDoesNotSaveDirtyDraft(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m = typeTest(m, "unsaved")
	text, version := bufferToString(m.buffer), m.localVersion
	m, cmd := rawUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	m = drainTestCmd(t, m, cmd)
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	stored, err := a.GetNote(first.ID)
	if err != nil || stored.Content != first.Content || stored.Revision != first.Revision {
		t.Fatal("opening search wrote editor draft")
	}
	if !m.needsSave || bufferToString(m.buffer) != text || m.localVersion != version {
		t.Fatal("cancel changed draft")
	}
}

func TestQN012SearchErrorsAreVisible(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	m.state = stateSearch
	m.prevState = stateTyping
	m.searchInput.SetValue("(broken")
	m = drainTestCmd(t, m, m.requestSearch())
	if m.searchErr == nil || m.searchPending {
		t.Fatal("FTS error hidden as no matches")
	}
	a.db.Close()
	m.searchInput.SetValue("alpha")
	m = drainTestCmd(t, m, m.requestSearch())
	if m.searchErr == nil || !strings.Contains(m.View(), "Search failed") {
		t.Fatal("closed database error not visible")
	}
}

func TestQN012TransitionWaitsForLatestSaveAndNewID(t *testing.T) {
	m, a, _, _, second := asyncModel(t)
	m = typeTest(m, "one")
	m, cmd := rawUpdate(m, tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	firstResult := cmd().(saveResult)
	m = typeTest(m, "two")
	newest := bufferToString(m.buffer)
	m, cmd = rawUpdate(m, firstResult)
	if m.activeID() == second.ID {
		t.Fatal("transition before latest draft saved")
	}
	m = drainTestCmd(t, m, cmd)
	if m.activeID() != second.ID {
		t.Fatal("transition never resumed")
	}
	// New note remains ID zero until its save succeeds, then replay Ctrl+N.
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	m = typeTest(m, "new draft")
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlN})
	if m.activeID() != 0 || m.needsSave {
		t.Fatal("new-note save/replay failed")
	}
	list, err := a.ListChecked(false)
	if err != nil {
		t.Fatal(err)
	}
	seenNewest, seenNew := false, false
	for _, n := range list {
		n, err = a.GetNote(n.ID)
		if err != nil {
			t.Fatal(err)
		}
		seenNewest = seenNewest || n.Content == newest
		seenNew = seenNew || strings.Contains(n.Content, "new draft")
	}
	if !seenNewest || !seenNew {
		t.Fatal("transition dropped persisted draft")
	}
}

func TestQN012ReloadCancelAndCopyLateResultKeepDraft(t *testing.T) {
	m, _, _ := conflictModel(t)
	m = updateTest(t, m, tickMsg(time.Now()))
	before := bufferToString(m.buffer)
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	var cmd tea.Cmd
	m, cmd = rawUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	late := cmd()
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = rawUpdate(m, late)
	if bufferToString(m.buffer) != before || !m.needsSave || m.state != stateTyping {
		t.Fatal("Esc did not cancel pending reload")
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd = rawUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	copy := cmd().(recoveryResult)
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = typeTest(m, "newer")
	text := bufferToString(m.buffer)
	id := m.activeID()
	m, _ = rawUpdate(m, copy)
	if bufferToString(m.buffer) != text || m.activeID() != id || !m.needsSave {
		t.Fatal("late copy response discarded newer draft")
	}
}

func TestQN012MutationWhileTypingKeepsRecoveryDraft(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	m, cmd := rawUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlD})
	result := cmd().(lifecycleResult)
	m = typeTest(m, "during delete")
	text := bufferToString(m.buffer)
	m = drainTestCmd(t, m, func() tea.Msg { return result })
	stored, err := a.GetNote(first.ID)
	if err != nil || stored.DeletedAt == "" {
		t.Fatal("delete not applied")
	}
	if m.state != stateConflict || !m.needsSave || bufferToString(m.buffer) != text {
		t.Fatal("async delete discarded intervening draft")
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if m.activeID() == first.ID || m.needsSave {
		t.Fatal("deleted draft not recoverable as copy")
	}
}

func TestQN012InitialLoadCannotOverwriteTyping(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = typeTest(m, "newest")
	before := bufferToString(m.buffer)
	m, _ = rawUpdate(m, initDataMsg{notes: []Note{{ID: 987, Content: "old startup"}}})
	if bufferToString(m.buffer) != before || m.activeID() == 987 {
		t.Fatal("late startup load replaced draft")
	}
}

// This store fails if a method runs before Update returns. Calling the
// returned Tea command enables I/O, proving jobs are lazy rather than inline.
type eventLoopGuardStore struct {
	NoteStore
	allowed bool
	calls   int
}

func (s *eventLoopGuardStore) check() {
	if !s.allowed {
		panic("store call on event loop")
	}
	s.calls++
}
func (s *eventLoopGuardStore) Save(n Note) (Note, error) { s.check(); return s.NoteStore.Save(n) }
func (s *eventLoopGuardStore) SaveCopy(n Note) (Note, error) {
	s.check()
	return s.NoteStore.SaveCopy(n)
}
func (s *eventLoopGuardStore) GetNote(id int) (Note, error) {
	s.check()
	return s.NoteStore.GetNote(id)
}
func (s *eventLoopGuardStore) ListChecked(trash bool) ([]Note, error) {
	s.check()
	return s.NoteStore.ListChecked(trash)
}
func (s *eventLoopGuardStore) SearchChecked(q string, trash bool) ([]Note, error) {
	s.check()
	return s.NoteStore.SearchChecked(q, trash)
}
func (s *eventLoopGuardStore) TrashCAS(id int, r int64) error {
	s.check()
	return s.NoteStore.TrashCAS(id, r)
}
func (s *eventLoopGuardStore) RestoreCAS(id int, r int64) error {
	s.check()
	return s.NoteStore.RestoreCAS(id, r)
}
func (s *eventLoopGuardStore) PurgeCAS(id int, r int64) error {
	s.check()
	return s.NoteStore.PurgeCAS(id, r)
}

func TestQN012UpdateAndViewNeverCallStore(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyCtrlN}, {Type: tea.KeyCtrlT}, {Type: tea.KeyCtrlF}, {Type: tea.KeyCtrlD}, {Type: tea.KeyEsc}, {Type: tea.KeyRight, Alt: true}, {Type: tea.KeyF1}, {Type: tea.KeyCtrlS}, {Type: tea.KeyCtrlG}} {
		t.Run(key.String(), func(t *testing.T) {
			m, a, _, _, _ := asyncModel(t)
			m = typeTest(m, "dirty")
			guard := &eventLoopGuardStore{NoteStore: a}
			m.store = guard
			m, cmd := rawUpdate(m, key)
			_ = m.View()
			if guard.calls != 0 {
				t.Fatal("inline I/O")
			}
			guard.allowed = true
			// Execute only the first worker; subsequent Update transitions must also
			// be pure, but drainTestCmd would enable the guard during Update.
			if cmd != nil {
				_ = cmd()
			}
		})
	}
	m, a, _, _, _ := asyncModel(t)
	m = typeTest(m, "dirty")
	guard := &eventLoopGuardStore{NoteStore: a}
	m.store = guard
	m.lastActivity = time.Now().Add(-3 * time.Second)
	m, cmd := rawUpdate(m, tickMsg(time.Now()))
	if guard.calls != 0 {
		t.Fatal("inline autosave")
	}
	guard.allowed = true
	_ = cmd()
	_ = m
}

func TestQN012ImmutableSaveMetadataSnapshot(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m = typeTest(m, "draft")
	m.Note.Tags = []string{"captured"}
	m.Note.KillHash = "captured hash"
	cmd := m.requestSave()
	m.Note.Tags[0] = "mutated"
	m.Note.KillHash = "mutated hash"
	result := cmd().(saveResult)
	if result.Request.Snapshot.Tags[0] != "captured" || result.Request.Snapshot.KillHash != "captured hash" {
		t.Fatal("worker captured mutable note metadata")
	}
}

func TestQN012GhostNeverQueuesSave(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.Note.ID = 0
	m.Note.IsGhost = true
	m = typeTest(m, "private")
	if cmd := m.requestSave(); cmd != nil || len(m.saves) != 0 {
		t.Fatal("ghost queued persistence")
	}
}

func TestQN012FailedLoadKeepsIdentityAndDraft(t *testing.T) {
	m, a, _, _, second := asyncModel(t)
	m = typeTest(m, "keep")
	cmd := m.requestOpen(second.ID, false, nil, false)
	a.db.Close()
	id, text, version := m.activeID(), bufferToString(m.buffer), m.localVersion
	m, _ = rawUpdate(m, cmd())
	if m.activeID() != id || bufferToString(m.buffer) != text || !m.needsSave || m.localVersion != version || !strings.Contains(m.flashMsg, "Load failed") {
		t.Fatal("failed load lost current draft")
	}
}

func TestQN012RandomizedSaveEditCycles(t *testing.T) {
	m, a, _, first, _ := asyncModel(t)
	for i := 0; i < 100; i++ {
		m = typeTest(m, fmt.Sprint(i%10))
		cmd := m.requestSave()
		result := cmd()
		m = typeTest(m, "x")
		text := bufferToString(m.buffer)
		var next tea.Cmd
		m, next = rawUpdate(m, result)
		m = drainTestCmd(t, m, next)
		stored, err := a.GetNote(first.ID)
		if err != nil || stored.Content != text || m.needsSave {
			t.Fatalf("cycle %d lost newest text", i)
		}
		// Old completions arriving again after reconciliation remain harmless.
		m, _ = rawUpdate(m, result)
	}
}

// A real SQLite writer lock delays the command but not the Bubble Tea loop.
func TestQN012LockedWriterStillAllowsTypingAndResize(t *testing.T) {
	m, a, b, first, _ := asyncModel(t)
	held, err := b.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	m = typeTest(m, "one")
	cmd := m.requestSave()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	m = typeTest(m, "two")
	before := bufferToString(m.buffer)
	m, _ = rawUpdate(m, tea.WindowSizeMsg{Width: 50, Height: 12})
	if m.viewport.Width != 50 || bufferToString(m.buffer) != before || !m.needsSave {
		t.Fatal("typing/resize blocked or draft changed")
	}
	select {
	case <-result:
		t.Fatal("save bypassed held writer lock")
	default:
	}
	if err = held.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-result:
		var next tea.Cmd
		m, next = rawUpdate(m, msg)
		m = drainTestCmd(t, m, next)
	case <-time.After(6 * time.Second):
		t.Fatal("save did not complete after lock release")
	}
	stored, err := a.GetNote(first.ID)
	if err != nil || stored.Content != before || m.needsSave {
		t.Fatal("latest text lost after blocked save")
	}
}

func TestQN012ExitWaitsForInactiveSaveAndSurfacesConflict(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			m, _, b, first, second := asyncModel(t)
			m = typeTest(m, "local")
			save := m.requestSave()
			m, _ = rawUpdate(m, m.requestOpen(second.ID, false, nil, false)())
			if conflict {
				remote := first
				remote.Content = "remote"
				if _, err := b.Save(remote); err != nil {
					t.Fatal(err)
				}
			}
			var cmd tea.Cmd
			m, cmd = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
			if cmd != nil || !m.exitPending() {
				t.Fatal("quit abandoned outstanding inactive save")
			}
			m, cmd = rawUpdate(m, save())
			if conflict {
				m = drainTestCmd(t, m, cmd)
				if m.activeID() != first.ID || !m.needsSave || !errors.Is(m.saveErr, ErrConflict) || m.state != stateConflict {
					t.Fatal("exit lost failed inactive draft")
				}
			} else {
				if cmd == nil {
					t.Fatal("exit never resumed")
				}
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Fatal("exit did not return Quit after all saves")
				}
			}
		})
	}
}

func TestQN012WorkersRunConcurrentlyWithEditorUpdates(t *testing.T) {
	m, _, _, _, second := asyncModel(t)
	m = typeTest(m, "draft")
	save := m.requestSave()
	load := m.requestOpen(second.ID, false, nil, false)
	highlight := m.requestHighlight()
	m.state = stateSearch
	m.prevState = stateTyping
	m.searchInput.SetValue("alpha")
	debounce := m.requestSearch()()
	var search tea.Cmd
	m, search = rawUpdate(m, debounce)
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	start := make(chan struct{})
	results := make(chan tea.Msg, 4)
	for _, cmd := range []tea.Cmd{save, load, highlight, search} {
		go func() { <-start; results <- cmd() }()
	}
	close(start)
	for i := 0; i < 50; i++ {
		m = typeTest(m, "x")
		m, _ = rawUpdate(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	}
	newest := bufferToString(m.buffer)
	for range 4 {
		select {
		case msg := <-results:
			m = updateTest(t, m, msg)
		case <-time.After(6 * time.Second):
			t.Fatal("worker did not complete")
		}
	}
	if bufferToString(m.buffer) != newest || m.activeID() == second.ID || m.needsSave {
		t.Fatal("concurrent completion replaced newest editor draft")
	}
}

func TestQN012SearchEnterStillPreservesConflictingDraft(t *testing.T) {
	m, b, remote := conflictModel(t)
	second, err := b.Save(Note{Content: "target"})
	if err != nil {
		t.Fatal(err)
	}
	before := bufferToString(m.buffer)
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.state != stateSearch || !m.needsSave || bufferToString(m.buffer) != before {
		t.Fatal("search discarded unsaved draft")
	}
	m.searchResults = []Note{second}
	m.searchPending = false
	m.searchErr = nil
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.state != stateConflict || m.activeID() != remote.ID || !m.needsSave || bufferToString(m.buffer) != before || !errors.Is(m.saveErr, ErrConflict) {
		t.Fatal("search transition overwrote conflict")
	}
}

func TestQN012RecoveredCopyAllowsExitButFurtherEditDoesNot(t *testing.T) {
	m, _, remote := conflictModel(t)
	m = updateTest(t, m, tickMsg(time.Now()))
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	if !m.drafts[remote.ID].recovered {
		t.Fatal("successful copy not recorded as recovery")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if cmd == nil {
		t.Fatal("recovered draft blocked exit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("copy recovery did not permit exit")
	}
	// Returning to the original and editing again creates a new unsaved version.
	for i, n := range m.notes {
		if n.ID == remote.ID {
			m.index = i
			break
		}
	}
	m.activateNote()
	m.state = stateTyping
	m = typeTest(m, "new edit")
	m.stashNote()
	if m.drafts[remote.ID].recovered {
		t.Fatal("copy recovery acknowledged later edits")
	}
}

func TestQN012AsyncRestoreDoesNotResetDeadline(t *testing.T) {
	m, a, _, _, _ := asyncModel(t)
	content := "#kill 1 hour\nrestore this"
	hash, _ := getKillTimerInfo(content)
	n, err := a.Save(Note{Content: content, KillHash: hash, KillStart: time.Now().Add(-2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.TrashCAS(n.ID, n.Revision); err != nil {
		t.Fatal(err)
	}
	n, err = a.GetNote(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.notes = []Note{n}
	m.index = 0
	m.activateNote()
	m.state = stateTrash
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlR})
	m = updateTest(t, m, keyMessage("tab"))
	m = updateTest(t, m, keyMessage("enter"))
	if m.activeID() != n.ID || m.state != stateTyping || m.Note.ExpiresAt.Valid {
		t.Fatal("restored wrong note or rearmed deadline")
	}
	m = updateTest(t, m, tickMsg(time.Now()))
	stored, err := a.GetNote(n.ID)
	if err != nil || stored.DeletedAt != "" {
		t.Fatal("restored note immediately expired")
	}
}

func TestQN012SearchOpeningCancelsOlderLoadAndPendingExit(t *testing.T) {
	m, _, _, _, second := asyncModel(t)
	oldLoad := m.requestOpen(second.ID, false, nil, false)()
	m = typeTest(m, "draft")
	save := m.requestSave()
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = rawUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.exitPending() || m.loadPending {
		t.Fatal("search retained pending exit/load")
	}
	m, cmd := rawUpdate(m, save())
	m = drainTestCmd(t, m, cmd)
	m, _ = rawUpdate(m, oldLoad)
	if m.state != stateSearch || m.activeID() == second.ID {
		t.Fatal("old transition escaped latest overlay")
	}
}

func TestQN012TypingBeforeStartupKeepsOnlyUserText(t *testing.T) {
	db := newTestDB(t)
	n, err := NewSQLiteStore(db).Save(Note{Content: "existing"})
	if err != nil {
		t.Fatal(err)
	}
	m := initialModel(db, time.Now())
	m = typeTest(m, "captured")
	before := bufferToString(m.buffer)
	if strings.Contains(before, "Loading") || !strings.Contains(before, "captured") {
		t.Fatal("startup placeholder became user data")
	}
	m, _ = rawUpdate(m, initDataMsg{notes: []Note{n}})
	if m.activeID() != 0 || bufferToString(m.buffer) != before || !m.needsSave {
		t.Fatal("startup completion replaced first keystrokes")
	}
	m = drainTestCmd(t, m, m.requestSave())
	saved, err := NewSQLiteStore(db).GetNote(m.activeID())
	if err != nil || saved.Content != before || saved.ID == n.ID {
		t.Fatal("first-keystroke draft not saved separately")
	}
}
