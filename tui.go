package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"regexp"

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
)

type textState struct {
	content string
	row     int
	col     int
}

type model struct {
	db       *sql.DB
	notes    []Note
	allNotes []Note
	index    int

	buffer         [][]rune
	cursorRow      int
	cursorCol      int
	virtualPhysCol int

	viewport    viewport.Model
	fileInput   textinput.Model
	searchInput textinput.Model
	pipeInput   textinput.Model

	state        appState
	prevState    appState
	searchQuery  string
	flashMsg     string
	showLineNums bool

	undoStack []textState
	redoStack []textState

	viewportTop  *int
	lastActivity time.Time
	freeScroll   bool

	theme Theme

	statActive int
	statTrash  int

	cachedHighlight string
	highlightDirty  bool

	needsSave bool

	searchResults []Note
	searchIndex   int
}


type tickMsg time.Time

type highlightMsg struct {
	originalText    string
	highlightedText string
}

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
	if len(stack) > 50 {
		stack = stack[1:] // Limit history to 50 states
	}
	return stack
}

type initDataMsg struct {
	theme Theme
	notes []Note
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
		db:             db,
		notes:          []Note{{ID: 0, Content: "Loading...", Preview: ""}},
		allNotes:       []Note{},
		index:          0,
		buffer:         stringToBuffer("Loading..."),
		cursorRow:      0,
		cursorCol:      0,
		virtualPhysCol: 0,
		fileInput:      fi,
		searchInput:    si,
		pipeInput:      pi,
		viewport:       vp,
		state:          stateTyping,
		showLineNums:   true,
		viewportTop:    new(int),
		// Provide a fallback theme while loading
		theme: Theme{BgDark: "#1E1E2E", TextLight: "#CDD6F4", AccentNormal: "#89B4FA"},
		flashMsg:       fmt.Sprintf("Started in %.2fms", float64(time.Since(startTime).Nanoseconds())/1e6),
	}

	m.cachedHighlight = "Loading..."
	m.highlightDirty = false

	return m
}

func (m model) Init() tea.Cmd {
	// Spin off I/O blocks into a background thread
	loadDataCmd := func() tea.Msg {
		theme := loadTheme()
		notes := loadNotes(m.db, false)
		
		if len(notes) == 0 {
			notes = []Note{{ID: 0, Content: "", Preview: ""}}
		} else if notes[0].ID > 0 {
			notes[0].Content = loadNoteContent(m.db, notes[0].ID)
		}
		
		return initDataMsg{theme: theme, notes: notes}
	}

	return tea.Batch(
		textinput.Blink,
		tickCmd(),
		cursorBlinkCmd(),
		loadDataCmd, // Send the msg when done
	)
}

func (m model) reload() model {
	inTrash := m.state == stateTrash
	if m.state == stateSearch {
		inTrash = m.prevState == stateTrash
	}

	if m.searchQuery != "" {
		m.notes = searchNotes(m.db, m.searchQuery, inTrash)
	} else {
		m.allNotes = loadNotes(m.db, inTrash)
		m.notes = m.allNotes
	}

	if len(m.notes) == 0 {
		m.notes = []Note{{ID: -1, Content: "--- No notes found ---", Preview: "--- No notes found ---"}}
	}

	if m.index >= len(m.notes) {
		m.index = len(m.notes) - 1
	}
	if m.index < 0 {
		m.index = 0
	}

	if len(m.notes) > 0 {
		if m.notes[m.index].ID > 0 && m.notes[m.index].Content == "" {
			m.notes[m.index].Content = loadNoteContent(m.db, m.notes[m.index].ID)
		}
		m.buffer = stringToBuffer(m.notes[m.index].Content)
		m.cursorRow, m.cursorCol = 0, 0
		m.virtualPhysCol = 0
		m.highlightDirty = true
	}
	return m
}

func (m *model) generateHelpText() string {
	termWidth := m.viewport.Width
	if termWidth == 0 {
		termWidth = 80
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal)).Bold(true)

	var docProps string
	if len(m.notes) > 0 && m.index >= 0 && m.index < len(m.notes) {
		currentNote := m.notes[m.index]
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

	cheatsheet := fmt.Sprintf(`
%s
Alt+Left/Right  : Switch Notes
Ctrl+Left/Right : Word Jump
Ctrl+U         : Promote current note to the front
Ctrl+N         : Create New Note
Ctrl+D         : Delete Current Note
Ctrl+S         : Save note to .md file
Alt+C          : Copy entire note to clipboard
Ctrl+T         : Open Trash Bin
Ctrl+F         : Find / Search Notes
F1             : Toggle Help

%s
Alt+M          : Toggle Math Mode ('code:' keyword disables lists)
Alt+V          : Toggle Code Mode
//             : Line comments (ignored by Math, Lists, and Expansions)
Ctrl+B / Alt+I : Bold (****) / Italic (**) 
`+"\x60 (1x / 3x)    : Auto-pair inline code / create multiline code block"+`
Ctrl+X         : Toggle Checkbox / Toggle Strikethrough
Ctrl+O/Alt+Ent : Smart open line below
Ctrl+K         : Delete Entire Line
Ctrl+Z/Alt+Z   : Undo  |  Ctrl+Y/Alt+Y : Redo
Tab / Shft+Tab : Indent / Un-indent line (4 spaces)
=              : Execute Math on line

%s
:today:        : e.g., 17 Apr 2026
:time:         : e.g., 3:04PM
:now:          : e.g., 3:04PM 17 Apr 2026
:uuid:         : e.g., 550e8400-e29b-41d4-a716-446655440000`,
		headerStyle.Render("=== GLOBAL NAVIGATION ==="),
		headerStyle.Render("=== EDITOR & MODES ==="),
		headerStyle.Render("=== MAGIC KEYWORDS ==="))

	dbPropsText := fmt.Sprintf("%s\nActive Notes : %d\nTrash Bin    : %d", headerStyle.Render("=== DATABASE ==="), m.statActive, m.statTrash)

	allTags := gatherAllTags(m.allNotes)
	tagsDisplay := strings.Join(allTags, "   ")
	if len(allTags) == 0 {
		tagsDisplay = "No tags found yet!"
	}
	wrappedTags := lipgloss.NewStyle().Width(termWidth - 10).Render("Your Tags:\n" + tagsDisplay)

	helpText := fmt.Sprintf("%s\n%s\n\n%s\n\n%s", docPropsText, cheatsheet, dbPropsText, wrappedTags)

	return lipgloss.NewStyle().Padding(2, 4).Render(helpText)
}

