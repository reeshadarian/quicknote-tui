package main

import (
	"database/sql"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type appState int

const (
	stateTyping appState = iota
	stateExport
	statePromptFile
	stateTrash
	stateHelp
	stateSearch
	statePipe
	stateConflict
	stateCommands
	stateConfirm
	stateTTLPicker
	stateTTLCustom
	stateCount
)

type textState struct {
	content string
	row     int
	col     int
}

type model struct {
	startupPending        bool
	clock                 Clock
	maintenancePending    bool
	maintenanceGeneration uint64
	lastMaintenance       time.Time
	lifecycleRequests     map[int]lifecycleRequest
	lifecycleQueued       map[int]lifecycleIntent
	lifecycleChanges      map[NoteID]LifecycleChange
	overlays              []OverlayFrame
	ttlPicker             TTLPicker
	confirmation          Confirmation
	exportGeneration      uint64
	windowHeight          int
	EditorDraft
	keymap             Keymap
	keyboardGeneration uint64
	commandReturn      appState
	commandIndex       int
	draftReady         bool
	sessionSeq         uint64
	store              NoteStore
	notes              []Note
	allNotes           []Note
	index              int

	viewport    viewport.Model
	fileInput   textinput.Model
	searchInput textinput.Model
	pipeInput   textinput.Model

	state        appState
	prevState    appState
	searchQuery  string
	flashMsg     string
	showLineNums bool

	lastActivity time.Time

	theme Theme

	statActive int
	statTrash  int

	cachedHighlight string
	highlightDirty  bool

	timerNow time.Time

	searchResults       []Note
	searchIndex         int
	drafts              map[int]EditorDraft
	conflictRemote      Note
	conflictReadErr     error
	confirmReload       bool
	conflictTop         int
	conflictInspect     bool
	mutationGeneration  uint64
	copyPending         bool
	pendingOpen         *Note
	pendingActionID     int
	requestSeq          uint64
	versionSeq          uint64
	openGeneration      uint64
	searchGeneration    uint64
	highlightGeneration uint64
	loadPending         bool
	searchPending       bool
	searchErr           error
	saves               map[int]saveRequest
	pendingAction       *tea.KeyMsg
	actionPending       bool
	recoveryGeneration  uint64
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type cursorBlinkMsg time.Time

func cursorBlinkCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*530, func(t time.Time) tea.Msg {
		return cursorBlinkMsg(t)
	})
}

func pushStack(stack []textState, val string, row, col int) []textState {
	stack = append(stack, textState{content: val, row: row, col: col})
	return stack
}

type initDataMsg struct {
	maintenance    MaintenanceResult
	keyboard       Keymap
	keyboardNotice string
	err            error
	theme          Theme
	notes          []Note
}

func initialModel(db *sql.DB, startTime time.Time) model {
	fi := textinput.New()
	fi.Prompt = "Save as: "
	si := textinput.New()
	si.Prompt = "Search: "
	pi := textinput.New()
	pi.Prompt = "Pipe through: "

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	// Instantly return the model without touching the disk or database
	m := model{
		startupPending: true,
		EditorDraft: EditorDraft{buffer: stringToBuffer(""),
			cursorRow:      0,
			cursorCol:      0,
			virtualPhysCol: 0,
			viewportTop:    new(int)},
		store:        NewSQLiteStore(db),
		notes:        []Note{{ID: 0, Content: "", Preview: ""}},
		allNotes:     []Note{},
		index:        0,
		fileInput:    fi,
		searchInput:  si,
		pipeInput:    pi,
		viewport:     vp,
		state:        stateTyping,
		showLineNums: true,
		timerNow:     startTime,
		// Provide a fallback theme while loading
		theme:    Theme{BgDark: "#1E1E2E", TextLight: "#CDD6F4", AccentNormal: "#89B4FA"},
		flashMsg: fmt.Sprintf("Started in %.2fms", float64(time.Since(startTime).Nanoseconds())/1e6),
	}

	m.cachedHighlight = ""
	m.highlightDirty = false

	return m
}

