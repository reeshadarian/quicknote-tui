package main

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// HitRegion describes only fully visible terminal cells, with exclusive bounds.
type HitRegion struct {
	X, Y, Width, Height int
	ActionID            string
}

func (r HitRegion) Contains(x, y int) bool {
	return r.Width > 0 && r.Height > 0 && x >= r.X && y >= r.Y && x < r.X+r.Width && y < r.Y+r.Height
}

type Layout struct {
	Width, Height, EditorHeight, Gutter, TextWidth int
	Compact                                        bool
}

func (m model) layout() Layout {
	w := m.viewport.Width
	if w <= 0 {
		w = 80
	}
	h := m.windowHeight
	if h <= 0 {
		h = max(1, m.viewport.Height+1)
		if m.viewport.Height == 0 {
			h = 24
		}
	}
	gutter := 0
	if m.showLineNums && w >= 40 {
		gutter = 6
	}
	return Layout{w, h, max(1, h-1), gutter, max(1, w-gutter), w < 70 || h < 12}
}

type LayoutGlyph struct {
	Rune, End, Cell, Width, SourceCell int
	Text                               string
}
type LayoutRow struct {
	Logical, Start, End int
	Glyphs              []LayoutGlyph
	Width               int
}

func lineRows(line []rune, width int) []LayoutRow {
	width = max(1, width)
	points := lineSourceMap(line)
	rows := []LayoutRow{}
	row := LayoutRow{Start: 0}
	for i, p := range points[:len(points)-1] {
		next := points[i+1]
		w := max(1, next.Cell-p.Cell)
		if row.Width > 0 && row.Width+w > width {
			row.End = p.Rune
			rows = append(rows, row)
			row = LayoutRow{Start: p.Rune}
		}
		text := p.Text
		if next.Cell == p.Cell {
			text = "◌" + text
		}
		displayWidth := w
		if text == "\t" {
			text = strings.Repeat(" ", w)
		}
		text = safeText(text)
		if w > width {
			text = "�"
			displayWidth = 1
		}
		row.Glyphs = append(row.Glyphs, LayoutGlyph{p.Rune, next.Rune, row.Width, displayWidth, p.Cell, text})
		row.Width += displayWidth
		row.End = next.Rune
	}
	if len(row.Glyphs) == 0 {
		row.Start = len(line)
		row.End = len(line)
	}
	rows = append(rows, row)
	return rows
}
func (m model) editorRows() []LayoutRow {
	l := m.layout()
	rows := []LayoutRow{}
	for i, line := range m.buffer {
		part := lineRows(line, l.TextWidth)
		for j := range part {
			part[j].Logical = i
		}
		rows = append(rows, part...)
		if i == m.cursorRow && m.cursorCol == len(line) && part[len(part)-1].Width >= l.TextWidth {
			rows = append(rows, LayoutRow{Logical: i, Start: len(line), End: len(line)})
		}
	}
	if len(rows) == 0 {
		rows = []LayoutRow{{}}
	}
	return rows
}
func cursorInRows(rows []LayoutRow, row, col int) (int, int) {
	found := 0
	cell := 0
	for i, r := range rows {
		if r.Logical != row {
			continue
		}
		if col < r.Start || col > r.End {
			continue
		}
		if col == r.End && i+1 < len(rows) && rows[i+1].Logical == row && rows[i+1].Start == col {
			continue
		}
		found = i
		cell = r.Width
		for _, g := range r.Glyphs {
			if col >= g.Rune && col < g.End {
				cell = g.Cell
				break
			}
		}
		return found, cell
	}
	return found, cell
}
func rowHit(row LayoutRow, x int) BufferCursor {
	col := row.End
	for _, g := range row.Glyphs {
		if x < g.Cell+g.Width {
			col = g.Rune
			break
		}
	}
	return BufferCursor{row.Logical, col}
}
func (m *model) updateEditorScroll() {
	if m.state != stateTyping && m.state != stateTrash {
		return
	}
	rows := m.editorRows()
	at, _ := cursorInRows(rows, m.cursorRow, m.cursorCol)
	height := m.layout().EditorHeight
	if !m.freeScroll {
		if at < *m.viewportTop {
			*m.viewportTop = at
		}
		if at >= *m.viewportTop+height {
			*m.viewportTop = at - height + 1
		}
	}
	*m.viewportTop = max(0, min(*m.viewportTop, max(0, len(rows)-height)))
}
func (m *model) moveVisual(delta int) {
	rows := m.editorRows()
	at, _ := cursorInRows(rows, m.cursorRow, m.cursorCol)
	to := max(0, min(at+delta, len(rows)-1))
	cursor := rowHit(rows[to], m.virtualPhysCol)
	m.cursorRow, m.cursorCol = cursor.Row, cursor.Col
	m.freeScroll = false
}
func (m *model) placeMouse(x, y int) {
	l := m.layout()
	if x < 0 || x >= l.Width || y < 0 || y >= l.EditorHeight {
		return
	}
	rows := m.editorRows()
	at := max(0, min(*m.viewportTop+y, len(rows)-1))
	c := rowHit(rows[at], max(0, x-l.Gutter))
	m.cursorRow, m.cursorCol = c.Row, c.Col
	m.virtualPhysCol = max(0, x-l.Gutter)
	m.freeScroll = false
}
func safeText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}
		return '�'
	}, s)
}
func wrapDisplay(s string, width int) []string {
	var out []string
	for _, line := range strings.Split(safeText(ansi.Strip(s)), "\n") {
		for _, row := range lineRows([]rune(line), max(1, width)) {
			var b strings.Builder
			for _, g := range row.Glyphs {
				b.WriteString(g.Text)
			}
			out = append(out, b.String())
		}
	}
	return out
}
func clipFrame(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(1, width), "")
	}
	return strings.Join(lines, "\n")
}
func (m model) panel(title, body, footer string) string {
	l := m.layout()
	height := m.viewport.Height
	if height <= 0 {
		height = l.Height - 1
	}
	height = max(1, height)
	if m.windowHeight > 0 {
		height = m.windowHeight
	}
	width := l.Width
	headers := wrapDisplay(title, width)
	footers := wrapDisplay(footer, width)
	if height == 1 {
		return clipFrame(footer, width, 1)
	}
	if len(footers) >= height {
		footers = footers[:height-1]
	}
	headers = headers[:min(len(headers), max(0, height-len(footers)))]
	available := max(0, height-len(headers)-len(footers))
	lines := append([]string(nil), headers...)
	content := wrapDisplay(body, width)
	lines = append(lines, content[:min(len(content), available)]...)
	for len(lines) < height-len(footers) {
		lines = append(lines, "")
	}
	lines = append(lines, footers...)
	return clipFrame(strings.Join(lines, "\n"), width, height)
}
func (m model) renderLayoutEditor() string {
	l := m.layout()
	rows := m.editorRows()
	top := 0
	if m.viewportTop != nil {
		top = *m.viewportTop
	}
	top = max(0, min(top, max(0, len(rows)-l.EditorHeight)))
	cursorRow, cursorCell := cursorInRows(rows, m.cursorRow, m.cursorCol)
	highlight := strings.Split(m.cachedHighlight, "\n")
	lines := []string{}
	for i := top; i < min(len(rows), top+l.EditorHeight); i++ {
		row := rows[i]
		var text strings.Builder
		trusted := row.Logical < len(m.buffer) && row.Logical < len(highlight) && !strings.ContainsAny(string(m.buffer[row.Logical]), "\x1b\t") && ansi.Strip(highlight[row.Logical]) == string(m.buffer[row.Logical])
		for _, g := range row.Glyphs {
			glyph := g.Text
			if trusted && glyph == string(m.buffer[row.Logical][g.Rune:g.End]) && lipgloss.Width(glyph) == g.Width {
				glyph = ansi.Cut(highlight[row.Logical], g.SourceCell, g.SourceCell+g.Width)
			}
			if i == cursorRow && g.Cell == cursorCell {
				glyph = lipgloss.NewStyle().Reverse(true).Render(glyph)
			}
			text.WriteString(glyph)
		}
		if i == cursorRow && cursorCell >= row.Width && row.Width < l.TextWidth {
			text.WriteString(lipgloss.NewStyle().Reverse(true).Render(" "))
		}
		prefix := ""
		if l.Gutter > 0 {
			prefix = fmt.Sprintf("%3d │ ", row.Logical+1)
			if row.Start > 0 {
				prefix = "    │ "
			}
		}
		lines = append(lines, prefix+text.String())
	}
	for len(lines) < l.EditorHeight {
		lines = append(lines, "")
	}
	return clipFrame(strings.Join(lines, "\n"), l.Width, l.EditorHeight)
}
