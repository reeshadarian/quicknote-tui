package main

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

type NoteID = int

// EditorDraft is the editor's authority. index is only a list/navigation cursor.
// A SessionID distinguishes memory-only/unsaved drafts until SQLite assigns ID.
type EditorDraft struct {
	Note                                 Note
	buffer                               EditorBuffer
	cursorRow, cursorCol, virtualPhysCol int
	undoStack, redoStack                 []textState
	viewportTop                          *int
	freeScroll                           bool
	needsSave                            bool
	saveErr                              error
	localVersion                         uint64
	recovered                            bool
	lastEdit                             time.Time
	typingGroup                          bool
	HistoryLimits                        HistoryLimits
}
type HistoryLimits struct{ Transactions, Bytes int }
type EditorBuffer [][]rune

func (b EditorBuffer) Text() string { return bufferToString(b) }
func (b EditorBuffer) Clone() EditorBuffer {
	c := make(EditorBuffer, len(b))
	for i := range b {
		c[i] = append([]rune(nil), b[i]...)
	}
	return c
}
func (d EditorDraft) Clone() EditorDraft {
	d.Note = cloneNote(d.Note)
	d.buffer = d.buffer.Clone()
	d.undoStack = append([]textState(nil), d.undoStack...)
	d.redoStack = append([]textState(nil), d.redoStack...)
	top := 0
	if d.viewportTop != nil {
		top = *d.viewportTop
	}
	d.viewportTop = &top
	return d
}
func noteKey(n Note) int {
	if n.ID != 0 {
		return n.ID
	}
	return -int(n.SessionID) - 2
}
func (m *model) bindDraft() {
	if m.draftReady {
		return
	}
	if len(m.notes) == 0 {
		m.notes = []Note{{}}
		m.index = 0
	}
	m.index = max(0, min(m.index, len(m.notes)-1))
	m.Note = cloneNote(m.notes[m.index])
	m.assignSession()
	m.draftReady = true
}
func (m *model) assignSession() {
	if m.Note.ID == 0 && m.Note.SessionID == 0 {
		m.sessionSeq++
		m.Note.SessionID = m.sessionSeq
	}
	if m.index >= 0 && m.index < len(m.notes) {
		m.notes[m.index] = cloneNote(m.Note)
	}
}
func (m model) activeKey() int {
	if !m.draftReady && len(m.notes) > 0 && m.index >= 0 && m.index < len(m.notes) {
		return noteKey(m.notes[m.index])
	}
	return noteKey(m.Note)
}
func (m *model) syncSummary() {
	for i, n := range m.notes {
		if noteKey(n) == m.activeKey() {
			m.notes[i] = cloneNote(m.Note)
			m.index = i
		}
	}
	for i, n := range m.allNotes {
		if noteKey(n) == m.activeKey() {
			m.allNotes[i] = cloneNote(m.Note)
		}
	}
}
func (m *model) stashNote() {
	m.bindDraft()
	if m.Note.ID < 0 {
		return
	}
	if m.drafts == nil {
		m.drafts = map[int]EditorDraft{}
	}
	previous := m.drafts[m.activeKey()]
	m.recovered = previous.recovered && previous.buffer.Text() == m.buffer.Text() && previous.localVersion == m.localVersion
	m.drafts[m.activeKey()] = m.EditorDraft.Clone()
	m.syncSummary()
}
func (m *model) activateNote() bool {
	if len(m.notes) == 0 || m.index < 0 || m.index >= len(m.notes) {
		return false
	}
	n := cloneNote(m.notes[m.index])
	m.Note = n
	m.assignSession()
	n = m.Note
	m.versionSeq++
	m.EditorDraft = EditorDraft{Note: n, buffer: stringToBuffer(n.Content), localVersion: m.versionSeq, viewportTop: new(int)}
	m.draftReady = true
	if d, ok := m.drafts[noteKey(n)]; ok && (d.needsSave || d.Note.Revision == n.Revision) {
		m.EditorDraft = d.Clone()
	}
	m.clampDraftCursor()
	m.highlightDirty = true
	return true
}
func (m *model) clampDraftCursor() {
	if len(m.buffer) == 0 {
		m.buffer = stringToBuffer("")
	}
	m.cursorRow = max(0, min(m.cursorRow, len(m.buffer)-1))
	m.cursorCol = max(0, min(m.cursorCol, len(m.buffer[m.cursorRow])))
	for m.cursorCol > 0 && !isGraphemeBoundary(m.buffer[m.cursorRow], m.cursorCol) {
		m.cursorCol--
	}
	if m.viewportTop == nil {
		m.viewportTop = new(int)
	}
}
func isGraphemeBoundary(line []rune, col int) bool {
	for _, b := range graphemeBoundaries(line) {
		if b == col {
			return true
		}
	}
	return false
}
func (d *EditorDraft) recordEdit(before textState, typing bool, now time.Time) {
	if !(typing && d.typingGroup && now.Sub(d.lastEdit) < 700*time.Millisecond) {
		d.undoStack = pushStack(d.undoStack, before.content, before.row, before.col)
	}
	d.redoStack = nil
	d.typingGroup = typing
	d.lastEdit = now
	limits := d.HistoryLimits
	if limits.Transactions <= 0 {
		limits.Transactions = 50
	}
	if limits.Bytes <= 0 {
		limits.Bytes = 4 << 20
	}
	bytes := 0
	for _, s := range d.undoStack {
		bytes += len(s.content)
	}
	for len(d.undoStack) > limits.Transactions || (bytes > limits.Bytes && len(d.undoStack) > 0) {
		bytes -= len(d.undoStack[0].content)
		d.undoStack = d.undoStack[1:]
	}
}