func (m model) Init() tea.Cmd {
	// Capture the store independently of the model for background I/O.
	store := m.store
	clock := clockOrReal(m.clock)
	loadDataCmd := func() tea.Msg {
		theme := loadTheme()
		keyboard, notice := loadKeymap()
		maintenance, err := store.Maintain(clock.Now(), nil)
		if err != nil {
			return initDataMsg{maintenance: maintenance, err: err}
		}
		notes, err := store.ListChecked(false)
		if err != nil {
			return initDataMsg{maintenance: maintenance, err: err}
		}

		if len(notes) == 0 {
			notes = []Note{{ID: 0, Content: "", Preview: ""}}
		} else if notes[0].ID > 0 {
			fresh, err := store.GetNote(notes[0].ID)
			if err != nil {
				return initDataMsg{maintenance: maintenance, err: err}
			}
			notes[0] = fresh
		}

		return initDataMsg{maintenance: maintenance, theme: theme, notes: notes, keyboard: keyboard, keyboardNotice: notice}
	}

	return tea.Batch(
		textinput.Blink,
		tickCmd(),
		cursorBlinkCmd(),
		loadDataCmd, // Send the msg when done
	)
}

func (m *model) generateHelpText() string {
	termWidth := m.viewport.Width
	if termWidth == 0 {
		termWidth = 80
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal)).Bold(true)

	var docProps string
	if len(m.notes) > 0 && m.index >= 0 && m.index < len(m.notes) {
		currentNote := m.Note
		content := bufferToString(m.buffer)
		lines := len(m.buffer)
		words := len(strings.Fields(content))
		chars := len([]rune(content))

		createdStr := currentNote.CreatedAt
		if createdStr == "" {
			createdStr = "Unknown"
		}

		tagsDisplay := "None"
		if len(currentNote.Tags) > 0 {
			tagsDisplay = strings.Join(currentNote.Tags, ", ")
		}

		docProps = fmt.Sprintf(
			"Note ID : %d\nCreated : %s\nUpdated : %s\nTags    : %s\nStats   : %d Lines | %d Words | %d Chars",
			currentNote.ID, createdStr, currentNote.UpdatedAt, tagsDisplay, lines, words, chars,
		)
	} else {
		docProps = "No active note."
	}
	docPropsText := fmt.Sprintf("%s\n%s", headerStyle.Render("=== CURRENT DOCUMENT ==="), docProps)

	context := m.actionContext()
	context.State = stateTyping
	cheatsheet := m.actionHelp(context) + "\nSnippets: :today: :time: :now: :uuid:\n" + m.actionHelp(ActionContext{State: stateTrash, Note: m.Note})

	dbPropsText := fmt.Sprintf("%s\nActive Notes : %d\nTrash Bin    : %d", headerStyle.Render("=== DATABASE ==="), m.statActive, m.statTrash)

	allTags := gatherAllTags(m.allNotes)
	tagsDisplay := strings.Join(allTags, "   ")
	if len(allTags) == 0 {
		tagsDisplay = "No tags found yet!"
	}
	wrappedTags := lipgloss.NewStyle().Width(max(1, termWidth-10)).Render("Your Tags:\n" + tagsDisplay)

	helpText := fmt.Sprintf("%s\n%s\n\n%s\n\n%s", docPropsText, cheatsheet, dbPropsText, wrappedTags)

	return lipgloss.NewStyle().Padding(2, 4).Render(helpText)
}

func isWordBoundary(r rune) bool {
	// Add or remove punctuation here based on how strict you want the jumps to be!
	return r == ' ' || r == '\t' || r == '\n' || r == '.' || r == ',' || r == ';' || r == ':' ||
		r == '!' || r == '?' || r == '-' || r == '(' || r == ')' || r == '[' || r == ']' ||
		r == '{' || r == '}' || r == '"' || r == '\''
}

