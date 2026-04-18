package main

import (
	"database/sql"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Knetic/govaluate"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type appState int

const (
	stateTyping appState = iota
	stateExport
	statePromptFile
	stateTrash
	stateHelp
	statePreview
	stateSearch
)

type ExportOption struct {
	Name    string
	Command string
}

type textState struct {
	content string
	row     int
	col     int
}

type model struct {
	db          *sql.DB
	notes       []Note
	allNotes    []Note
	index       int
	textarea    textarea.Model
	fileInput   textinput.Model
	searchInput textinput.Model
	viewport    viewport.Model
	glamour     *glamour.TermRenderer
	state       appState
	prevState   appState
	searchQuery string
	flashMsg    string

	undoStack []textState
	redoStack []textState
}

// --- Math Functions & Constants ---
var mathFunctions = map[string]govaluate.ExpressionFunction{
	"sqrt":  func(args ...interface{}) (interface{}, error) { return math.Sqrt(args[0].(float64)), nil },
	"cbrt":  func(args ...interface{}) (interface{}, error) { return math.Cbrt(args[0].(float64)), nil },
	"log":   func(args ...interface{}) (interface{}, error) { return math.Log10(args[0].(float64)), nil },
	"log2":  func(args ...interface{}) (interface{}, error) { return math.Log2(args[0].(float64)), nil },
	"ceil":  func(args ...interface{}) (interface{}, error) { return math.Ceil(args[0].(float64)), nil },
	"floor": func(args ...interface{}) (interface{}, error) { return math.Floor(args[0].(float64)), nil },
	"fact": func(args ...interface{}) (interface{}, error) {
		n := int(args[0].(float64))
		res := 1.0
		for i := 2; i <= n; i++ {
			res *= float64(i)
		}
		return res, nil
	},
	"doublefact": func(args ...interface{}) (interface{}, error) {
		n := int(args[0].(float64))
		res := 1.0
		for i := n; i > 0; i -= 2 {
			res *= float64(i)
		}
		return res, nil
	},
}

var mathConstants = map[string]interface{}{
	"pi": math.Pi, "Pi": math.Pi, "PI": math.Pi,
	"e": math.E, "E": math.E,
	"phi": 1.618033988749895,
}

// --- Date/Time Helpers ---
var timeLayouts = []string{
	"3:04PM Mon 02 Jan 2006",
	"3:04 PM Mon 02 Jan 2006",
	"Mon 02 Jan 2006",
	"3:04PM 02 Jan 2006",
	"3:04 PM 02 Jan 2006",
	"02 Jan 2006",
	"3:04PM",
	"3:04 PM",
}

// NEW: Context-aware keyword expansion
func expandKeywords(val string, isCodeMode bool) (string, bool) {
	if isCodeMode {
		return val, false
	}
	if !strings.Contains(val, ":") {
		return val, false
	}

	now := time.Now()
	changed := false
	replacements := map[string]string{
		":today:": now.Format("02 Jan 2006"),
		":Today:": now.Format("Mon 02 Jan 2006"),
		":time:":  now.Format("3:04PM"),
		":now:":   now.Format("3:04PM 02 Jan 2006"),
		":Now:":   now.Format("3:04PM Mon 02 Jan 2006"),
	}

	lines := strings.Split(val, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // Skip comments
		}

		for k, v := range replacements {
			if strings.Contains(lines[i], k) {
				lines[i] = strings.ReplaceAll(lines[i], k, v)
				changed = true
			}
		}
	}

	if changed {
		return strings.Join(lines, "\n"), true
	}
	return val, false
}

func tryDateTimeMath(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	re := regexp.MustCompile(`^(.*?)\s*([\+\-])\s*(\d+(?:\.\d+)?)\s*([a-zA-Z]+)?$`)
	match := re.FindStringSubmatch(expr)
	if match == nil {
		return "", false
	}

	dateStr := strings.TrimSpace(match[1])
	op := match[2]
	valFloat, _ := strconv.ParseFloat(match[3], 64)
	unit := strings.ToLower(match[4])

	var parsedTime time.Time
	var matchedLayout string
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, dateStr); err == nil {
			parsedTime = t
			matchedLayout = layout
			break
		}
	}
	if matchedLayout == "" {
		return "", false
	}
	if op == "-" {
		valFloat = -valFloat
	}

	if unit == "" {
		if matchedLayout == "3:04PM" || matchedLayout == "3:04 PM" {
			unit = "hr"
		} else {
			unit = "d"
		}
	}

	var outTime time.Time
	valInt := int(valFloat)

	switch unit {
	case "y", "yr", "yrs", "year", "years":
		outTime = parsedTime.AddDate(valInt, 0, 0)
	case "mo", "mon", "month", "months":
		outTime = parsedTime.AddDate(0, valInt, 0)
	case "w", "wk", "week", "weeks":
		outTime = parsedTime.AddDate(0, 0, valInt*7)
	case "d", "day", "days":
		outTime = parsedTime.AddDate(0, 0, valInt)
	case "h", "hr", "hrs", "hour", "hours":
		outTime = parsedTime.Add(time.Duration(valFloat * float64(time.Hour)))
	case "m", "min", "mins", "minute", "minutes":
		outTime = parsedTime.Add(time.Duration(valFloat * float64(time.Minute)))
	default:
		return "", false
	}
	return outTime.Format(matchedLayout), true
}