// SourcePoint connects byte/rune offsets, grapheme boundaries and terminal cells.
type SourcePoint struct {
	Byte, Rune, Cell int
	Text             string
}

func lineSourceMap(line []rune) []SourcePoint {
	g := uniseg.NewGraphemes(string(line))
	out := []SourcePoint{}
	r, c := 0, 0
	for g.Next() {
		start, _ := g.Positions()
		s := g.Str()
		out = append(out, SourcePoint{start, r, c, s})
		r += utf8.RuneCountInString(s)
		w := g.Width()
		if s == "\t" {
			w = 4 - c%4
		}
		c += w
	}
	out = append(out, SourcePoint{len(string(line)), len(line), c, ""})
	return out
}

type BufferCursor struct{ Row, Col int }

func (b EditorBuffer) ReplaceRange(start, end BufferCursor, text string) (EditorBuffer, BufferCursor) {
	if len(b) == 0 {
		b = stringToBuffer("")
	}
	start.Row = max(0, min(start.Row, len(b)-1))
	end.Row = max(start.Row, min(end.Row, len(b)-1))
	start.Col = max(0, min(start.Col, len(b[start.Row])))
	end.Col = max(0, min(end.Col, len(b[end.Row])))
	for start.Col > 0 && !isGraphemeBoundary(b[start.Row], start.Col) {
		start.Col--
	}
	for end.Col < len(b[end.Row]) && !isGraphemeBoundary(b[end.Row], end.Col) {
		end.Col++
	}
	if start.Row == end.Row && end.Col < start.Col {
		end.Col = start.Col
	}
	parts := strings.Split(text, "\n")
	out := b.Clone()
	prefix := append([]rune(nil), b[start.Row][:start.Col]...)
	suffix := append([]rune(nil), b[end.Row][end.Col:]...)
	rows := make(EditorBuffer, len(parts))
	for i, p := range parts {
		rows[i] = []rune(p)
	}
	rows[0] = append(prefix, rows[0]...)
	col := len(rows[len(rows)-1])
	if len(parts) == 1 {
		col = len(prefix) + len([]rune(parts[0]))
	}
	rows[len(rows)-1] = append(rows[len(rows)-1], suffix...)
	out = append(out[:start.Row], append(rows, out[end.Row+1:]...)...)
	return out, BufferCursor{start.Row + len(parts) - 1, col}
}
func (b EditorBuffer) Insert(at BufferCursor, text string) (EditorBuffer, BufferCursor) {
	return b.ReplaceRange(at, at, text)
}
func (b EditorBuffer) DeleteBackward(at BufferCursor) (EditorBuffer, BufferCursor) {
	if at.Col > 0 {
		return b.ReplaceRange(BufferCursor{at.Row, previousGraphemeBoundary(b[at.Row], at.Col)}, at, "")
	}
	if at.Row > 0 {
		return b.ReplaceRange(BufferCursor{at.Row - 1, len(b[at.Row-1])}, at, "")
	}
	return b, at
}
func (b EditorBuffer) DeleteForward(at BufferCursor) (EditorBuffer, BufferCursor) {
	end := at
	if at.Col < len(b[at.Row]) {
		end.Col = nextGraphemeBoundary(b[at.Row], at.Col)
	} else if at.Row < len(b)-1 {
		end = BufferCursor{at.Row + 1, 0}
	}
	return b.ReplaceRange(at, end, "")
}
func (b EditorBuffer) SplitLine(at BufferCursor) (EditorBuffer, BufferCursor) {
	return b.Insert(at, "\n")
}
func (b EditorBuffer) JoinLine(row int) (EditorBuffer, BufferCursor) {
	return b.DeleteBackward(BufferCursor{row, 0})
}