func (m model) updateEditor(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	clampCursor := func() {
		if m.cursorRow < 0 {
			m.cursorRow = 0
		}
		if m.cursorRow >= len(m.buffer) {
			m.cursorRow = len(m.buffer) - 1
		}
		if m.cursorCol < 0 {
			m.cursorCol = 0
		}
		if m.cursorCol > len(m.buffer[m.cursorRow]) {
			m.cursorCol = len(m.buffer[m.cursorRow])
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.windowHeight = max(1, msg.Height)
		m.viewport.Width = max(1, msg.Width)
		m.viewport.Height = max(1, msg.Height-1)
		return m, nil

	case tea.MouseMsg:
		if m.state == stateHelp {
			m.viewport, cmd = m.viewport.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			goto Finalize
		}
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelUp {
				*m.viewportTop -= 4
				if *m.viewportTop < 0 {
					*m.viewportTop = 0
				}
				m.freeScroll = true
			} else if msg.Button == tea.MouseButtonWheelDown {
				*m.viewportTop += 4
				m.freeScroll = true
			} else if msg.Button == tea.MouseButtonLeft {
				m.placeMouse(msg.X, msg.Y)
			}
		}
		return m, nil

	case tea.KeyMsg:
		m.lastActivity = time.Now()
		m.freeScroll = false
		m.highlightDirty = true

		if m.state == statePipe {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.popOverlay()
				m.pipeInput.SetValue("")
			case "enter":
				cmdStr := strings.TrimSpace(m.pipeInput.Value())
				if cmdStr != "" {

					lines := strings.Split(bufferToString(m.buffer), "\n")
					var headerLines []string
					contentIndex := 0

					for i, line := range lines {
						lower := strings.ToLower(strings.TrimSpace(line))
						if strings.HasPrefix(lower, "code:") || strings.HasPrefix(lower, "math:") || strings.HasPrefix(lower, "# ") || lower == "" {
							headerLines = append(headerLines, line)
							contentIndex = i + 1
						} else {
							break
						}
					}

					contentToPipe := strings.Join(lines[contentIndex:], "\n")
					cmdExc := exec.Command("sh", "-c", cmdStr)
					cmdExc.Stdin = strings.NewReader(contentToPipe)
					out, err := cmdExc.CombinedOutput()

					if err != nil {
						errMsg := strings.TrimSpace(string(out))
						if errMsg == "" {
							errMsg = err.Error()
						}
						errMsg = strings.Split(errMsg, "\n")[0]
						if len(errMsg) > 55 {
							errMsg = errMsg[:52] + "..."
						}
						m.showError("Filter failed", fmt.Errorf("%s", errMsg))
					} else {
						finalText := string(out)
						finalText = strings.TrimRight(finalText, "\n")
						if len(headerLines) > 0 {
							finalText = strings.Join(headerLines, "\n") + "\n" + finalText
						}
						m.popOverlay()
						m.confirm("replace", "Replace draft with filter output?", finalText, finalText)
						m.virtualPhysCol = 0
						m.flashMsg = "Filter output ready"
					}
				}
				if m.state == statePipe {
					m.popOverlay()
				}
				m.pipeInput.SetValue("")
			default:
				m.pipeInput, cmd = m.pipeInput.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			goto Finalize
		}

		if m.state == stateHelp {
			switch msg.String() {
			case "esc", "ctrl+c", "enter", "f1":
				m.popOverlay()
			case "up", "down", "pgup", "pgdown":
				m.viewport, cmd = m.viewport.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			goto Finalize
		}

		if m.state == stateTrash {
			if msg.String() == "up" || msg.String() == "down" || msg.String() == "pgup" || msg.String() == "pgdown" {
				m.viewport, cmd = m.viewport.Update(msg)
				cmds = append(cmds, cmd)
			}
			goto Finalize
		}

		if m.state == statePromptFile {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.exportGeneration++
				m.popOverlay()
				m.fileInput.Reset()
			case "enter":
				if m.fileInput.Value() != "" {
					cmds = append(cmds, m.checkExport(m.fileInput.Value()))
				}
			default:
				old := m.fileInput.Value()
				m.fileInput, cmd = m.fileInput.Update(msg)
				if old != m.fileInput.Value() {
					m.exportGeneration++
				}
				cmds = append(cmds, cmd)
			}
			goto Finalize
		}

		currentVal := bufferToString(m.buffer)
		contentLower := strings.ToLower(strings.TrimSpace(currentVal))
		isMathMode := strings.HasPrefix(contentLower, "math:")
		isCodeMode := strings.HasPrefix(contentLower, "code:")

		isMacro := false
		isModifying := msg.Type == tea.KeyRunes

		switch msg.String() {
		case " ", "enter", "alt+enter", "ctrl+o", "tab", "shift+tab", "ctrl+x", "ctrl+b", "alt+i", "ctrl+k", "=", "`", "ctrl+s":
			isMacro = true
			isModifying = true
		case "backspace", "delete":
			isModifying = true
		}

		if isModifying {
			m.needsSave = true
			if len(m.undoStack) == 0 {
				m.undoStack = pushStack(m.undoStack, currentVal, m.cursorRow, m.cursorCol)
			}
		}

		if isMacro {
			if len(m.undoStack) == 0 || m.undoStack[len(m.undoStack)-1].content != currentVal {
				m.undoStack = pushStack(m.undoStack, currentVal, m.cursorRow, m.cursorCol)
				m.redoStack = nil
			}
		}

		switch msg.String() {
		case "f2":
			m.showLineNums = !m.showLineNums
		case "ctrl+z", "alt+z":
			if len(m.undoStack) > 0 {
				m.redoStack = pushStack(m.redoStack, bufferToString(m.buffer), m.cursorRow, m.cursorCol)
				last := m.undoStack[len(m.undoStack)-1]
				m.undoStack = m.undoStack[:len(m.undoStack)-1]
				m.buffer = stringToBuffer(last.content)
				m.cursorRow, m.cursorCol = last.row, last.col
				clampCursor()
			}

		case "ctrl+y", "alt+y":
			if len(m.redoStack) > 0 {
				m.undoStack = pushStack(m.undoStack, bufferToString(m.buffer), m.cursorRow, m.cursorCol)
				next := m.redoStack[len(m.redoStack)-1]
				m.redoStack = m.redoStack[:len(m.redoStack)-1]
				m.buffer = stringToBuffer(next.content)
				m.cursorRow, m.cursorCol = next.row, next.col
				clampCursor()
			}

		case "alt+c":
			cmds = append(cmds, m.requestClipboard())
		case "alt+x":
			m.pushOverlay(statePipe)
			m.pipeInput.SetValue("")
			m.pipeInput.Focus()
			cmds = append(cmds, textinput.Blink)
		case "up", "down", "pgup", "pgdown":
			delta := 1
			switch msg.String() {
			case "up":
				delta = -1
			case "pgup":
				delta = -m.layout().EditorHeight
			case "pgdown":
				delta = m.layout().EditorHeight
			}
			m.moveVisual(delta)
		case "left":
			if m.cursorCol > 0 {
				m.cursorCol = previousGraphemeBoundary(m.buffer[m.cursorRow], m.cursorCol)
			} else if m.cursorRow > 0 {
				m.cursorRow--
				m.cursorCol = len(m.buffer[m.cursorRow])
			}
		case "right":
			if m.cursorCol < len(m.buffer[m.cursorRow]) {
				m.cursorCol = nextGraphemeBoundary(m.buffer[m.cursorRow], m.cursorCol)
			} else if m.cursorRow < len(m.buffer)-1 {
				m.cursorRow++
				m.cursorCol = 0
			}
		case "ctrl+left":
			// 1. If at the very start of a line, wrap back to the end of the previous line
			if m.cursorCol == 0 && m.cursorRow > 0 {
				m.cursorRow--
				m.cursorCol = len(m.buffer[m.cursorRow])
			}

			line := m.buffer[m.cursorRow]

			// 2. Skip over any trailing spaces/punctuation going backwards
			for m.cursorCol > 0 && isWordBoundary(line[m.cursorCol-1]) {
				m.cursorCol--
			}
			// 3. Move backwards until we hit a space/punctuation or the start of the line
			for m.cursorCol > 0 && !isWordBoundary(line[m.cursorCol-1]) {
				m.cursorCol--
			}

			// Update physical memory so up/down arrows respect the new column
			m.virtualPhysCol = m.cursorCol

		case "ctrl+right":
			line := m.buffer[m.cursorRow]

			// 1. If at the very end of a line, wrap forward to the start of the next line
			if m.cursorCol >= len(line) && m.cursorRow < len(m.buffer)-1 {
				m.cursorRow++
				m.cursorCol = 0
				line = m.buffer[m.cursorRow]
			}

			// 2. Skip over any leading spaces/punctuation going forwards
			for m.cursorCol < len(line) && isWordBoundary(line[m.cursorCol]) {
				m.cursorCol++
			}
			// 3. Move forwards until we hit a space/punctuation or the end of the line
			for m.cursorCol < len(line) && !isWordBoundary(line[m.cursorCol]) {
				m.cursorCol++
			}

			// Update physical memory so up/down arrows respect the new column
			m.virtualPhysCol = m.cursorCol
		case "home", "ctrl+a":
			gutterWidth := 0
			if m.showLineNums {
				gutterWidth = 6
			}
			textWidth := m.viewport.Width - gutterWidth - 2
			if textWidth <= 0 {
				textWidth = 80
			}

			starts, _ := getLineMap(m.buffer[m.cursorRow], textWidth)

			physRow := len(starts) - 1
			for r := 0; r < len(starts); r++ {
				if r < len(starts)-1 {
					if m.cursorCol < starts[r+1] {
						physRow = r
						break
					}
				} else {
					physRow = r
				}
			}

			firstNonSpace := 0
			line := m.buffer[m.cursorRow]
			for firstNonSpace < len(line) && (line[firstNonSpace] == ' ' || line[firstNonSpace] == '\t') {
				firstNonSpace++
			}

			if physRow == 0 {
				if m.cursorCol == firstNonSpace {
					m.cursorCol = 0
				} else {
					m.cursorCol = firstNonSpace
				}
			} else {
				if m.cursorCol == starts[physRow] {
					m.cursorCol = firstNonSpace
				} else {
					m.cursorCol = starts[physRow]
				}
			}
		case "end", "ctrl+e":
			gutterWidth := 0
			if m.showLineNums {
				gutterWidth = 6
			}
			textWidth := m.viewport.Width - gutterWidth - 2
			if textWidth <= 0 {
				textWidth = 80
			}

			starts, lengths := getLineMap(m.buffer[m.cursorRow], textWidth)

			physRow := len(starts) - 1
			for r := 0; r < len(starts); r++ {
				if r < len(starts)-1 {
					if m.cursorCol < starts[r+1] {
						physRow = r
						break
					}
				} else {
					physRow = r
				}
			}

			physEnd := starts[physRow] + lengths[physRow]
			if physEnd > len(m.buffer[m.cursorRow]) {
				physEnd = len(m.buffer[m.cursorRow])
			}

			if m.cursorCol == physEnd {
				m.cursorCol = len(m.buffer[m.cursorRow])
			} else {
				m.cursorCol = physEnd
			}

		case "backspace":
			if m.cursorCol > 0 {
				previous := previousGraphemeBoundary(m.buffer[m.cursorRow], m.cursorCol)
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:previous], m.buffer[m.cursorRow][m.cursorCol:]...)
				m.cursorCol = previous
			} else if m.cursorRow > 0 {
				prevLen := len(m.buffer[m.cursorRow-1])
				m.buffer[m.cursorRow-1] = append(m.buffer[m.cursorRow-1], m.buffer[m.cursorRow]...)
				m.buffer = append(m.buffer[:m.cursorRow], m.buffer[m.cursorRow+1:]...)
				m.cursorRow--
				m.cursorCol = prevLen
			}

		case "delete":
			if m.cursorCol < len(m.buffer[m.cursorRow]) {
				next := nextGraphemeBoundary(m.buffer[m.cursorRow], m.cursorCol)
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], m.buffer[m.cursorRow][next:]...)
			} else if m.cursorRow < len(m.buffer)-1 {
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow], m.buffer[m.cursorRow+1]...)
				m.buffer = append(m.buffer[:m.cursorRow+1], m.buffer[m.cursorRow+2:]...)
			}

		case "enter":
			inCodeBlock := false
			for r := 0; r <= m.cursorRow; r++ {
				if strings.Count(string(m.buffer[r]), "```")%2 != 0 {
					inCodeBlock = !inCodeBlock
				}
			}

			if isCodeMode || inCodeBlock {
				rightPart := append([]rune{}, m.buffer[m.cursorRow][m.cursorCol:]...)
				m.buffer[m.cursorRow] = m.buffer[m.cursorRow][:m.cursorCol]
				m.buffer = append(m.buffer[:m.cursorRow+1], append([][]rune{rightPart}, m.buffer[m.cursorRow+1:]...)...)
				m.cursorRow++
				m.cursorCol = 0
				break
			}

			curLineStr := string(m.buffer[m.cursorRow])
			trimmed := strings.TrimSpace(curLineStr)

			isOrderedEmpty, _ := regexp.MatchString(`^[a-zA-Z0-9]+\.$`, trimmed)
			if trimmed == "- [ ]" || trimmed == "- [x]" || trimmed == "-" || trimmed == "*" || trimmed == ">" || isOrderedEmpty {
				m.buffer[m.cursorRow] = []rune{}
				lines := strings.Split(bufferToString(m.buffer), "\n")
				m.buffer = stringToBuffer(strings.Join(autoRenumber(lines), "\n"))
				m.cursorCol = 0
				break
			}

			prefixToInsert := ""
			leadingSpace := curLineStr[:len(curLineStr)-len(strings.TrimLeft(curLineStr, " \t"))]
			if strings.HasPrefix(trimmed, "- [ ] ") || strings.HasPrefix(trimmed, "- [x] ") {
				prefixToInsert = leadingSpace + "- [ ] "
			} else if strings.HasPrefix(trimmed, "- ") {
				prefixToInsert = leadingSpace + "- "
			} else if strings.HasPrefix(trimmed, "* ") {
				prefixToInsert = leadingSpace + "* "
			} else if strings.HasPrefix(trimmed, "> ") {
				prefixToInsert = leadingSpace + "> "
			} else {
				re := regexp.MustCompile(`^([a-zA-Z0-9]+)\. `)
				if len(re.FindStringSubmatch(trimmed)) == 2 {
					prefixToInsert = leadingSpace + "1. "
				}
			}

			rightPart := append([]rune(prefixToInsert), m.buffer[m.cursorRow][m.cursorCol:]...)
			m.buffer[m.cursorRow] = m.buffer[m.cursorRow][:m.cursorCol]

			m.buffer = append(m.buffer[:m.cursorRow+1], append([][]rune{rightPart}, m.buffer[m.cursorRow+1:]...)...)
			m.cursorRow++

			lines := strings.Split(bufferToString(m.buffer), "\n")
			m.buffer = stringToBuffer(strings.Join(autoRenumber(lines), "\n"))

			renumberedLine := string(m.buffer[m.cursorRow])
			reNumPrefix := regexp.MustCompile(`^(\s*[a-zA-Z0-9]+\.\s)`)

			if match := reNumPrefix.FindString(renumberedLine); match != "" && reNumPrefix.MatchString(prefixToInsert) {
				m.cursorCol = len([]rune(match))
			} else {
				m.cursorCol = len([]rune(prefixToInsert))
			}

		case " ":
			m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append([]rune{' '}, m.buffer[m.cursorRow][m.cursorCol:]...)...)
			m.cursorCol++

		case "tab":
			var cursor BufferCursor
			m.buffer, cursor = m.buffer.Insert(BufferCursor{m.cursorRow, m.cursorCol}, "    ")
			m.cursorRow, m.cursorCol = cursor.Row, cursor.Col
		case "shift+tab":
			line := m.buffer[m.cursorRow]
			spaces := 0
			for i := 0; i < len(line) && i < 4; i++ {
				if line[i] == ' ' {
					spaces++
				} else {
					break
				}
			}
			if spaces > 0 {
				m.buffer[m.cursorRow] = append(line[:0], line[spaces:]...)
				m.cursorCol -= spaces
				if m.cursorCol < 0 {
					m.cursorCol = 0
				}
				lines := strings.Split(bufferToString(m.buffer), "\n")
				m.buffer = stringToBuffer(strings.Join(autoRenumber(lines), "\n"))
			}

		case "ctrl+k":
			if len(m.buffer) > 1 {
				m.buffer = append(m.buffer[:m.cursorRow], m.buffer[m.cursorRow+1:]...)
			} else {
				m.buffer[0] = []rune{}
			}
			clampCursor()

		case "ctrl+o", "alt+enter":
			curLineStr := string(m.buffer[m.cursorRow])
			leadingSpace := curLineStr[:len(curLineStr)-len(strings.TrimLeft(curLineStr, " \t"))]

			m.buffer = append(m.buffer[:m.cursorRow+1], append([][]rune{[]rune(leadingSpace)}, m.buffer[m.cursorRow+1:]...)...)
			m.cursorRow++
			m.cursorCol = len([]rune(leadingSpace))

		case "ctrl+b":
			runes := []rune("****")
			m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append(runes, m.buffer[m.cursorRow][m.cursorCol:]...)...)
			m.cursorCol += 2

		case "alt+i":
			runes := []rune("**")
			m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append(runes, m.buffer[m.cursorRow][m.cursorCol:]...)...)
			m.cursorCol += 1

		case "ctrl+x":
			lineStr := string(m.buffer[m.cursorRow])
			trimmed := strings.TrimSpace(lineStr)

			if trimmed == "" {
				leadingSpace := lineStr[:len(lineStr)-len(strings.TrimLeft(lineStr, " \t"))]
				lineStr = leadingSpace + "- [ ] "
				m.buffer[m.cursorRow] = []rune(lineStr)
				m.cursorCol = len(m.buffer[m.cursorRow])
				clampCursor()
			} else if strings.Contains(lineStr, "- [ ]") {
				lineStr = strings.Replace(lineStr, "- [ ]", "- [x]", 1)
				parts := strings.SplitN(lineStr, "- [x] ", 2)
				if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
					text := parts[1]
					if !strings.HasPrefix(text, "~~") || !strings.HasSuffix(text, "~~") {
						lineStr = parts[0] + "- [x] ~~" + text + "~~"
					}
				}
			} else if strings.Contains(lineStr, "- [x]") {
				lineStr = strings.Replace(lineStr, "- [x]", "- [ ]", 1)
				parts := strings.SplitN(lineStr, "- [ ] ", 2)
				if len(parts) == 2 {
					text := parts[1]
					if strings.HasPrefix(text, "~~") && strings.HasSuffix(text, "~~") {
						text = text[2 : len(text)-2]
						lineStr = parts[0] + "- [ ] " + text
					}
				}
			} else {
				trimmed := strings.TrimSpace(lineStr)
				if trimmed != "" {
					if strings.HasPrefix(trimmed, "~~") && strings.HasSuffix(trimmed, "~~") {
						trimmed = trimmed[2 : len(trimmed)-2]
						lineStr = strings.Replace(lineStr, strings.TrimSpace(lineStr), trimmed, 1)
					} else {
						lineStr = strings.Replace(lineStr, trimmed, "~~"+trimmed+"~~", 1)
					}
				}
			}
			m.buffer[m.cursorRow] = []rune(lineStr)
			clampCursor()

		case "alt+m":
			lines := strings.Split(bufferToString(m.buffer), "\n")
			firstLine := strings.ToLower(strings.TrimSpace(lines[0]))
			if strings.HasPrefix(firstLine, "math:") {
				colonIdx := strings.Index(lines[0], ":")
				rest := strings.TrimSpace(lines[0][colonIdx+1:])
				if rest == "" && len(lines) > 1 {
					lines = lines[1:]
					m.cursorRow--
				} else {
					lines[0] = rest
				}
			} else if strings.HasPrefix(firstLine, "code:") {
				colonIdx := strings.Index(lines[0], ":")
				rest := strings.TrimSpace(lines[0][colonIdx+1:])
				if rest == "" {
					lines[0] = "math:"
				} else {
					lines[0] = "math: " + rest
				}
			} else {
				lines = append([]string{"math:"}, lines...)
				m.cursorRow++
			}
			m.buffer = stringToBuffer(strings.Join(lines, "\n"))
			clampCursor()

		case "alt+v":
			lines := strings.Split(bufferToString(m.buffer), "\n")
			firstLine := strings.ToLower(strings.TrimSpace(lines[0]))
			if strings.HasPrefix(firstLine, "code:") {
				colonIdx := strings.Index(lines[0], ":")
				rest := strings.TrimSpace(lines[0][colonIdx+1:])
				if rest == "" && len(lines) > 1 {
					lines = lines[1:]
					m.cursorRow--
				} else {
					lines[0] = rest
				}
			} else if strings.HasPrefix(firstLine, "math:") {
				colonIdx := strings.Index(lines[0], ":")
				rest := strings.TrimSpace(lines[0][colonIdx+1:])
				if rest == "" {
					lines[0] = "code:"
				} else {
					lines[0] = "code: " + rest
				}
			} else {
				lines = append([]string{"code:"}, lines...)
				m.cursorRow++
			}
			m.buffer = stringToBuffer(strings.Join(lines, "\n"))
			clampCursor()

		case "`":
			line := m.buffer[m.cursorRow]
			before := string(line[:m.cursorCol])
			after := string(line[m.cursorCol:])

			if strings.HasSuffix(before, "``") {
				before = strings.TrimSuffix(before, "``")
				if strings.HasPrefix(after, "``") {
					after = after[2:]
				} else if strings.HasPrefix(after, "`") {
					after = after[1:]
				}
				lines := strings.Split(bufferToString(m.buffer), "\n")

				lines[m.cursorRow] = before + "\x60\x60\x60"
				newLines := append(lines[:m.cursorRow+1], append([]string{"", "\x60\x60\x60" + after}, lines[m.cursorRow+1:]...)...)

				m.buffer = stringToBuffer(strings.Join(newLines, "\n"))
				m.cursorRow++
				m.cursorCol = 0
			} else if strings.HasSuffix(before, "`") {
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append([]rune("``"), m.buffer[m.cursorRow][m.cursorCol:]...)...)
				m.cursorCol += 1
			} else if strings.HasPrefix(after, "`") {
				m.cursorCol++
			} else {
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append([]rune("``"), m.buffer[m.cursorRow][m.cursorCol:]...)...)
				m.cursorCol += 1
			}

		case "=":
			lineStr := string(m.buffer[m.cursorRow])
			if len(lineStr) > 0 && !strings.HasSuffix(lineStr, " ") && !strings.HasSuffix(lineStr, ":") {
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append([]rune(" ="), m.buffer[m.cursorRow][m.cursorCol:]...)...)
				m.cursorCol += 2
			} else {
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append([]rune{'='}, m.buffer[m.cursorRow][m.cursorCol:]...)...)
				m.cursorCol++
			}

		default:
			if msg.Type == tea.KeyRunes {
				strRunes := string(msg.Runes)

				if strings.Contains(strRunes, "\n") || strings.Contains(strRunes, "\r") {
					strRunes = strings.ReplaceAll(strRunes, "\r\n", "\n")
					strRunes = strings.ReplaceAll(strRunes, "\r", "\n")

					lines := strings.Split(strRunes, "\n")

					leftPart := append([]rune{}, m.buffer[m.cursorRow][:m.cursorCol]...)
					rightPart := append([]rune{}, m.buffer[m.cursorRow][m.cursorCol:]...)

					m.buffer[m.cursorRow] = append(leftPart, []rune(lines[0])...)

					var newRows [][]rune
					for i := 1; i < len(lines)-1; i++ {
						newRows = append(newRows, []rune(lines[i]))
					}

					if len(lines) > 1 {
						lastRow := append([]rune(lines[len(lines)-1]), rightPart...)
						newRows = append(newRows, lastRow)
					}

					if len(newRows) > 0 {
						newBuffer := make([][]rune, 0, len(m.buffer)+len(newRows))
						newBuffer = append(newBuffer, m.buffer[:m.cursorRow+1]...)
						newBuffer = append(newBuffer, newRows...)
						newBuffer = append(newBuffer, m.buffer[m.cursorRow+1:]...)
						m.buffer = newBuffer
					}

					m.cursorRow += len(lines) - 1
					if len(lines) > 1 {
						m.cursorCol = len([]rune(lines[len(lines)-1]))
					} else {
						m.cursorCol += len([]rune(lines[0]))
					}

				} else {
					m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append(msg.Runes, m.buffer[m.cursorRow][m.cursorCol:]...)...)
					m.cursorCol += len(msg.Runes)
				}
			}
		}

		if msg.Type == tea.KeyRunes && msg.String() == ":" {
			newValExp, expanded := expandKeywords(bufferToString(m.buffer), isCodeMode, msg.String(), m.cursorRow, m.cursorCol)
			if expanded {
				diff := len([]rune(newValExp)) - len([]rune(bufferToString(m.buffer)))
				m.undoStack = pushStack(m.undoStack, bufferToString(m.buffer), m.cursorRow, m.cursorCol)
				m.redoStack = nil
				m.buffer = stringToBuffer(newValExp)
				m.cursorCol += diff
			}
		}

		if isMathMode {
			newValMath, changed := updateMathBuffer(bufferToString(m.buffer), msg.String())
			if changed {
				m.undoStack = pushStack(m.undoStack, bufferToString(m.buffer), m.cursorRow, m.cursorCol)
				m.redoStack = nil
				m.buffer = stringToBuffer(newValMath)
			}
		}

		clampCursor()

		kStr := msg.String()
		if kStr != "up" && kStr != "down" && kStr != "pgup" && kStr != "pgdown" {
			gutterWidth := 0
			if m.showLineNums {
				gutterWidth = 6
			}
			textWidth := m.viewport.Width - gutterWidth - 2
			if textWidth <= 0 {
				textWidth = 80
			}

			_, m.virtualPhysCol = cursorInRows(m.editorRows(), m.cursorRow, m.cursorCol)
		}
	}