// --- General Helpers ---
func copyToClipboard(text string) {
	if _, err := exec.LookPath("pbcopy"); err == nil {
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	} else if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := exec.Command("wl-copy")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	} else if _, err := exec.LookPath("xclip"); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
	}
}

func autoRenumber(lines []string) []string {
	counters := make(map[string]int)
	reMatch := regexp.MustCompile(`^(\s*)\d+\.(.*)`)
	reNum := regexp.MustCompile(`^(\s*)(\d+)\.`)

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			counters = make(map[string]int)
			continue
		}

		if match := reMatch.FindStringSubmatch(line); match != nil {
			indent := match[1]
			rest := match[2]

			origNum := 1
			if numMatch := reNum.FindStringSubmatch(line); numMatch != nil {
				origNum, _ = strconv.Atoi(numMatch[2])
			}

			if val, exists := counters[indent]; exists {
				counters[indent] = val + 1
				lines[i] = fmt.Sprintf("%s%d.%s", indent, counters[indent], rest)
			} else {
				counters[indent] = origNum
			}
		}
	}
	return lines
}

func sanitizeFilename(name string) string {
	re := regexp.MustCompile(`[\\/:*?"<>|]`)
	return re.ReplaceAllString(name, "")
}

func extractTitle(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return "note"
	}
	firstLine := strings.TrimSpace(lines[0])
	lowerFirst := strings.ToLower(firstLine)
	if strings.HasPrefix(lowerFirst, "math:") || strings.HasPrefix(lowerFirst, "code:") {
		parts := strings.SplitN(firstLine, ":", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			return sanitizeFilename(strings.TrimSpace(parts[1]))
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			t := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if t != "" {
				return sanitizeFilename(t)
			}
		}
	}
	return "note"
}

func tryConversion(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	reBase := regexp.MustCompile(`(?i)^([0-9a-f]+)\s+(bin|hex|dec|oct)\s+to\s+(bin|hex|dec|oct)$`)
	if match := reBase.FindStringSubmatch(expr); match != nil {
		valStr := match[1]
		bases := map[string]int{"bin": 2, "oct": 8, "dec": 10, "hex": 16}
		parsed, err := strconv.ParseInt(valStr, bases[strings.ToLower(match[2])], 64)
		if err == nil {
			res := strconv.FormatInt(parsed, bases[strings.ToLower(match[3])])
			if bases[strings.ToLower(match[3])] == 16 {
				res = strings.ToUpper(res)
			}
			return res, true
		}
	}
	reTemp := regexp.MustCompile(`(?i)^(\-?\d+(?:\.\d+)?)\s+(c|f|k)\s+to\s+(c|f|k)$`)
	if match := reTemp.FindStringSubmatch(expr); match != nil {
		val, _ := strconv.ParseFloat(match[1], 64)
		from, to := strings.ToLower(match[2]), strings.ToLower(match[3])
		var c float64
		switch from {
		case "c":
			c = val
		case "f":
			c = (val - 32) * 5 / 9
		case "k":
			c = val - 273.15
		}
		var res float64
		switch to {
		case "c":
			res = c
		case "f":
			res = (c * 9 / 5) + 32
		case "k":
			res = c + 273.15
		}
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", res), "0"), "."), true
	}
	reUnit := regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s+([a-z]+)\s+to\s+([a-z]+)$`)
	if match := reUnit.FindStringSubmatch(expr); match != nil {
		val, _ := strconv.ParseFloat(match[1], 64)
		from, to := strings.ToLower(match[2]), strings.ToLower(match[3])
		units := map[string]float64{
			"kg": 1, "g": 0.001, "mg": 0.000001, "lbs": 0.453592, "lb": 0.453592, "oz": 0.0283495,
			"m": 1, "km": 1000, "cm": 0.01, "mm": 0.001, "in": 0.0254, "ft": 0.3048, "yd": 0.9144, "mi": 1609.34,
			"l": 1, "ml": 0.001, "gal": 3.78541, "qt": 0.946353, "pt": 0.473176, "cup": 0.236588,
		}
		if fF, ok1 := units[from]; ok1 {
			if tF, ok2 := units[to]; ok2 {
				res := val * (fF / tF)
				return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", res), "0"), "."), true
			}
		}
	}
	return "", false
}

func preprocessMath(expr string) string {
	expr = strings.ReplaceAll(expr, "÷", "/")
	expr = strings.ReplaceAll(expr, " x ", " * ")
	expr = strings.ReplaceAll(expr, " X ", " * ")
	expr = strings.ReplaceAll(expr, "^", "**")
	reOf := regexp.MustCompile(`(\d+(?:\.\d+)?)%\s*of\s*(\d+(?:\.\d+)?)`)
	expr = reOf.ReplaceAllString(expr, "($1 / 100 * $2)")
	rePercAddSub := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*([\+\-])\s*(\d+(?:\.\d+)?)%`)
	expr = rePercAddSub.ReplaceAllString(expr, "$1 $2 ($1 * $3 / 100)")
	reSqrt := regexp.MustCompile(`√(\d+(?:\.\d+)?)`)
	expr = reSqrt.ReplaceAllString(expr, "sqrt($1)")
	reCbrt := regexp.MustCompile(`∛(\d+(?:\.\d+)?)`)
	expr = reCbrt.ReplaceAllString(expr, "cbrt($1)")
	reDoubleFact := regexp.MustCompile(`(\d+(?:\.\d+)?)!!`)
	expr = reDoubleFact.ReplaceAllString(expr, "doublefact($1)")
	reFact := regexp.MustCompile(`(\d+(?:\.\d+)?)!`)
	expr = reFact.ReplaceAllString(expr, "fact($1)")
	return expr
}