func isWordBoundary(r rune) bool {
	// Add or remove punctuation here based on how strict you want the jumps to be!
	return r == ' ' || r == '\t' || r == '\n' || r == '.' || r == ',' || r == ';' || r == ':' || 
		r == '!' || r == '?' || r == '-' || r == '(' || r == ')' || r == '[' || r == ']' || 
		r == '{' || r == '}' || r == '"' || r == '\''
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	inTrashContext := m.state == stateTrash ||
		(m.state == stateHelp && m.prevState == stateTrash) ||
		(m.state == stateSearch && m.prevState == stateTrash)

	if len(m.notes) > 0 && !inTrashContext {
		currentDoc := bufferToString(m.buffer)
		hash, duration := getKillTimerInfo(currentDoc)

		if m.notes[m.index].KillHash != hash {
			m.notes[m.index].KillHash = hash
			m.notes[m.index].KillStart = time.Now()
		}

		if duration != nil {
			killAt := m.notes[m.index].KillStart.Add(*duration)
			if time.Now().After(killAt) {
				if !m.notes[m.index].IsGhost {
					m.notes[m.index].Content = currentDoc
					m.notes[m.index] = saveNote(m.db, m.notes[m.index])
					trashNote(m.db, m.notes[m.index].ID)
				}

				m.notes = append(m.notes[:m.index], m.notes[m.index+1:]...)
				if len(m.notes) == 0 {
					m.notes = []Note{{Content: "", UpdatedAt: time.Now().Format("2006-01-02 15:04:05")}}
				}
				if m.index >= len(m.notes) {
					m.index = len(m.notes) - 1
				}
				if m.notes[m.index].ID > 0 && m.notes[m.index].Content == "" {
					m.notes[m.index].Content = loadNoteContent(m.db, m.notes[m.index].ID)
				}
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				m.cursorRow, m.cursorCol = 0, 0
				m.virtualPhysCol = 0

				if _, ok := msg.(tickMsg); ok {
					return m, tickCmd()
				}
				return m, nil
			}
		}
	}

	switch msg := msg.(type) {

	case initDataMsg:
		// Received the background data! Hydrate the UI.
		m.theme = msg.theme
		m.allNotes = msg.notes
		m.notes = msg.notes
		m.index = 0
		initialText := ""
		if len(m.notes) > 0 {
			initialText = m.notes[0].Content
		}
		m.buffer = stringToBuffer(initialText)
		
		// Immediately chain a command to calculate syntax highlighting on the new data
		highlightCmd := func() tea.Msg {
			content := bufferToString(m.buffer)
			return highlightMsg{
				originalText:    content,
				highlightedText: computeHighlight(content, m.theme),
			}
		}
		return m, highlightCmd

	case highlightMsg:
		if bufferToString(m.buffer) == msg.originalText {
			m.cachedHighlight = msg.highlightedText
		}
		return m, nil

	case tickMsg:
		cmds = append(cmds, tickCmd())
		if m.needsSave && time.Since(m.lastActivity) > 2*time.Second {
			if len(m.notes) > 0 && m.index < len(m.notes) && !m.notes[m.index].IsGhost {
				m.notes[m.index].Content = bufferToString(m.buffer)
				
				// 1. Save the note and check for duplicates
				savedNote := saveNote(m.db, m.notes[m.index])
				m.notes[m.index] = savedNote
				m.needsSave = false
				
				// 2. Instantly inform the user in the status bar
				if savedNote.IsDuplicate {
					m.flashMsg = "⚠️ Duplicate note detected!"
				} else {
					m.flashMsg = "Auto-saved"
				}
			}
		}

	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 1
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
				m.freeScroll = false
				if msg.Y >= m.viewport.Height {
					return m, nil
				}

				gutterWidth := 0
				if m.showLineNums {
					gutterWidth = 6
				}
				textWidth := m.viewport.Width - gutterWidth - 2
				if textWidth <= 0 {
					textWidth = 80
				}

				targetPhysLine := *m.viewportTop + msg.Y
				visualX := msg.X - gutterWidth
				if visualX < 0 {
					visualX = 0
				}

				currentPhysLine := 0
				found := false

				for i, line := range m.buffer {
					starts, lengths := getLineMap(line, textWidth)

					if currentPhysLine+len(starts) > targetPhysLine {
						m.cursorRow = i
						segmentIdx := targetPhysLine - currentPhysLine

						targetCol := starts[segmentIdx] + visualX
						segmentEnd := starts[segmentIdx] + lengths[segmentIdx]

						if targetCol > segmentEnd {
							targetCol = segmentEnd
						}
						if targetCol > len(line) {
							targetCol = len(line)
						}

						m.cursorCol = targetCol
						m.virtualPhysCol = visualX
						found = true
						break
					}
					currentPhysLine += len(starts)
				}

				if !found && len(m.buffer) > 0 {
					m.cursorRow = len(m.buffer) - 1
					m.cursorCol = len(m.buffer[m.cursorRow])

					starts, _ := getLineMap(m.buffer[m.cursorRow], textWidth)
					physRow := len(starts) - 1
					m.virtualPhysCol = m.cursorCol - starts[physRow]
				}

				clampCursor()
			}
		}
		return m, nil

	case tea.KeyMsg:
		m.lastActivity = time.Now()
		m.freeScroll = false
		m.highlightDirty = true

		if m.flashMsg != "" {
			m.flashMsg = ""
		}

		if m.state == stateSearch {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = m.prevState
				m.searchInput.Reset()
				
			case "up", "ctrl+k":
				if m.searchIndex > 0 {
					m.searchIndex--
				}
				
			case "down", "ctrl+j":
				if m.searchIndex < len(m.searchResults)-1 {
					m.searchIndex++
				}
				
			case "enter":
				if len(m.searchResults) > 0 {
					selectedNote := m.searchResults[m.searchIndex]
					m.buffer = stringToBuffer(selectedNote.Content)
					m.cachedHighlight = computeHighlight(selectedNote.Content, m.theme)
					m.state = stateTyping
					m.searchInput.Reset()
					
					// Jump exactly to the matched line and column
					m.cursorRow = selectedNote.MatchRow
					m.cursorCol = selectedNote.MatchCol
					m.virtualPhysCol = m.cursorCol // Remember column for up/down navigation
					
					// Scroll the viewport so the matched line is perfectly centered on screen
					*m.viewportTop = m.cursorRow - (m.viewport.Height / 2)
					if *m.viewportTop < 0 {
						*m.viewportTop = 0
					}
				}
				
			default:
				prevVal := m.searchInput.Value()
				m.searchInput, cmd = m.searchInput.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
				
				// Execute fuzzy search instantly if input changed
				if m.searchInput.Value() != prevVal {
					m.searchIndex = 0
					m.searchResults = searchNotes(m.db, m.searchInput.Value(), m.prevState == stateTrash)
				}
			}
			goto Finalize
		}

		if m.state == statePipe {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = stateTyping
				m.pipeInput.SetValue("")
			case "enter":
				cmdStr := strings.TrimSpace(m.pipeInput.Value())
				if cmdStr != "" {
					m.undoStack = pushStack(m.undoStack, bufferToString(m.buffer), m.cursorRow, m.cursorCol)
					m.redoStack = nil

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
						if len(m.undoStack) > 0 {
							m.undoStack = m.undoStack[:len(m.undoStack)-1]
						}
						errMsg := strings.TrimSpace(string(out))
						if errMsg == "" {
							errMsg = err.Error()
						}
						errMsg = strings.Split(errMsg, "\n")[0]
						if len(errMsg) > 55 {
							errMsg = errMsg[:52] + "..."
						}
						m.flashMsg = "Err: " + errMsg
					} else {
						finalText := string(out)
						finalText = strings.TrimRight(finalText, "\n")
						if len(headerLines) > 0 {
							finalText = strings.Join(headerLines, "\n") + "\n" + finalText
						}
						m.buffer = stringToBuffer(finalText)
						m.cursorRow, m.cursorCol = 0, 0
						m.virtualPhysCol = 0
						m.flashMsg = "Filtered!"
					}
				}
				m.state = stateTyping
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
				m.state = m.prevState
			case "up", "down", "pgup", "pgdown":
				m.viewport, cmd = m.viewport.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			goto Finalize
		}

		if m.state == stateTrash {
			switch msg.String() {
			case "esc", "ctrl+t":
				m.state = stateTyping
				m.searchQuery = ""
				m.searchInput.SetValue("")
				m.index = 0
				m = m.reload()
				clampCursor()
				m.virtualPhysCol = 0
			case "f1":
				m.prevState = m.state
				m.state = stateHelp
				m.statActive = len(loadNotes(m.db, false))
				m.statTrash = len(m.allNotes)
				m.viewport.SetContent(m.generateHelpText())
				m.viewport.GotoTop()
			case "ctrl+f":
				m.prevState = m.state
				m.state = stateSearch
				m.searchInput.Focus()
				m.searchInput.SetValue("") // Clear previous search
				// Dynamically check if we launched this from the trash
				m.searchResults = searchNotes(m.db, "", m.prevState == stateTrash)
				m.searchIndex = 0
				cmds = append(cmds, textinput.Blink)
			case "alt+right":
				if m.index < len(m.notes)-1 {
					m.index++
					if m.notes[m.index].ID > 0 && m.notes[m.index].Content == "" {
						m.notes[m.index].Content = loadNoteContent(m.db, m.notes[m.index].ID)
					}
					m.buffer = stringToBuffer(m.notes[m.index].Content)
					clampCursor()
					m.virtualPhysCol = 0
					*m.viewportTop = 0
				}
			case "alt+left":
				if m.index > 0 {
					m.index--
					if m.notes[m.index].ID > 0 && m.notes[m.index].Content == "" {
						m.notes[m.index].Content = loadNoteContent(m.db, m.notes[m.index].ID)
					}
					m.buffer = stringToBuffer(m.notes[m.index].Content)
					clampCursor()
					m.virtualPhysCol = 0
					*m.viewportTop = 0
				}
			case "ctrl+r":
				if m.notes[m.index].ID > 0 {
					// 1. Restore the note in the database
					restoreNote(m.db, m.notes[m.index].ID)
					
					// 2. Change state back to active notes so the user leaves the trash
					m.state = stateTyping 
					m.index = 0
					m = m.reload()
					
					// 3. Instantly reset the timer in memory so the background loop doesn't fight us
					m.notes[m.index].KillStart = time.Now()
					
					// 4. Reset cursor positions
					clampCursor()
					m.virtualPhysCol = 0
					*m.viewportTop = 0
					m.flashMsg = "Note Restored!"
				}
			case "up":
				if *m.viewportTop > 0 {
					*m.viewportTop--
				}
			case "down":
				*m.viewportTop++
			}
			goto Finalize
		}

		if m.state == statePromptFile {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = stateTyping
				m.fileInput.SetValue("")
			case "enter":
				path := m.fileInput.Value()
				if path != "" {
					if strings.HasPrefix(path, "~/") {
						home, _ := os.UserHomeDir()
						path = strings.Replace(path, "~", home, 1)
					}
					_ = os.WriteFile(path, []byte(bufferToString(m.buffer)), 0644)
				}
				m.state = stateTyping
				m.fileInput.SetValue("")
			default:
				m.fileInput, cmd = m.fileInput.Update(msg)
				if cmd != nil {
					cmds = append(cmds, cmd)
				}
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
		case "ctrl+g":
			m.notes[m.index].IsGhost = !m.notes[m.index].IsGhost
			if m.notes[m.index].IsGhost && m.notes[m.index].ID > 0 {
				deleteNote(m.db, m.notes[m.index].ID)
				m.notes[m.index].ID = 0
			} else {
				m.notes[m.index].Content = bufferToString(m.buffer)
				m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			}
			return m, nil

		case "esc", "ctrl+c":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])

			if m.searchQuery != "" {
				m.searchQuery = ""
				m.searchInput.SetValue("")
				m.index = 0
				m = m.reload()
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				clampCursor()
				m.virtualPhysCol = 0
				goto Finalize
			}
			return m, tea.Quit

		case "f1":
			if m.state == stateHelp {
				m.state = m.prevState
			} else {
				m.notes[m.index].Content = bufferToString(m.buffer)
				m.notes[m.index] = saveNote(m.db, m.notes[m.index])

				m.prevState = m.state
				m.state = stateHelp

				m.allNotes = loadNotes(m.db, false)

				m.statActive = len(m.allNotes)
				m.statTrash = len(loadNotes(m.db, true))

				m.viewport.SetContent(m.generateHelpText())
				m.viewport.GotoTop()
			}
			goto Finalize

		case "f2":
			m.showLineNums = !m.showLineNums
			goto Finalize

		case "ctrl+f":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.prevState = m.state
			m.state = stateSearch
			m.searchInput.Focus()
			m.searchInput.SetValue(m.searchQuery)
			m.searchInput.CursorEnd()
			m = m.reload()
			cmds = append(cmds, textinput.Blink)
			goto Finalize

		case "ctrl+u":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			if m.index > 0 {
				m = m.reload()
				m.index = 0
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				clampCursor()
				m.virtualPhysCol = 0
			}
			m.flashMsg = "Note Promoted!"

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

		case "ctrl+t":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.state = stateTrash
			m.index = 0
			m.searchQuery = ""
			m.searchInput.SetValue("")
			m.undoStack, m.redoStack = nil, nil
			m = m.reload()
			m.buffer = stringToBuffer(m.notes[m.index].Content)
			clampCursor()
			m.virtualPhysCol = 0

		case "ctrl+s":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.state = statePromptFile
			m.fileInput.SetValue(extractTitle(m.notes[m.index].Content) + ".md")
			m.fileInput.CursorEnd()
			m.fileInput.Focus()
			cmds = append(cmds, textinput.Blink)

		case "ctrl+n":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.searchQuery = ""
			m.searchInput.SetValue("")

			newNote := Note{
				ID:          0,
				Content:     "",
				UpdatedTime: time.Now(),
				CreatedAt:   time.Now().Format("02 Jan 2006, 3:04 PM"),
				CreatedTime: time.Now(),
			}
			m.allNotes = append([]Note{newNote}, m.allNotes...)

			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.searchQuery = ""
			m.searchInput.SetValue("")

			m.notes = m.allNotes
			m.index = 0
			m.buffer = stringToBuffer("")
			m.cursorRow, m.cursorCol = 0, 0
			m.virtualPhysCol = 0
			m.undoStack, m.redoStack = nil, nil

		case "ctrl+d":
			if len(m.notes) > 0 {
				if strings.TrimSpace(bufferToString(m.buffer)) == "" {
					if m.notes[m.index].ID > 0 {
						hardDeleteNote(m.db, m.notes[m.index].ID)
					}
				} else if !m.notes[m.index].IsGhost {
					m.notes[m.index].Content = bufferToString(m.buffer)
					m.notes[m.index] = saveNote(m.db, m.notes[m.index])
					trashNote(m.db, m.notes[m.index].ID)
				}
				m.index = 0
				m.undoStack, m.redoStack = nil, nil
				m = m.reload()
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				clampCursor()
				m.virtualPhysCol = 0
			}

		case "alt+c":
			copyToClipboard(bufferToString(m.buffer))
			m.flashMsg = "Copied!"

		case "alt+x":
			m.state = statePipe
			m.pipeInput.SetValue("")
			m.pipeInput.Focus()
			cmds = append(cmds, textinput.Blink)

		case "alt+right":
			if m.needsSave {
				m.notes[m.index].Content = bufferToString(m.buffer)
				m.notes[m.index] = saveNote(m.db, m.notes[m.index])
				m.needsSave = false
			}
			if m.index < len(m.notes)-1 {
				m.index++
				if m.notes[m.index].ID > 0 && m.notes[m.index].Content == "" {
					m.notes[m.index].Content = loadNoteContent(m.db, m.notes[m.index].ID)
				}
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				m.cursorRow, m.cursorCol = 0, 0
				m.virtualPhysCol = 0
				*m.viewportTop = 0
				m.undoStack, m.redoStack = nil, nil
			}

		case "alt+left":
			if m.needsSave {
				m.notes[m.index].Content = bufferToString(m.buffer)
				m.notes[m.index] = saveNote(m.db, m.notes[m.index])
				m.needsSave = false
			}
			if m.index > 0 {
				m.index--
				if m.notes[m.index].ID > 0 && m.notes[m.index].Content == "" {
					m.notes[m.index].Content = loadNoteContent(m.db, m.notes[m.index].ID)
				}
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				m.cursorRow, m.cursorCol = 0, 0
				m.virtualPhysCol = 0
				*m.viewportTop = 0
				m.undoStack, m.redoStack = nil, nil
			}

		case "up", "down", "pgup", "pgdown":
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

			physCol := m.virtualPhysCol
			if physCol < 0 {
				physCol = 0
			}

			if msg.String() == "up" {
				if physRow > 0 {
					targetRow := physRow - 1
					targetCol := physCol
					if targetCol > lengths[targetRow] {
						targetCol = lengths[targetRow]
					}
					m.cursorCol = starts[targetRow] + targetCol
				} else if m.cursorRow > 0 {
					m.cursorRow--
					pStarts, pLengths := getLineMap(m.buffer[m.cursorRow], textWidth)
					targetRow := len(pStarts) - 1
					targetCol := physCol
					if targetCol > pLengths[targetRow] {
						targetCol = pLengths[targetRow]
					}
					m.cursorCol = pStarts[targetRow] + targetCol
				}
			} else if msg.String() == "down" {
				if physRow < len(starts)-1 {
					targetRow := physRow + 1
					targetCol := physCol
					if targetCol > lengths[targetRow] {
						targetCol = lengths[targetRow]
					}
					m.cursorCol = starts[targetRow] + targetCol
				} else if m.cursorRow < len(m.buffer)-1 {
					m.cursorRow++
					nStarts, nLengths := getLineMap(m.buffer[m.cursorRow], textWidth)
					targetRow := 0
					targetCol := physCol
					if targetCol > nLengths[targetRow] {
						targetCol = nLengths[targetRow]
					}
					m.cursorCol = nStarts[targetRow] + targetCol
				}
			} else if msg.String() == "pgup" {
				m.cursorRow -= m.viewport.Height
				clampCursor()

				pStarts, pLengths := getLineMap(m.buffer[m.cursorRow], textWidth)
				targetRow := len(pStarts) - 1
				targetCol := physCol
				if targetCol > pLengths[targetRow] {
					targetCol = pLengths[targetRow]
				}
				m.cursorCol = pStarts[targetRow] + targetCol
			} else if msg.String() == "pgdown" {
				m.cursorRow += m.viewport.Height
				clampCursor()

				nStarts, nLengths := getLineMap(m.buffer[m.cursorRow], textWidth)
				targetRow := 0
				targetCol := physCol
				if targetCol > nLengths[targetRow] {
					targetCol = nLengths[targetRow]
				}
				m.cursorCol = nStarts[targetRow] + targetCol
			}
			clampCursor()

		case "left":
			if m.cursorCol > 0 {
				m.cursorCol--
			} else if m.cursorRow > 0 {
				m.cursorRow--
				m.cursorCol = len(m.buffer[m.cursorRow])
			}
		case "right":
			if m.cursorCol < len(m.buffer[m.cursorRow]) {
				m.cursorCol++
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
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol-1], m.buffer[m.cursorRow][m.cursorCol:]...)
				m.cursorCol--
			} else if m.cursorRow > 0 {
				prevLen := len(m.buffer[m.cursorRow-1])
				m.buffer[m.cursorRow-1] = append(m.buffer[m.cursorRow-1], m.buffer[m.cursorRow]...)
				m.buffer = append(m.buffer[:m.cursorRow], m.buffer[m.cursorRow+1:]...)
				m.cursorRow--
				m.cursorCol = prevLen
			}

		case "delete":
			if m.cursorCol < len(m.buffer[m.cursorRow]) {
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], m.buffer[m.cursorRow][m.cursorCol+1:]...)
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
			line := string(m.buffer[m.cursorRow])
			before := line[:m.cursorCol]
			after := line[m.cursorCol:]

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

		if len(m.buffer) > 0 && len(m.buffer[len(m.buffer)-1]) > 0 {
			m.buffer = append(m.buffer, []rune{})
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
			m.virtualPhysCol = m.cursorCol - starts[physRow]
		}
	}