Finalize:
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	if !m.draftReady && m.index >= 0 && m.index < len(m.notes) {
		m.Note = cloneNote(m.notes[m.index])
	}
	switch m.state {
	case stateTTLPicker, stateTTLCustom:
		return m.ttlPickerView()
	case stateConfirm:
		return m.confirmationView()
	case stateCommands:
		return m.commandView()
	case stateConflict:
		return m.conflictView()
	case stateHelp:
		return m.panel("Help", m.viewport.View(), "Esc Close · ↑↓ Scroll")
	case stateSearch:
		title := "Find notes: " + m.searchInput.View()
		body := "No matches"
		if m.searchErr != nil {
			body = "Search failed: " + m.searchErr.Error()
		} else if m.searchPending {
			body = "Searching…"
		} else if len(m.searchResults) > 0 {
			lines := []string{}
			visible := max(1, m.viewport.Height-3)
			start := max(0, m.searchIndex-visible+1)
			for i := start; i < min(len(m.searchResults), start+visible); i++ {
				prefix := "  "
				if i == m.searchIndex {
					prefix = "> "
				}
				preview := strings.ReplaceAll(strings.ReplaceAll(m.searchResults[i].Preview, "<<M_START>>", ""), "<<M_END>>", "")
				if m.searchResults[i].IsPinned {
					preview = "[Pinned] " + preview
				}
				lines = append(lines, prefix+preview)
			}
			body = strings.Join(lines, "\n")
		}
		return m.panel(title, body, "Esc Cancel · ↑↓ Choose · Enter Open")
	case statePromptFile:
		return m.panel("Export note", m.fileInput.View(), "Esc Cancel · Enter Write")
	case statePipe:
		return m.panel("Filter through local command", m.pipeInput.View(), "Esc Cancel · Enter Run")
	}
	l := m.layout()
	editor := m.renderLayoutEditor()
	if l.Height <= 1 {
		return clipFrame(editor, l.Width, 1)
	}
	status, _ := m.statusLayout()
	return clipFrame(editor+"\n"+status, l.Width, l.Height)
}
func (m model) renderEditorView() string { return m.renderLayoutEditor() }