func updateMathBuffer(val string) (string, bool) {
	changed := false
	lines := strings.Split(val, "\n")
	params := make(map[string]interface{})
	for k, v := range mathConstants {
		params[k] = v
	}

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		if idx := strings.Index(line, ":="); idx != -1 {
			varName := strings.TrimSpace(line[:idx])
			rawExpr := strings.TrimSpace(line[idx+2:])
			if varName != "" && rawExpr != "" {
				processedExpr := preprocessMath(rawExpr)
				expression, err := govaluate.NewEvaluableExpressionWithFunctions(processedExpr, mathFunctions)
				if err == nil {
					if res, err := expression.Evaluate(params); err == nil {
						params[varName] = res
					}
				}
			}
			continue
		}
		if idx := strings.Index(line, "="); idx != -1 {
			left := line[:idx]
			rawExpr := strings.TrimSpace(left)
			if rawExpr == "" {
				continue
			}
			var resultStr string

			if dtRes, ok := tryDateTimeMath(rawExpr); ok {
				resultStr = dtRes
			} else if convRes, ok := tryConversion(rawExpr); ok {
				resultStr = convRes
			} else {
				processedExpr := preprocessMath(rawExpr)
				expression, err := govaluate.NewEvaluableExpressionWithFunctions(processedExpr, mathFunctions)
				if err == nil {
					result, err := expression.Evaluate(params)
					if err == nil {
						resStr := fmt.Sprintf("%f", result)
						if strings.Contains(resStr, ".") {
							resStr = strings.TrimRight(strings.TrimRight(resStr, "0"), ".")
						}
						resultStr = resStr
					}
				}
			}
			if resultStr != "" {
				expected := strings.TrimRight(left, " ") + " = " + resultStr
				if line != expected {
					lines[i] = expected
					changed = true
				}
			}
		}
	}
	return strings.Join(lines, "\n"), changed
}