Finalize:
	if m.highlightDirty {
		m.highlightDirty = false
		content := bufferToString(m.buffer)
		m.cachedHighlight = content

		cmds = append(cmds, func() tea.Msg {
			hl := computeHighlight(content, m.theme)
			return highlightMsg{
				originalText:    content,
				highlightedText: hl,
			}
		})
	}

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	bgDark := lipgloss.Color(m.theme.BgDark)
	bgMed := lipgloss.Color(m.theme.BgMed)
	textLight := lipgloss.Color(m.theme.TextLight)
	textDark := lipgloss.Color(m.theme.TextDark)

	accentNormal := lipgloss.Color(m.theme.AccentNormal)
	accentMath := lipgloss.Color(m.theme.AccentMath)
	accentCode := lipgloss.Color(m.theme.AccentCode)
	accentTrash := lipgloss.Color(m.theme.AccentTrash)

	currentText := bufferToString(m.buffer)
	words := len(strings.Fields(currentText))
	chars := len(currentText)

	modeStr := "NORMAL "
	modeColor := accentNormal
	val := strings.ToLower(strings.TrimSpace(currentText))
	if strings.HasPrefix(val, "math:") {
		modeStr = "MATH "
		modeColor = accentMath
	} else if strings.HasPrefix(val, "code:") {
		modeStr = "CODE "
		modeColor = accentCode
	}

	switch m.state {
	case stateTrash:
		modeStr = "TRASH "
		modeColor = accentTrash
	case stateHelp:
		modeStr = "HELP "
		modeColor = accentNormal
	case stateSearch:
		modeStr = "FIND "
		modeColor = accentMath
	}

	modeBlock := lipgloss.NewStyle().Background(modeColor).Foreground(textDark).Bold(true).Padding(0, 1).Render(modeStr)

	searchTag := ""
	if m.searchQuery != "" {
		searchTag = fmt.Sprintf(" [Find: %s] ", m.searchQuery)
	}

	var noteInfoStr string
	if len(m.notes) == 1 && m.notes[0].ID == -1 {
		noteInfoStr = fmt.Sprintf(" Note 0/0%s ", searchTag)
	} else {
		noteInfoStr = fmt.Sprintf(" Note %d/%d%s ", m.index+1, len(m.notes), searchTag)
	}

	if m.notes[m.index].IsGhost {
		noteInfoStr = lipgloss.NewStyle().Background(lipgloss.Color(bgMed)).Foreground(textLight).Bold(true).Padding(0, 1).Render("󰊠")
	}
	if m.flashMsg != "" {
		noteInfoStr = " ★ " + m.flashMsg + " "
	}

	noteBlock := lipgloss.NewStyle().Background(bgMed).Foreground(textLight).Padding(0, 1).Render(noteInfoStr)
	statBlock := lipgloss.NewStyle().Background(bgDark).Foreground(textLight).Padding(0, 1).Render(fmt.Sprintf(" %dW %dC", words, chars))

	headingBlock := ""
	heading := getFirstHeading(currentText)
	if heading != "Untitled" && heading != "" {
		headingBlock = lipgloss.NewStyle().Background(bgDark).Foreground(textLight).Padding(0, 1).Render("│  " + heading)
	}

	killBlock := ""
	if m.state != stateTrash {
		hash, duration := getKillTimerInfo(currentText)

		if m.notes[m.index].KillHash != hash {
			m.notes[m.index].KillHash = hash
			m.notes[m.index].KillStart = time.Now()
		}

		if duration != nil {
			killAt := m.notes[m.index].KillStart.Add(*duration)
			timeLeft := time.Until(killAt)
			if timeLeft > 0 {
				timeStr := formatKillTime(timeLeft)
				killBlock = lipgloss.NewStyle().
					Background(bgDark).
					Foreground(lipgloss.Color("#F38BA8")).
					Bold(true).
					Padding(0, 1).
					Render(fmt.Sprintf("KILL: %s", timeStr))
			}
		}
	}

	leftCap := lipgloss.NewStyle().Foreground(modeColor).Render("")
	rightCap := lipgloss.NewStyle().Foreground(bgMed).Render("")

	leftBarBlocks := []string{leftCap, modeBlock, noteBlock, statBlock}
	if headingBlock != "" {
		leftBarBlocks = append(leftBarBlocks, headingBlock)
	}
	if killBlock != "" {
		leftBarBlocks = append(leftBarBlocks, killBlock)
	}

	leftBar := lipgloss.JoinHorizontal(lipgloss.Top, leftBarBlocks...)

	timeStr := " " + formatRelativeTime(m.notes[m.index].UpdatedTime) + " "
	if m.state == stateTrash {
		if m.notes[m.index].DeletedTime.IsZero() {
			timeStr = " Deleted: Unknown "
		} else {
			timeStr = " Deleted: " + formatRelativeTime(m.notes[m.index].DeletedTime) + " "
		}
	}

	saveBlock := lipgloss.NewStyle().Background(bgMed).Foreground(textLight).Padding(0, 1).Render(timeStr)
	rightBar := lipgloss.JoinHorizontal(lipgloss.Top, saveBlock, rightCap)

	termWidth := m.viewport.Width
	if termWidth == 0 {
		termWidth = 80
	}

	spacerWidth := termWidth - lipgloss.Width(leftBar) - lipgloss.Width(rightBar)
	if spacerWidth < 0 {
		spacerWidth = 0
	}
	spacerStyle := lipgloss.NewStyle().Background(bgDark).Render(strings.Repeat(" ", spacerWidth))

	statusBar := lipgloss.JoinHorizontal(lipgloss.Top, leftBar, spacerStyle, rightBar)
	statusBar = lipgloss.NewStyle().MaxWidth(termWidth).Render(statusBar)

	if m.state == stateHelp {
		return m.viewport.View()
	}

	if m.state == stateSearch {
		// Define the accent color if it isn't already defined earlier in View()
		accentNormal := lipgloss.Color(m.theme.AccentNormal) 

		// 1. Build the Search Input Box
		inputBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accentNormal).
			Padding(0, 1).
			Width(60).
			Render(m.searchInput.View())

		// 2. Build the Results List
		var resultLines []string
		if len(m.searchResults) == 0 {
			resultLines = append(resultLines, lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Comment)).Render("  No matches found..."))
		} else {
			// --- SCROLLING LOGIC START ---
			maxVisible := 10 // Maximum items to show inside the bordered box
			start := 0

			// Shift the window down if the cursor goes past the max limit
			if m.searchIndex >= maxVisible {
				start = m.searchIndex - maxVisible + 1
			}

			end := start + maxVisible
			if end > len(m.searchResults) {
				end = len(m.searchResults)
			}
			// --- SCROLLING LOGIC END ---

			// Use a standard for-loop bounded by our scrolling window
			for i := start; i < end; i++ {
				res := m.searchResults[i]
				cursor := "  "
				style := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.TextLight))

				if i == m.searchIndex {
					cursor = "❯ "
					style = style.Foreground(accentNormal).Bold(true)
				}

				// --- CLEAN UP THE SQLITE SEARCH TAGS ---
				cleanPreview := strings.ReplaceAll(res.Preview, "<<M_START>>", "")
				cleanPreview = strings.ReplaceAll(cleanPreview, "<<M_END>>", "")

				// Use cleanPreview instead of res.Preview
				resultLines = append(resultLines, style.Render(fmt.Sprintf("%s%s", cursor, cleanPreview)))
			}
		}

		resultsBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(m.theme.BgMed)).
			Padding(0, 1).
			Width(60).
			Render(strings.Join(resultLines, "\n"))

		// 3. Stack them and center on screen
		modal := lipgloss.JoinVertical(lipgloss.Left, inputBox, resultsBox)
		centeredBox := lipgloss.Place(termWidth, m.viewport.Height, lipgloss.Center, lipgloss.Center, modal)
		
		return lipgloss.JoinVertical(lipgloss.Left, centeredBox, statusBar)
	}

	if m.state == statePromptFile {
		p := lipgloss.NewStyle().Foreground(accentNormal).Bold(true).Render("Save Note to File") + "\n\n" + m.fileInput.View()
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentNormal).Padding(1, 3).Render(p)
		centeredBox := lipgloss.Place(termWidth, m.viewport.Height, lipgloss.Center, lipgloss.Center, box)
		return lipgloss.JoinVertical(lipgloss.Left, centeredBox, statusBar)
	}

	if m.state == statePipe {
		p := lipgloss.NewStyle().Foreground(accentNormal).Bold(true).Render("Filter Note through Shell Command") + "\n\n" + m.pipeInput.View()
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentNormal).Padding(1, 3).Render(p)
		centeredBox := lipgloss.Place(termWidth, m.viewport.Height, lipgloss.Center, lipgloss.Center, box)
		return lipgloss.JoinVertical(lipgloss.Left, centeredBox, statusBar)
	}

	if m.state == stateSearch {
		inputBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(m.theme.AccentNormal)).
			Padding(0, 1).
			Width(60).
			Render(m.searchInput.View())

		// 2. Build the Results List
		var resultLines []string
		if len(m.searchResults) == 0 {
			resultLines = append(resultLines, lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Comment)).Render("  No matches found..."))
		} else {
			for i, res := range m.searchResults {
				cursor := "  "
				baseStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.TextLight))

				if i == m.searchIndex {
					cursor = "❯ "
					baseStyle = baseStyle.Foreground(accentNormal).Bold(true)
				}

				// Highlight style: Dark text on Accent-colored background
				hlStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.BgDark)).Background(accentNormal).Bold(true)

				// Parse the <<M_START>> and <<M_END>> tags dynamically
				parts := strings.Split(res.Preview, "<<M_START>>")
				formattedPreview := baseStyle.Render(parts[0])
				
				for p := 1; p < len(parts); p++ {
					subParts := strings.SplitN(parts[p], "<<M_END>>", 2)
					if len(subParts) == 2 {
						// Apply Highlight style to matched text, then revert to baseStyle
						formattedPreview += hlStyle.Render(subParts[0]) + baseStyle.Render(subParts[1])
					} else {
						formattedPreview += baseStyle.Render(parts[p])
					}
				}

				resultLines = append(resultLines, baseStyle.Render(cursor)+formattedPreview)
			}
		}

		resultsBox := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(m.theme.BgMed)).
			Padding(0, 1).
			Width(60).
			Render(strings.Join(resultLines, "\n"))

		modal := lipgloss.JoinVertical(lipgloss.Left, inputBox, resultsBox)

		// This overlays the modal in the center of your existing UI
		return lipgloss.Place(
			m.viewport.Width, m.viewport.Height,
			lipgloss.Center, lipgloss.Center,
			modal,
			lipgloss.WithWhitespaceChars(" "),
		)
	}

	return fmt.Sprintf("%s\n%s", m.renderEditorView(), statusBar)
}

func (m *model) renderEditorView() string {
	highlighted := m.cachedHighlight

	charUnderCursor := " "
	if m.cursorCol < len(m.buffer[m.cursorRow]) {
		charUnderCursor = string(m.buffer[m.cursorRow][m.cursorCol])
	}

	cursorStyle := lipgloss.NewStyle().Reverse(true)
	visibleCursor := cursorStyle.Render(charUnderCursor)

	logicalLinesHl := strings.Split(highlighted, "\n")

	if len(logicalLinesHl) > len(m.buffer) {
		logicalLinesHl = logicalLinesHl[:len(m.buffer)]
	} else if len(logicalLinesHl) < len(m.buffer) {
		for len(logicalLinesHl) < len(m.buffer) {
			logicalLinesHl = append(logicalLinesHl, "")
		}
	}

	var physicalLines []string

	cursorPhysicalRow := 0
	physicalLineIdx := 0

	gutterWidth := 0
	if m.showLineNums {
		gutterWidth = 6
	}
	textWidth := m.viewport.Width - gutterWidth - 2
	if textWidth <= 0 {
		textWidth = 80
	}

	for i, logicalLineHl := range logicalLinesHl {
		starts, lengths := getLineMap(m.buffer[i], textWidth)
		isCursorLine := (i == m.cursorRow)

		var physSegments []string
		rawLine := string(m.buffer[i])

		if strings.TrimSpace(rawLine) == "---" && !isCursorLine {
			availableWidth := m.viewport.Width
			if m.showLineNums {
				availableWidth -= 6
			}
			if availableWidth < 1 {
				availableWidth = 1
			}
			
			// Draw an edge-to-edge line using box-drawing characters
			hr := strings.Repeat("─", availableWidth)
			styledHr := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Comment)).Render(hr)
			
			physSegments = []string{styledHr}
		} else {
			// Normal text rendering
			physSegments = buildPhysicalLines(logicalLineHl, m.cursorCol, isCursorLine, starts, lengths, visibleCursor)
		}

		physSegments = carryANSI(physSegments)

		for j, pLine := range physSegments {
			if m.showLineNums {
				if j == 0 {
					numStr := fmt.Sprintf("%3d │ ", i+1)
					gutter := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Comment)).Render(numStr)
					physicalLines = append(physicalLines, gutter+pLine)
				} else {
					emptyGutter := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Comment)).Render("    │ ")
					physicalLines = append(physicalLines, emptyGutter+pLine)
				}
			} else {
				physicalLines = append(physicalLines, pLine)
			}
		}

		if isCursorLine {
			for p, start := range starts {
				if p < len(starts)-1 {
					if m.cursorCol >= start && m.cursorCol < starts[p+1] {
						cursorPhysicalRow = physicalLineIdx + p
						break
					}
				} else {
					cursorPhysicalRow = physicalLineIdx + p
				}
			}
		}

		physicalLineIdx += len(physSegments)
	}

	if !m.freeScroll {
		if cursorPhysicalRow < *m.viewportTop {
			*m.viewportTop = cursorPhysicalRow
		} else if cursorPhysicalRow >= *m.viewportTop+m.viewport.Height {
			*m.viewportTop = cursorPhysicalRow - m.viewport.Height + 1
		}
	}

	maxTop := len(physicalLines) - m.viewport.Height
	if maxTop < 0 {
		maxTop = 0
	}
	if *m.viewportTop > maxTop {
		*m.viewportTop = maxTop
	}
	if *m.viewportTop < 0 {
		*m.viewportTop = 0
	}

	startRow := *m.viewportTop
	if startRow < 0 {
		startRow = 0
	}

	endRow := startRow + m.viewport.Height
	if endRow > len(physicalLines) {
		endRow = len(physicalLines)
	}
	if startRow > endRow {
		startRow = endRow
	}

	visibleLines := physicalLines[startRow:endRow]

	for len(visibleLines) < m.viewport.Height {
		if m.showLineNums {
			emptyGutter := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Comment)).Render("    │ ")
			visibleLines = append(visibleLines, emptyGutter)
		} else {
			visibleLines = append(visibleLines, "")
		}
	}

	return strings.Join(visibleLines, "\n")
}