func restoreCursor(ta textarea.Model, targetRow, targetCol, totalLines int) textarea.Model {
	for i := 0; i <= totalLines; i++ {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	for i := 0; i < 500; i++ {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}
	for i := 0; i < targetRow; i++ {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	for i := 0; i < targetCol; i++ {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	return ta
}

func (m model) reload() model {
	inTrash := m.state == stateTrash
	if m.state == stateSearch {
		inTrash = m.prevState == stateTrash
	}

	m.allNotes = loadNotes(m.db, inTrash)

	if m.searchQuery == "" {
		m.notes = m.allNotes
	} else {
		var filtered []Note
		q := strings.ToLower(m.searchQuery)
		for _, n := range m.allNotes {
			if strings.Contains(strings.ToLower(n.Content), q) {
				filtered = append(filtered, n)
			}
		}
		if len(filtered) == 0 {
			filtered = append(filtered, Note{ID: -1, Content: "--- No matches found ---", UpdatedAt: ""})
		}
		m.notes = filtered
	}

	if m.index >= len(m.notes) {
		m.index = len(m.notes) - 1
	}
	if m.index < 0 {
		m.index = 0
	}
	if len(m.notes) > 0 {
		m.textarea.SetValue(m.notes[m.index].Content)
	}
	return m
}

func (m *model) generateHelpText() string {
	accentNormal := lipgloss.Color("#89B4FA")
	headerStyle := lipgloss.NewStyle().Foreground(accentNormal).Bold(true)

	words := len(strings.Fields(m.textarea.Value()))
	chars := len(m.textarea.Value())

	val := strings.ToLower(strings.TrimSpace(m.textarea.Value()))
	mode := "Normal"
	if strings.HasPrefix(val, "math:") {
		mode = "Math Mode"
	}
	if strings.HasPrefix(val, "code:") {
		mode = "Code Mode"
	}

	timeStr := m.notes[m.index].UpdatedAt
	if timeStr == "" {
		timeStr = "Just now"
	}

	idStr := fmt.Sprintf("Note ID: %d", m.notes[m.index].ID)
	if m.notes[m.index].ID <= 0 {
		idStr = "Note ID: Unsaved (New)"
	}

	infoSection := fmt.Sprintf("%s\n%s\nWords: %d | Characters: %d\nLast Saved: %s\nMode: %s",
		headerStyle.Render("=== NOTE INFORMATION ==="),
		idStr, words, chars, timeStr, mode)

	cheatsheet := fmt.Sprintf(`%s
Alt+Left/Right : Switch Notes
Ctrl+U         : Promote current note to the front
Ctrl+N         : Create New Note
Ctrl+D         : Delete Current Note
Ctrl+S         : Save note to .md file
Ctrl+W         : Copy entire note to clipboard
Ctrl+T         : Open Trash Bin
Ctrl+F         : Find / Search Notes
Ctrl+P         : Toggle Markdown Preview
F1             : Toggle Help

%s
Alt+M          : Toggle Math Mode ('code:' keyword disables lists)
//             : Line comments (ignored by Math, Lists, and Expansions)
Ctrl+B / R     : Bold (****) / Italic (**) 
`+"` (1x / 3x)  : Auto-pair inline code / create multiline code block"+`
Ctrl+X         : Toggle Checkbox / Toggle Strikethrough
Ctrl+O/Alt+Ent : Smart open line below
Alt+N          : Delete Entire Line
Ctrl+Z/Alt+Z   : Undo  |  Ctrl+Y/Alt+Y : Redo
Tab / Shft+Tab : Indent / Un-indent line (4 spaces)
=              : Execute Math on line
(Auto-List)    : Auto-increments 1., -, *, >, and - [ ] on Enter

%s
:today:        : e.g., 17 Apr 2026
:time:         : e.g., 3:04PM
:now:          : e.g., 3:04PM 17 Apr 2026`,
		headerStyle.Render("=== GLOBAL NAVIGATION ==="),
		headerStyle.Render("=== EDITOR & MODES ==="),
		headerStyle.Render("=== MAGIC KEYWORDS ==="))

	content := lipgloss.JoinVertical(lipgloss.Left, infoSection, "\n", cheatsheet, "\n(Press Esc, Enter, or F1 to return)")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentNormal).
		Padding(1, 3).
		Render(content)

	termWidth := m.viewport.Width
	if termWidth == 0 {
		termWidth = 80
	}
	return lipgloss.PlaceHorizontal(termWidth, lipgloss.Center, box)
}

// --- Main App Logic ---

func initialModel(db *sql.DB) model {
	ti := textarea.New()
	ti.Focus()
	ti.Prompt = ""
	ti.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(lipgloss.Color("236"))

	fi := textinput.New()
	fi.Prompt = "Save as: "

	si := textinput.New()
	si.Prompt = "Search: "

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	glam, err := glamour.NewTermRenderer(glamour.WithAutoStyle())
	if err != nil {
		log.Println("Warning: Failed to initialize Glamour renderer:", err)
	}

	notes := loadNotes(db, false)
	ti.SetValue(notes[0].Content)

	return model{
		db: db, notes: notes, allNotes: notes, index: 0, textarea: ti, fileInput: fi, searchInput: si, viewport: vp, glamour: glam, state: stateTyping,
	}
}

func (m model) Init() tea.Cmd { return tea.Batch(textarea.Blink, textinput.Blink) }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:

		if m.flashMsg != "" {
			m.flashMsg = ""
		}

		if m.state == statePreview {
			switch msg.String() {
			case "esc", "ctrl+p":
				m.state = stateTyping
				return m, nil
			}
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		if m.state == stateSearch {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = m.prevState
				m.searchQuery = ""
				m.searchInput.SetValue("")
				m = m.reload()
				return m, nil
			case "enter":
				m.state = m.prevState
				return m, nil
			}

			m.searchInput, cmd = m.searchInput.Update(msg)
			newQuery := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))
			if newQuery != m.searchQuery {
				m.searchQuery = newQuery
				m.index = 0
				m = m.reload()
			}
			return m, cmd
		}

		if m.state == stateHelp {
			switch msg.String() {
			case "esc", "ctrl+c", "enter", "f1":
				m.state = stateTyping
				return m, nil
			}
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

		if m.state == stateTrash {
			switch msg.String() {
			case "esc", "ctrl+t":
				m.state = stateTyping
				m.searchQuery = ""
				m.searchInput.SetValue("")
				m.index = 0
				m = m.reload()
				return m, nil
			case "alt+left":
				if m.index < len(m.notes)-1 {
					m.index++
					m.textarea.SetValue(m.notes[m.index].Content)
				}
			case "alt+right":
				if m.index > 0 {
					m.index--
					m.textarea.SetValue(m.notes[m.index].Content)
				}
			case "ctrl+f":
				m.prevState = m.state
				m.state = stateSearch
				m.searchInput.Focus()
				m.searchInput.SetValue(m.searchQuery)
				m.searchInput.CursorEnd()
				m = m.reload()
				return m, textinput.Blink
			case "ctrl+r":
				if m.notes[m.index].ID > 0 {
					restoreNote(m.db, m.notes[m.index].ID)
					m.index = 0
					m = m.reload()
				}
			}
			return m, nil
		}

		if m.state == statePromptFile {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = stateTyping
				m.fileInput.SetValue("")
				return m, nil
			case "enter":
				path := m.fileInput.Value()
				if path != "" {
					if strings.HasPrefix(path, "~/") {
						home, _ := os.UserHomeDir()
						path = strings.Replace(path, "~", home, 1)
					}
					_ = os.WriteFile(path, []byte(m.notes[m.index].Content), 0644)
				}
				m.state = stateTyping
				m.fileInput.SetValue("")
				return m, nil
			}
			m.fileInput, cmd = m.fileInput.Update(msg)
			return m, cmd
		}

		// ==========================
		// STATE: NORMAL TYPING
		// ==========================
		contentLower := strings.ToLower(strings.TrimSpace(m.textarea.Value()))
		isMathMode := strings.HasPrefix(contentLower, "math:")
		isCodeMode := strings.HasPrefix(contentLower, "code:")

		isMacro := false
		switch msg.String() {
		case "space", "enter", "alt+enter", "ctrl+o", "tab", "shift+tab", "ctrl+x", "ctrl+b", "ctrl+r", "alt+n", "=", "`", "ctrl+s":
			isMacro = true
		}

		if isMacro {
			if len(m.undoStack) == 0 || m.undoStack[len(m.undoStack)-1].content != m.textarea.Value() {
				m.undoStack = append(m.undoStack, textState{
					content: m.textarea.Value(),
					row:     m.textarea.Line(),
					col:     m.textarea.LineInfo().ColumnOffset,
				})
				if len(m.undoStack) > 50 {
					m.undoStack = m.undoStack[1:]
				}
				m.redoStack = nil
			}
		}

		switch msg.String() {
		case "esc", "ctrl+c":
			if msg.String() == "esc" && m.searchQuery != "" {
				m.notes[m.index].Content = m.textarea.Value()
				m.notes[m.index] = saveNote(m.db, m.notes[m.index])

				m.searchQuery = ""
				m.searchInput.SetValue("")
				m.index = 0
				m = m.reload()
				return m, nil
			}
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			return m, tea.Quit

		case "f1":
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.viewport.SetContent(m.generateHelpText())
			m.viewport.GotoTop()
			m.state = stateHelp
			return m, nil

		case "ctrl+f":
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])

			m.prevState = m.state
			m.state = stateSearch
			m.searchInput.Focus()
			m.searchInput.SetValue(m.searchQuery)
			m.searchInput.CursorEnd()
			m = m.reload()
			return m, textinput.Blink

		case "ctrl+w":
			copyToClipboard(m.textarea.Value())
			m.flashMsg = "Copied to Clipboard!"
			return m, nil

		case "ctrl+u":
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])

			if m.index > 0 {
				m = m.reload()
				m.index = 0
				m.textarea.SetValue(m.notes[m.index].Content)
			}
			m.flashMsg = "Note Promoted!"
			return m, nil

		case "ctrl+z", "alt+z":
			if len(m.undoStack) > 0 {
				m.redoStack = append(m.redoStack, textState{content: m.textarea.Value(), row: m.textarea.Line(), col: m.textarea.LineInfo().ColumnOffset})
				last := m.undoStack[len(m.undoStack)-1]
				m.undoStack = m.undoStack[:len(m.undoStack)-1]
				m.textarea.SetValue(last.content)
				m.textarea = restoreCursor(m.textarea, last.row, last.col, len(strings.Split(last.content, "\n")))
			}
			return m, nil

		case "ctrl+y", "alt+y":
			if len(m.redoStack) > 0 {
				m.undoStack = append(m.undoStack, textState{content: m.textarea.Value(), row: m.textarea.Line(), col: m.textarea.LineInfo().ColumnOffset})
				next := m.redoStack[len(m.redoStack)-1]
				m.redoStack = m.redoStack[:len(m.redoStack)-1]
				m.textarea.SetValue(next.content)
				m.textarea = restoreCursor(m.textarea, next.row, next.col, len(strings.Split(next.content, "\n")))
			}
			return m, nil

		case "ctrl+p":
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			var out string
			if m.glamour != nil {
				out, _ = m.glamour.Render(m.textarea.Value())
			} else {
				out = m.textarea.Value()
			}
			m.viewport.SetContent(out)
			m.viewport.GotoTop()
			m.state = statePreview
			return m, nil

		case "ctrl+t":
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.notes = loadNotes(m.db, true)
			m.index = 0
			m.textarea.SetValue(m.notes[m.index].Content)
			m.undoStack = nil
			m.redoStack = nil
			m.state = stateTrash
			return m, nil

		case "ctrl+s":
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.state = statePromptFile
			title := extractTitle(m.notes[m.index].Content)
			m.fileInput.SetValue(title + ".md")
			m.fileInput.CursorEnd()
			m.fileInput.Focus()
			return m, textinput.Blink

		case "ctrl+n":
			m.notes[m.index].Content = m.textarea.Value()
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])

			m.searchQuery = ""
			m.searchInput.SetValue("")
			m.state = stateTyping

			newNote := Note{ID: 0, Content: "", UpdatedAt: "Just now"}
			m.allNotes = append([]Note{newNote}, m.allNotes...)
			m.notes = m.allNotes
			m.index = 0
			m.textarea.SetValue("")
			m.undoStack = nil
			m.redoStack = nil
			return m, nil

		case "ctrl+d":
			if len(m.notes) > 0 {
				id := m.notes[m.index].ID
				if id > 0 {
					if strings.TrimSpace(m.textarea.Value()) == "" {
						hardDeleteNote(m.db, id)
					} else {
						trashNote(m.db, id)
					}
				}
				m.index = 0
				m.undoStack = nil
				m.redoStack = nil
				m = m.reload()
			}
			return m, nil

		case "alt+m":
			lines := strings.Split(m.textarea.Value(), "\n")
			if len(lines) == 0 {
				lines = []string{""}
			}
			firstLine := strings.ToLower(strings.TrimSpace(lines[0]))
			if strings.HasPrefix(firstLine, "math:") {
				colonIdx := strings.Index(lines[0], ":")
				rest := strings.TrimSpace(lines[0][colonIdx+1:])
				if rest == "" {
					if len(lines) > 1 {
						lines = lines[1:]
					} else {
						lines[0] = ""
					}
				} else {
					lines[0] = rest
				}
			} else {
				lines = append([]string{"math:"}, lines...)
			}
			m.textarea.SetValue(strings.Join(lines, "\n"))
			return m, nil

		case "`":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()
			col := m.textarea.LineInfo().ColumnOffset

			if cur < len(lines) {
				line := lines[cur]
				before := line[:col]
				after := line[col:]

				if strings.HasSuffix(before, "``") {
					before = strings.TrimSuffix(before, "``")
					if strings.HasPrefix(after, "``") {
						after = after[2:]
					} else if strings.HasPrefix(after, "`") {
						after = after[1:]
					}

					lines[cur] = before + "```"

					newLines := make([]string, 0, len(lines)+2)
					newLines = append(newLines, lines[:cur+1]...)
					newLines = append(newLines, "", "```"+after)
					if cur+1 < len(lines) {
						newLines = append(newLines, lines[cur+1:]...)
					}

					m.textarea.SetValue(strings.Join(newLines, "\n"))
					m.textarea = restoreCursor(m.textarea, cur+1, 0, len(newLines))
					return m, nil

				} else if strings.HasSuffix(before, "`") {
					m.textarea.InsertString("``")
					m.textarea, _ = m.textarea.Update(tea.KeyMsg{Type: tea.KeyLeft})
					return m, nil

				} else if strings.HasPrefix(after, "`") {
					m.textarea, _ = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRight})
					return m, nil

				} else {
					m.textarea.InsertString("``")
					m.textarea, _ = m.textarea.Update(tea.KeyMsg{Type: tea.KeyLeft})
					return m, nil
				}
			}

		case "ctrl+b":
			m.textarea.InsertString("****")
			m.textarea, _ = m.textarea.Update(tea.KeyMsg{Type: tea.KeyLeft})
			m.textarea, _ = m.textarea.Update(tea.KeyMsg{Type: tea.KeyLeft})
			return m, nil

		case "ctrl+r":
			m.textarea.InsertString("**")
			m.textarea, _ = m.textarea.Update(tea.KeyMsg{Type: tea.KeyLeft})
			return m, nil

		case "alt+n":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()
			if cur < len(lines) {
				lines = append(lines[:cur], lines[cur+1:]...)
				if len(lines) == 0 {
					lines = []string{""}
				}
				targetRow := cur
				if targetRow >= len(lines) {
					targetRow = len(lines) - 1
				}
				if targetRow < 0 {
					targetRow = 0
				}
				m.textarea.SetValue(strings.Join(lines, "\n"))
				m.textarea = restoreCursor(m.textarea, targetRow, 0, len(lines))
			}
			return m, nil

		case "alt+left":
			m.notes[m.index].Content = m.textarea.Value()
			saveNote(m.db, m.notes[m.index])
			if m.index < len(m.notes)-1 {
				m.index++
				m.textarea.SetValue(m.notes[m.index].Content)
				m.undoStack = nil
				m.redoStack = nil
			}
			return m, nil

		case "alt+right":
			m.notes[m.index].Content = m.textarea.Value()
			saveNote(m.db, m.notes[m.index])
			if m.index > 0 {
				m.index--
				m.textarea.SetValue(m.notes[m.index].Content)
				m.undoStack = nil
				m.redoStack = nil
			}
			return m, nil

		case "tab":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()
			if cur < len(lines) {
				curLine := lines[cur]
				curLine = "    " + curLine
				targetCol := m.textarea.LineInfo().ColumnOffset + 4

				re := regexp.MustCompile(`^(\s*)\d+\.(.*)`)
				if re.MatchString(curLine) {
					oldLen := len(curLine)
					curLine = re.ReplaceAllString(curLine, "${1}1.$2")
					targetCol += len(curLine) - oldLen
				}

				lines[cur] = curLine
				lines = autoRenumber(lines)

				m.textarea.SetValue(strings.Join(lines, "\n"))
				m.textarea = restoreCursor(m.textarea, cur, targetCol, len(lines))
			}
			return m, nil

		case "shift+tab":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()
			if cur < len(lines) {
				line := lines[cur]
				spaces := 0
				for _, ch := range line {
					if ch == ' ' && spaces < 4 {
						spaces++
					} else {
						break
					}
				}

				if spaces > 0 {
					lines[cur] = line[spaces:]
					targetCol := m.textarea.LineInfo().ColumnOffset - spaces
					if targetCol < 0 {
						targetCol = 0
					}

					lines = autoRenumber(lines)

					m.textarea.SetValue(strings.Join(lines, "\n"))
					m.textarea = restoreCursor(m.textarea, cur, targetCol, len(lines))
				}
			}
			return m, nil

		case "ctrl+x":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()
			if cur < len(lines) {
				line := lines[cur]
				changed := false

				if strings.Contains(line, "- [ ]") {
					line = strings.Replace(line, "- [ ]", "- [x]", 1)
					parts := strings.SplitN(line, "- [x] ", 2)
					if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
						text := parts[1]
						if !strings.HasPrefix(text, "~~") || !strings.HasSuffix(text, "~~") {
							line = parts[0] + "- [x] ~~" + text + "~~"
						}
					}
					changed = true
				} else if strings.Contains(line, "- [x]") {
					line = strings.Replace(line, "- [x]", "- [ ]", 1)
					parts := strings.SplitN(line, "- [ ] ", 2)
					if len(parts) == 2 {
						text := parts[1]
						if strings.HasPrefix(text, "~~") && strings.HasSuffix(text, "~~") {
							text = text[2 : len(text)-2]
							line = parts[0] + "- [ ] " + text
						}
					}
					changed = true
				} else {
					trimmed := strings.TrimSpace(line)
					if trimmed != "" {
						if strings.HasPrefix(trimmed, "~~") && strings.HasSuffix(trimmed, "~~") {
							trimmed = trimmed[2 : len(trimmed)-2]
							line = strings.Replace(line, strings.TrimSpace(line), trimmed, 1)
						} else {
							line = strings.Replace(line, trimmed, "~~"+trimmed+"~~", 1)
						}
						changed = true
					}
				}

				if changed {
					lines[cur] = line
					targetCol := m.textarea.LineInfo().ColumnOffset
					m.textarea.SetValue(strings.Join(lines, "\n"))
					m.textarea = restoreCursor(m.textarea, cur, targetCol, len(lines))
				}
			}
			return m, nil

		case "=":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()
			if cur < len(lines) && len(lines[cur]) > 0 && !strings.HasSuffix(lines[cur], " ") && !strings.HasSuffix(lines[cur], ":") {
				m.textarea.InsertString(" ")
			}
			m.textarea, cmd = m.textarea.Update(msg)
			cmds = append(cmds, cmd)

		case "ctrl+o", "alt+enter":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()

			leadingSpace := ""
			if cur < len(lines) {
				curLine := lines[cur]
				leadingSpace = curLine[:len(curLine)-len(strings.TrimLeft(curLine, " \t"))]
			}

			var newLines []string
			newLines = append(newLines, lines[:cur+1]...)
			newLines = append(newLines, leadingSpace)
			if cur+1 < len(lines) {
				newLines = append(newLines, lines[cur+1:]...)
			}

			m.textarea.SetValue(strings.Join(newLines, "\n"))
			m.textarea = restoreCursor(m.textarea, cur+1, len(leadingSpace), len(newLines))
			return m, nil

		case "enter":
			lines := strings.Split(m.textarea.Value(), "\n")
			cur := m.textarea.Line()
			col := m.textarea.LineInfo().ColumnOffset

			if isCodeMode {
				m.textarea, cmd = m.textarea.Update(msg)
				return m, tea.Batch(append(cmds, cmd)...)
			}

			if cur < len(lines) {
				curLine := lines[cur]

				if col < len(curLine) {
					m.textarea, cmd = m.textarea.Update(msg)
					return m, tea.Batch(append(cmds, cmd)...)
				}

				trimmed := strings.TrimSpace(curLine)

				if strings.HasPrefix(trimmed, "//") {
					m.textarea, cmd = m.textarea.Update(msg)
					return m, tea.Batch(append(cmds, cmd)...)
				}

				isOrderedEmpty, _ := regexp.MatchString(`^\d+\.$`, trimmed)

				if trimmed == "- [ ]" || trimmed == "- [x]" || trimmed == "-" || trimmed == "*" || trimmed == ">" || isOrderedEmpty {
					lines[cur] = ""
					lines = autoRenumber(lines)

					m.textarea.SetValue(strings.Join(lines, "\n"))
					m.textarea = restoreCursor(m.textarea, cur, 0, len(lines))
					return m, nil
				}

				prefixToInsert := ""
				leadingSpace := curLine[:len(curLine)-len(strings.TrimLeft(curLine, " \t"))]

				if strings.HasPrefix(trimmed, "- [ ] ") || strings.HasPrefix(trimmed, "- [x] ") {
					prefixToInsert = leadingSpace + "- [ ] "
				} else if strings.HasPrefix(trimmed, "- ") {
					prefixToInsert = leadingSpace + "- "
				} else if strings.HasPrefix(trimmed, "* ") {
					prefixToInsert = leadingSpace + "* "
				} else if strings.HasPrefix(trimmed, "> ") {
					prefixToInsert = leadingSpace + "> "
				} else {
					re := regexp.MustCompile(`^(\d+)\. `)
					match := re.FindStringSubmatch(trimmed)
					if len(match) == 2 {
						num, _ := strconv.Atoi(match[1])
						prefixToInsert = leadingSpace + fmt.Sprintf("%d. ", num+1)
					}
				}

				m.textarea, cmd = m.textarea.Update(msg)
				cmds = append(cmds, cmd)
				if prefixToInsert != "" {
					m.textarea.InsertString(prefixToInsert)

					newLines := strings.Split(m.textarea.Value(), "\n")
					newLines = autoRenumber(newLines)

					targetRow := m.textarea.Line()
					targetCol := m.textarea.LineInfo().ColumnOffset

					m.textarea.SetValue(strings.Join(newLines, "\n"))
					m.textarea = restoreCursor(m.textarea, targetRow, targetCol, len(newLines))
				}
				return m, tea.Batch(cmds...)
			}
			m.textarea, cmd = m.textarea.Update(msg)
			cmds = append(cmds, cmd)

		default:
			m.textarea, cmd = m.textarea.Update(msg)
			cmds = append(cmds, cmd)
		}

		// NEW: Pass isCodeMode to expandKeywords
		newValExp, expanded := expandKeywords(m.textarea.Value(), isCodeMode)
		if expanded {
			diff := len(newValExp) - len(m.textarea.Value())
			targetRow := m.textarea.Line()
			targetCol := m.textarea.LineInfo().ColumnOffset + diff
			m.textarea.SetValue(newValExp)
			m.textarea = restoreCursor(m.textarea, targetRow, targetCol, len(strings.Split(newValExp, "\n")))
		}

		if isMathMode {
			newValMath, changed := updateMathBuffer(m.textarea.Value())
			if changed {
				targetRow := m.textarea.Line()
				targetCol := m.textarea.LineInfo().ColumnOffset
				m.textarea.SetValue(newValMath)
				m.textarea = restoreCursor(m.textarea, targetRow, targetCol, len(strings.Split(newValMath, "\n")))
			}
		}
		return m, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		m.textarea.SetWidth(msg.Width)
		m.textarea.SetHeight(msg.Height - 1)
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 1

		if m.state == stateHelp {
			m.viewport.SetContent(m.generateHelpText())
		}
	}

	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	bgDark := lipgloss.Color("#1E1E2E")
	bgMed := lipgloss.Color("#313244")
	textLight := lipgloss.Color("#CDD6F4")
	textDark := lipgloss.Color("#11111B")

	accentNormal := lipgloss.Color("#89B4FA")
	accentMath := lipgloss.Color("#F9E2AF")
	accentCode := lipgloss.Color("#A6E3A1")
	accentTrash := lipgloss.Color("#F38BA8")

	words := len(strings.Fields(m.textarea.Value()))
	chars := len(m.textarea.Value())

	modeStr := " NORMAL "
	modeColor := accentNormal
	val := strings.ToLower(strings.TrimSpace(m.textarea.Value()))
	if strings.HasPrefix(val, "math:") {
		modeStr = " MATH "
		modeColor = accentMath
	} else if strings.HasPrefix(val, "code:") {
		modeStr = " CODE "
		modeColor = accentCode
	}

	if m.state == stateTrash {
		modeStr = " TRASH "
		modeColor = accentTrash
	} else if m.state == statePreview {
		modeStr = " PREVIEW "
		modeColor = accentNormal
	} else if m.state == stateHelp {
		modeStr = " HELP "
		modeColor = accentNormal
	} else if m.state == stateSearch {
		modeStr = " FIND "
		modeColor = accentMath
	}

	modeBlock := lipgloss.NewStyle().Background(modeColor).Foreground(textDark).Bold(true).Padding(0, 1).Render(modeStr)

	searchTag := ""
	if m.searchQuery != "" {
		searchTag = fmt.Sprintf(" [Find: %s] ", m.searchQuery)
	}

	noteInfoStr := fmt.Sprintf("Note %d/%d%s", m.index+1, len(m.notes), searchTag)
	if m.flashMsg != "" {
		noteInfoStr = " ★ " + m.flashMsg + " "
	}

	noteBlock := lipgloss.NewStyle().Background(bgMed).Foreground(textLight).Padding(0, 1).Render(noteInfoStr)
	statBlock := lipgloss.NewStyle().Background(bgDark).Foreground(textLight).Padding(0, 1).Render(fmt.Sprintf("%dW %dC", words, chars))

	leftBar := lipgloss.JoinHorizontal(lipgloss.Top, modeBlock, noteBlock, statBlock)

	timeStr := m.notes[m.index].UpdatedAt
	if timeStr == "" {
		timeStr = "Just now"
	}
	if m.state == stateTrash {
		timeStr = "Deleted: " + m.notes[m.index].DeletedAt
		if m.notes[m.index].DeletedAt == "" {
			timeStr = "Deleted: Unknown"
		}
	}

	saveBlock := lipgloss.NewStyle().Background(bgMed).Foreground(textLight).Padding(0, 1).Render(timeStr)

	termWidth := m.viewport.Width
	if termWidth == 0 {
		termWidth = 80
	}
	spacerWidth := termWidth - lipgloss.Width(leftBar) - lipgloss.Width(saveBlock)
	if spacerWidth < 0 {
		spacerWidth = 0
	}
	spacerStyle := lipgloss.NewStyle().Background(bgDark).Render(strings.Repeat(" ", spacerWidth))

	statusBar := lipgloss.JoinHorizontal(lipgloss.Top, leftBar, spacerStyle, saveBlock)

	if m.state == statePreview || m.state == stateHelp {
		return fmt.Sprintf("%s\n%s", m.viewport.View(), statusBar)
	}

	if m.state == stateSearch {
		p := lipgloss.NewStyle().Foreground(accentNormal).Bold(true).Render("Find in Notes (Esc to clear)") + "\n\n" + m.searchInput.View()
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentNormal).Padding(1, 3).Render(p)
		centeredBox := lipgloss.Place(termWidth, m.viewport.Height, lipgloss.Center, lipgloss.Center, box)
		return lipgloss.JoinVertical(lipgloss.Left, centeredBox, statusBar)
	}

	if m.state == statePromptFile {
		p := lipgloss.NewStyle().Foreground(accentNormal).Bold(true).Render("Save Note to File") + "\n\n" + m.fileInput.View()
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentNormal).Padding(1, 3).Render(p)
		centeredBox := lipgloss.Place(termWidth, m.viewport.Height, lipgloss.Center, lipgloss.Center, box)
		return lipgloss.JoinVertical(lipgloss.Left, centeredBox, statusBar)
	}

	return fmt.Sprintf("%s\n%s", m.textarea.View(), statusBar)
}

func main() {
	db := initDB()
	defer db.Close()
	p := tea.NewProgram(initialModel(db), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
