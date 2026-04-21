package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Knetic/govaluate"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type appState int

// --- CONFIGURATION ---
type Theme struct {
	BgDark       string `json:"bg_dark"`
	BgMed        string `json:"bg_med"`
	TextLight    string `json:"text_light"`
	TextDark     string `json:"text_dark"`
	AccentNormal string `json:"accent_normal"`
	AccentMath   string `json:"accent_math"`
	AccentCode   string `json:"accent_code"`
	AccentTrash  string `json:"accent_trash"`
	Comment      string `json:"comment"`
}

func loadTheme() Theme {
	defaultTheme := Theme{
		BgDark:       "#1E1E2E",
		BgMed:        "#313244",
		TextLight:    "#CDD6F4",
		TextDark:     "#11111B",
		AccentNormal: "#89B4FA",
		AccentMath:   "#F9E2AF",
		AccentCode:   "#A6E3A1",
		AccentTrash:  "#F38BA8",
		Comment:      "#6C7086",
	}

	configDir := filepath.Join(os.Getenv("HOME"), ".config", "quicknote-tui")
	_ = os.MkdirAll(configDir, 0755)
	themePath := filepath.Join(configDir, "theme.json")

	data, err := os.ReadFile(themePath)
	if err != nil {
		// Create the default config file so the user can easily find and edit it later
		b, _ := json.MarshalIndent(defaultTheme, "", "  ")
		_ = os.WriteFile(themePath, b, 0644)
		return defaultTheme
	}

	var t Theme
	if err := json.Unmarshal(data, &t); err != nil {
		return defaultTheme
	}
	return t
}

type searchMode int

const (
	modeFuzzy searchMode = iota
	modeLiteral
	modeTag
)

const (
	stateTyping appState = iota
	stateExport
	statePromptFile
	stateTrash
	stateHelp
	stateSearch
	statePipe
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
	db       *sql.DB
	notes    []Note
	allNotes []Note
	index    int

	buffer         [][]rune
	cursorRow      int
	cursorCol      int
	virtualPhysCol int // THE FIX: "Remembers" intended column when moving vertically
	sMode          searchMode

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

	viewportTop *int
	// cursorPhase  bool
	lastActivity time.Time
	freeScroll   bool

	theme Theme

	statActive int // <-- ADD THIS
	statTrash  int // <-- ADD THIS
}

func generateUUID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "00000000-0000-0000-0000-000000000000" // Fallback if crypto fails
	}

	// Set the mathematically required Version 4 and Variant 10 bits
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// --- Buffer Helpers ---
func stringToBuffer(s string) [][]rune {
	if s == "" {
		return [][]rune{{}}
	}
	lines := strings.Split(s, "\n")
	buffer := make([][]rune, len(lines))
	for i, line := range lines {
		buffer[i] = []rune(line)
	}
	return buffer
}

func bufferToString(b [][]rune) string {
	var builder strings.Builder
	for i, line := range b {
		builder.WriteString(string(line))
		if i < len(b)-1 {
			builder.WriteRune('\n')
		}
	}
	return builder.String()
}

// THE FIX: Parses the ORIGINAL segment to prevent exponential memory leaks
// while keeping syntax colors active across wrapped physical lines!
func carryANSI(segments []string) []string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	var activeANSI []string

	for i := range segments {
		originalSegment := segments[i]

		prefix := strings.Join(activeANSI, "")
		segments[i] = prefix + originalSegment

		matches := re.FindAllString(originalSegment, -1)
		for _, match := range matches {
			if match == "\x1b[0m" || match == "\x1b[m" || match == "\x1b[39m" || match == "\x1b[49m" || match == "\x1b[0;0m" {
				activeANSI = nil
			} else {
				activeANSI = append(activeANSI, match)
			}
		}

		if len(activeANSI) > 0 && !strings.HasSuffix(segments[i], "\x1b[0m") {
			segments[i] = segments[i] + "\x1b[0m"
		}
	}
	return segments
}

// THE FIX: Safely slices ANSI strings, injects the cursor, and RESTORES active colors
// so Lipgloss's \x1b[0m reset doesn't kill Chroma's syntax highlighting!
func buildPhysicalLines(ansiLine string, cursorCol int, isCursorLine bool, starts []int, lengths []int, visibleCursor string) []string {
	var physLines []string
	var currentPhys strings.Builder

	visualIdx := 0
	inAnsi := false
	physIdx := 0

	var activeANSI []string
	var currentANSI strings.Builder

	runes := []rune(ansiLine)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if r == '\x1b' {
			inAnsi = true
			currentANSI.Reset()
		}
		if inAnsi {
			currentPhys.WriteRune(r)
			currentANSI.WriteRune(r)
			if r == 'm' {
				inAnsi = false
				code := currentANSI.String()
				// Track the active color state
				if code == "\x1b[0m" || code == "\x1b[m" || code == "\x1b[39m" || code == "\x1b[49m" || code == "\x1b[0;0m" {
					activeANSI = nil
				} else {
					activeANSI = append(activeANSI, code)
				}
			}
			continue
		}

		if physIdx < len(starts) && visualIdx >= starts[physIdx]+lengths[physIdx] {
			if physIdx+1 < len(starts) && visualIdx == starts[physIdx+1] {
				physLines = append(physLines, currentPhys.String())
				currentPhys.Reset()
				physIdx++
			}
		}

		if physIdx < len(starts) && visualIdx >= starts[physIdx] && visualIdx < starts[physIdx]+lengths[physIdx] {
			if isCursorLine && visualIdx == cursorCol {
				currentPhys.WriteString(visibleCursor)
				// RE-APPLY THE CHROMA COLORS immediately after the cursor's reset code
				for _, ansi := range activeANSI {
					currentPhys.WriteString(ansi)
				}
			} else {
				currentPhys.WriteRune(r)
			}
		} else {
			if isCursorLine && visualIdx == cursorCol {
				currentPhys.WriteString(visibleCursor)
				// RE-APPLY THE CHROMA COLORS
				for _, ansi := range activeANSI {
					currentPhys.WriteString(ansi)
				}
			}
		}
		visualIdx++
	}

	if isCursorLine && visualIdx == cursorCol {
		currentPhys.WriteString(visibleCursor)
	}

	physLines = append(physLines, currentPhys.String())
	return physLines
}

// THE FIX: Safely highlights characters at specific indices without breaking Chroma!
func applySearchHighlight(highlighted string, indices []int, theme Theme) string {
	if len(indices) == 0 {
		return highlighted
	}

	// High contrast background for the matched characters
	searchStyle := lipgloss.NewStyle().Background(lipgloss.Color(theme.AccentMath)).Foreground(lipgloss.Color(theme.BgDark)).Render

	var buf strings.Builder
	visualIdx := 0
	inAnsi := false
	var activeANSI []string
	var currentANSI strings.Builder

	idxPos := 0
	runes := []rune(highlighted)

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if r == '\x1b' {
			inAnsi = true
			currentANSI.Reset()
		}
		if inAnsi {
			buf.WriteRune(r)
			currentANSI.WriteRune(r)
			if r == 'm' {
				inAnsi = false
				code := currentANSI.String()
				if code == "\x1b[0m" || code == "\x1b[m" || code == "\x1b[39m" || code == "\x1b[49m" || code == "\x1b[0;0m" {
					activeANSI = nil
				} else {
					activeANSI = append(activeANSI, code)
				}
			}
			continue
		}

		isMatch := false
		if idxPos < len(indices) && visualIdx == indices[idxPos] {
			isMatch = true
			idxPos++
		}

		if isMatch {
			buf.WriteString(searchStyle(string(r)))
			// Restore previous active ANSI colors because lipgloss appends a reset
			for _, ansi := range activeANSI {
				buf.WriteString(ansi)
			}
		} else {
			buf.WriteRune(r)
		}

		visualIdx++
	}

	return buf.String()
}

func getLineMap(runes []rune, width int) ([]int, []int) {
	if width <= 0 {
		width = 80
	}
	if len(runes) == 0 {
		return []int{0}, []int{0}
	}

	var starts []int
	var lengths []int
	start := 0

	for start < len(runes) {
		remain := len(runes) - start
		if remain <= width {
			starts = append(starts, start)
			lengths = append(lengths, remain)
			break
		}

		breakPoint := -1
		for i := start + width; i >= start; i-- {
			if i < len(runes) && runes[i] == ' ' {
				breakPoint = i
				break
			}
		}

		if breakPoint == -1 {
			for i := start + width - 1; i >= start; i-- {
				if i < len(runes) {
					r := runes[i]
					if r == ',' || r == '.' || r == ';' || r == ':' || r == '-' || r == '}' || r == ']' || r == ')' || r == '{' || r == '[' || r == '(' || r == '/' || r == '\\' || r == '"' || r == '\'' {
						breakPoint = i + 1
						break
					}
				}
			}
		}

		if breakPoint > start {
			starts = append(starts, start)
			lengths = append(lengths, breakPoint-start)
			if breakPoint < len(runes) && runes[breakPoint] == ' ' {
				start = breakPoint + 1
			} else {
				start = breakPoint
			}
		} else {
			starts = append(starts, start)
			lengths = append(lengths, width)
			start += width
		}
	}
	return starts, lengths
}

// --- Background Heartbeat ---
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

// --- Ephemeral Note Helpers ---

func getFirstHeading(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			return strings.TrimPrefix(trimmed, "# ")
		} else if strings.HasPrefix(trimmed, "## ") {
			return strings.TrimPrefix(trimmed, "## ")
		} else if strings.HasPrefix(trimmed, "### ") {
			return strings.TrimPrefix(trimmed, "### ")
		}
	}
	return "Untitled"
}

func getKillTimerInfo(content string) (string, *time.Duration) {
	re := regexp.MustCompile(`(?i)#kill\s+([0-9.]+)\s*([a-zA-Z]+)`)
	matches := re.FindAllStringSubmatch(content, -1)

	if len(matches) == 0 {
		return "", nil
	}

	var hashBuilder strings.Builder
	for _, m := range matches {
		hashBuilder.WriteString(m[0])
		hashBuilder.WriteString("|")
	}
	hashStr := hashBuilder.String()

	lastMatch := matches[len(matches)-1]
	valFloat, err := strconv.ParseFloat(lastMatch[1], 64)
	if err != nil {
		return hashStr, nil
	}
	unit := strings.ToLower(lastMatch[2])

	var d time.Duration
	switch unit {
	case "mo", "mon", "month", "months":
		d = time.Duration(valFloat * 30 * 24 * float64(time.Hour))
	case "w", "wk", "wks", "week", "weeks":
		d = time.Duration(valFloat * 7 * 24 * float64(time.Hour))
	case "d", "day", "days":
		d = time.Duration(valFloat * 24 * float64(time.Hour))
	case "h", "hr", "hrs", "hour", "hours":
		d = time.Duration(valFloat * float64(time.Hour))
	case "m", "min", "mins", "minute", "minutes":
		d = time.Duration(valFloat * float64(time.Minute))
	case "s", "sec", "secs", "second", "seconds":
		d = time.Duration(valFloat * float64(time.Second))
	default:
		return hashStr, nil
	}

	return hashStr, &d
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

// --- Smart List Helpers ---

func toAlpha(n int, lower bool) string {
	res := ""
	for n > 0 {
		n--
		res = string(rune('A'+(n%26))) + res
		n /= 26
	}
	if lower {
		return strings.ToLower(res)
	}
	return res
}

func toRoman(n int, lower bool) string {
	vals := []int{1000, 900, 500, 400, 100, 90, 50, 40, 10, 9, 5, 4, 1}
	syms := []string{"M", "CM", "D", "CD", "C", "XC", "L", "XL", "X", "IX", "V", "IV", "I"}
	res := ""
	for i := 0; i < len(vals); i++ {
		for n >= vals[i] {
			n -= vals[i]
			res += syms[i]
		}
	}
	if lower {
		return strings.ToLower(res)
	}
	return res
}

func romanToInt(s string) int {
	s = strings.ToUpper(s)
	romanMap := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	total := 0
	for i := 0; i < len(s); i++ {
		val := romanMap[s[i]]
		if i+1 < len(s) && romanMap[s[i+1]] > val {
			total -= val
		} else {
			total += val
		}
	}
	if total == 0 {
		return 1
	}
	return total
}

func alphaToInt(s string) int {
	s = strings.ToUpper(s)
	total := 0
	for i := 0; i < len(s); i++ {
		total = total*26 + int(s[i]-'A'+1)
	}
	return total
}

func formatItem(count int, style int) string {
	switch style {
	case 0:
		return strconv.Itoa(count)
	case 1:
		return toAlpha(count, true)
	case 2:
		return toRoman(count, true)
	case 3:
		return toAlpha(count, false)
	case 4:
		return toRoman(count, false)
	}
	return strconv.Itoa(count)
}

func guessStyle(token string) (int, int) {
	if num, err := strconv.Atoi(token); err == nil {
		return num, 0
	}

	isLowerRoman := regexp.MustCompile(`^[ivxlcdm]+$`).MatchString(token)
	isUpperRoman := regexp.MustCompile(`^[IVXLCDM]+$`).MatchString(token)

	if isLowerRoman && (len(token) > 1 || token == "i" || token == "v" || token == "x") {
		return romanToInt(token), 2
	}
	if isUpperRoman && (len(token) > 1 || token == "I" || token == "V" || token == "X") {
		return romanToInt(token), 4
	}

	if regexp.MustCompile(`^[a-z]+$`).MatchString(token) {
		return alphaToInt(token), 1
	}
	if regexp.MustCompile(`^[A-Z]+$`).MatchString(token) {
		return alphaToInt(token), 3
	}

	return 1, 0
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

func formatKillTime(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours() / 24)
	hours := int(int(d.Hours()) % 24)
	minutes := int(int(d.Minutes()) % 60)
	seconds := int(int(d.Seconds()) % 60)

	if days > 30 {
		months := days / 30
		remDays := days % 30
		if remDays > 0 {
			return fmt.Sprintf("%dmo %dd", months, remDays)
		}
		return fmt.Sprintf("%dmo", months)
	}
	if days > 0 {
		if hours > 0 {
			return fmt.Sprintf("%dd %dh", days, hours)
		}
		return fmt.Sprintf("%dd", days)
	}
	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	}
	if minutes > 0 {
		if seconds > 0 {
			return fmt.Sprintf("%dm %ds", minutes, seconds)
		}
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%ds", seconds)
}

func expandKeywords(val string, isCodeMode bool, lastKey string) (string, bool) {
	if isCodeMode {
		return val, false
	}

	if lastKey != ":" {
		return val, false
	}

	if !strings.Contains(val, ":") {
		return val, false
	}

	re := regexp.MustCompile(`:(today|Today|time|Time|now|Now)(?:\s*([\+\-])\s*(\d+(?:\.\d+)?)\s*([a-zA-Z]+))?:`)

	changed := false

	for strings.Contains(val, ":uuid:") {
		val = strings.Replace(val, ":uuid:", generateUUID(), 1)
		changed = true
	}

	lines := strings.Split(val, "\n")

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		matches := re.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			fullMatch := match[0]
			base := match[1]
			op := match[2]
			valStr := match[3]
			unit := strings.ToLower(match[4])

			outTime := time.Now()

			if op != "" {
				valFloat, _ := strconv.ParseFloat(valStr, 64)
				if op == "-" {
					valFloat = -valFloat
				}
				valInt := int(valFloat)

				switch unit {
				case "y", "yr", "yrs", "year", "years":
					outTime = outTime.AddDate(valInt, 0, 0)
				case "mo", "mon", "month", "months":
					outTime = outTime.AddDate(0, valInt, 0)
				case "w", "wk", "week", "weeks":
					outTime = outTime.AddDate(0, 0, valInt*7)
				case "d", "day", "days":
					outTime = outTime.AddDate(0, 0, valInt)
				case "h", "hr", "hrs", "hour", "hours":
					outTime = outTime.Add(time.Duration(valFloat * float64(time.Hour)))
				case "m", "min", "mins", "minute", "minutes":
					outTime = outTime.Add(time.Duration(valFloat * float64(time.Minute)))
				case "s", "sec", "secs", "second", "seconds":
					outTime = outTime.Add(time.Duration(valFloat * float64(time.Second)))
				}
			}

			var formatted string
			switch base {
			case "today":
				formatted = outTime.Format("02 Jan 2006")
			case "Today":
				formatted = outTime.Format("Mon 02 Jan 2006")
			case "time":
				formatted = outTime.Format("3:04PM")
			case "Time":
				formatted = outTime.Format("3:04:05PM")
			case "now":
				formatted = outTime.Format("3:04PM 02 Jan 2006")
			case "Now":
				formatted = outTime.Format("3:04PM Mon 02 Jan 2006")
			}

			lines[i] = strings.Replace(lines[i], fullMatch, formatted, 1)
			changed = true
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

func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return "Just now"
	}
	d := time.Since(t)
	if d < time.Minute {
		return "Just now"
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 min ago"
		}
		return fmt.Sprintf("%d mins ago", mins)
	}
	if d < 24*time.Hour {
		hrs := int(d.Hours())
		if hrs == 1 {
			return "1 hr ago"
		}
		return fmt.Sprintf("%d hrs ago", hrs)
	}
	days := int(d.Hours() / 24)
	if days == 1 {
		return "1 day ago"
	}
	if days < 30 {
		return fmt.Sprintf("%d days ago", days)
	}
	return t.Format("02 Jan 2006")
}

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
	type listState struct {
		count int
		style int
	}
	counters := make(map[string]*listState)

	reMatch := regexp.MustCompile(`^(\s*)([a-zA-Z0-9]+)\.\s+(.*)`)

	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			counters = make(map[string]*listState)
			continue
		}

		if match := reMatch.FindStringSubmatch(line); match != nil {
			indent := match[1]
			prefix := match[2]
			rest := match[3]

			for k := range counters {
				if len(k) > len(indent) {
					delete(counters, k)
				}
			}

			depth := len(indent) / 4

			if state, exists := counters[indent]; exists {
				state.count++
				lines[i] = fmt.Sprintf("%s%s. %s", indent, formatItem(state.count, state.style), rest)
			} else {
				parsedCount, parsedStyle := guessStyle(prefix)
				styleToUse := parsedStyle

				if prefix == "1" {
					styleToUse = depth % 5
				}

				counters[indent] = &listState{count: parsedCount, style: styleToUse}
				lines[i] = fmt.Sprintf("%s%s. %s", indent, formatItem(parsedCount, styleToUse), rest)
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
	// Re-use our robust heading parser so the file name always matches the status bar!
	title := getFirstHeading(content)

	if title != "Untitled" && title != "" {
		return sanitizeFilename(title)
	}

	// Fallback if the note has no headings at all
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

func updateMathBuffer(val string, lastKey string) (string, bool) {
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
					isNewEquation := strings.HasSuffix(strings.TrimSpace(line), "=")
					if isNewEquation && lastKey != "=" {
						continue
					}

					lines[i] = expected
					changed = true
				}
			}
		}
	}
	return strings.Join(lines, "\n"), changed
}

func stripANSI(str string) string {
	var buf strings.Builder
	inAnsi := false
	for _, r := range str {
		if r == '\x1b' {
			inAnsi = true
		}
		if inAnsi {
			if r == 'm' {
				inAnsi = false
			}
			continue
		}
		buf.WriteRune(r)
	}
	return buf.String()
}

// THE FIX: Fuzzy matches on a strict line-by-line basis.
// It will only match and highlight if the entire sequence exists within a single line!
func fuzzyMatch(query, target string) (bool, []int) {
	if query == "" {
		return true, nil
	}

	qRunes := []rune(strings.ToLower(query))
	tRunes := []rune(target) // Do NOT lower the entire string at once!

	var allIndices []int
	var currentLineIndices []int
	qIdx := 0
	matchedAnyLine := false

	for i, tRune := range tRunes {
		if tRune == '\n' {
			if qIdx == len(qRunes) {
				matchedAnyLine = true
				allIndices = append(allIndices, currentLineIndices...)
			}
			qIdx = 0
			currentLineIndices = currentLineIndices[:0]
			continue
		}

		if qIdx < len(qRunes) && qRunes[qIdx] == unicode.ToLower(tRune) {
			currentLineIndices = append(currentLineIndices, i)
			qIdx++
		}
	}

	if qIdx == len(qRunes) {
		matchedAnyLine = true
		allIndices = append(allIndices, currentLineIndices...)
	}

	return matchedAnyLine, allIndices
}

// THE FIX: Rune-safe literal and tag matchers that return exact indices for highlighting!
// THE FIX: Standard literal string matcher
func literalMatch(query, target string) (bool, []int) {
	if query == "" {
		return true, nil
	}

	qRunes := []rune(strings.ToLower(query))
	tRunes := []rune(strings.ToLower(target))

	var allIndices []int
	matchedAny := false

	for i := 0; i <= len(tRunes)-len(qRunes); i++ {
		match := true
		for j := 0; j < len(qRunes); j++ {
			if tRunes[i+j] != qRunes[j] {
				match = false
				break
			}
		}
		if match {
			matchedAny = true
			for j := 0; j < len(qRunes); j++ {
				allIndices = append(allIndices, i+j)
			}
			i += len(qRunes) - 1 // Skip ahead so we don't overlap matches
		}
	}
	return matchedAny, allIndices
}

func tagMatch(query, target string) (bool, []int) {
	if query == "" {
		return true, nil
	}

	fragments := strings.Fields(strings.ToLower(query))
	targetLower := strings.ToLower(target)

	// Just grab standard hashtags anywhere in the text!
	reTag := regexp.MustCompile(`(?i)#[a-zA-Z0-9_-]+`)
	matches := reTag.FindAllStringIndex(targetLower, -1)

	var allIndices []int

	for _, frag := range fragments {
		// Always auto-prepend hash
		if !strings.HasPrefix(frag, "#") {
			frag = "#" + frag
		}

		matchedThisFrag := false

		for _, matchIdx := range matches {
			start, end := matchIdx[0], matchIdx[1]
			tagText := targetLower[start:end]

			if tagText == "#kill" {
				continue
			}

			if strings.HasPrefix(tagText, frag) {
				matchedThisFrag = true
				for i := start; i < start+len(frag); i++ {
					allIndices = append(allIndices, i)
				}
			}
		}

		if !matchedThisFrag {
			return false, nil
		}
	}

	return true, allIndices
}

// Smart router that decides which search engine to use
func performSearch(query, target string, mode searchMode) (bool, []int) {
	switch mode {
	case modeLiteral:
		return literalMatch(query, target)
	case modeTag:
		return tagMatch(query, target)
	default:
		return fuzzyMatch(query, target)
	}
}

// THE FIX: A blazing fast in-memory filter that skips the hard drive!
func (m model) filterNotes() model {
	if m.searchQuery == "" {
		m.notes = m.allNotes
	} else {
		var filtered []Note
		for _, n := range m.allNotes {
			isMatch, _ := performSearch(m.searchQuery, stripANSI(n.Content), m.sMode)
			if isMatch {
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
		m.buffer = stringToBuffer(m.notes[m.index].Content)
		m.cursorRow, m.cursorCol = 0, 0
		m.virtualPhysCol = 0
	}
	return m
}

// reload only hits SQLite when we actually need fresh data
func (m model) reload() model {
	inTrash := m.state == stateTrash
	if m.state == stateSearch {
		inTrash = m.prevState == stateTrash
	}

	m.allNotes = loadNotes(m.db, inTrash)
	return m.filterNotes()
}

func (m *model) generateHelpText() string {
	termWidth := m.viewport.Width
	if termWidth == 0 {
		termWidth = 80
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal)).Bold(true)

	// 1. Document Properties
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

		// ADDED: Extract the tags string
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

	// 2. Cheatsheet
	cheatsheet := fmt.Sprintf(`
%s
Alt+Left/Right : Switch Notes
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
:now:          : e.g., 3:04PM 17 Apr 2026`,
		headerStyle.Render("=== GLOBAL NAVIGATION ==="),
		headerStyle.Render("=== EDITOR & MODES ==="),
		headerStyle.Render("=== MAGIC KEYWORDS ==="))

	// 3. Database Stats
	dbPropsText := fmt.Sprintf("%s\nActive Notes : %d\nTrash Bin    : %d", headerStyle.Render("=== DATABASE ==="), m.statActive, m.statTrash)

	// 4. Tags
	allTags := gatherAllTags(m.allNotes)
	tagsDisplay := strings.Join(allTags, "   ")
	if len(allTags) == 0 {
		tagsDisplay = "No tags found yet!"
	}
	wrappedTags := lipgloss.NewStyle().Width(termWidth - 10).Render("Your Tags:\n" + tagsDisplay)

	// Combine in correct order
	helpText := fmt.Sprintf("%s\n%s\n\n%s\n\n%s", docPropsText, cheatsheet, dbPropsText, wrappedTags)

	return lipgloss.NewStyle().Padding(2, 4).Render(helpText)
}

// --- Main App Logic ---

func initialModel(db *sql.DB, startTime time.Time) model {
	fi := textinput.New()
	fi.Prompt = "Save as: "

	si := textinput.New()
	si.Prompt = "Search: "

	pi := textinput.New()
	pi.Prompt = "Pipe through: "

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

	loadedTheme := loadTheme()

	notes := loadNotes(db, false)
	initialText := ""
	if len(notes) > 0 {
		initialText = notes[0].Content
	}
	buf := stringToBuffer(initialText)

	return model{
		db:             db,
		notes:          notes,
		allNotes:       notes,
		index:          0,
		buffer:         buf,
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
		theme:          loadedTheme,
		flashMsg:       fmt.Sprintf("Started in %.2fms", float64(time.Since(startTime).Nanoseconds())/1e6),
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, tickCmd(), cursorBlinkCmd())
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

	// --- ADDED: Accurately track if we are interacting with trashed notes ---
	inTrashContext := m.state == stateTrash ||
		(m.state == stateHelp && m.prevState == stateTrash) ||
		(m.state == stateSearch && m.prevState == stateTrash)

	// --- UPDATED: Use the context flag instead of checking m.state directly ---
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

	case tickMsg:
		cmds = append(cmds, tickCmd())

	// case cursorBlinkMsg:
	// 	// THE FIX: Stop toggling the blink if idle for 2 seconds.
	// 	// If the screen stops changing, Bubble Tea stops redrawing, and native mouse highlighting works!
	// 	if time.Since(m.lastActivity) < 5*time.Second {
	// 		m.cursorPhase = !m.cursorPhase
	// 	} else {
	// 		m.cursorPhase = true // Force the cursor to stay solid
	// 	}
	// 	cmds = append(cmds, cursorBlinkCmd())

	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 1
		return m, nil

	case tea.MouseMsg:
		// ADDED: Route mouse scrolling to the viewport if we are on the F1 screen
		if m.state == stateHelp {
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelUp {
				*m.viewportTop -= 4
				if *m.viewportTop < 0 {
					*m.viewportTop = 0
				}
				m.freeScroll = true // Detach camera
			} else if msg.Button == tea.MouseButtonWheelDown {
				*m.viewportTop += 4
				m.freeScroll = true // Detach camera
			} else if msg.Button == tea.MouseButtonLeft {
				m.freeScroll = false // Re-attach camera on click
				// 1. Ignore clicks on the status bar
				if msg.Y >= m.viewport.Height {
					return m, nil
				}

				// 2. Account for the line number gutter offset
				gutterWidth := 0
				if m.showLineNums {
					gutterWidth = 6
				}
				textWidth := m.viewport.Width - gutterWidth - 1
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

				// 3. Re-simulate the physical wrap to find the exact logical line
				for i, line := range m.buffer {
					starts, lengths := getLineMap(line, textWidth)

					if currentPhysLine+len(starts) > targetPhysLine {
						// We found the logical row!
						m.cursorRow = i
						segmentIdx := targetPhysLine - currentPhysLine

						targetCol := starts[segmentIdx] + visualX
						segmentEnd := starts[segmentIdx] + lengths[segmentIdx]

						// Clamp to the end of the physical wrap segment
						if targetCol > segmentEnd {
							targetCol = segmentEnd
						}
						// Clamp to the end of the actual line
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

				// 4. If they clicked the blank space below the document, jump to the end
				if !found && len(m.buffer) > 0 {
					m.cursorRow = len(m.buffer) - 1
					m.cursorCol = len(m.buffer[m.cursorRow])

					// Re-evaluate the virtual phys col for the absolute end of the file
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
		m.freeScroll = false // ADDED: Instantly re-attach camera when typing or using arrows

		if m.flashMsg != "" {
			m.flashMsg = ""
		}

		if m.state == stateSearch {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = m.prevState
				m.searchQuery = ""
				m.searchInput.SetValue("")
				m = m.reload() // Re-sync with the DB when exiting search
				clampCursor()
				m.virtualPhysCol = 0
				return m, nil
			case "enter":
				m.state = m.prevState
				return m, nil
			case "tab":
				m.sMode = (m.sMode + 1) % 3
				m = m.filterNotes() // Re-filter instantly using the new mode
				return m, nil
			}

			m.searchInput, cmd = m.searchInput.Update(msg)
			newQuery := strings.ToLower(strings.TrimSpace(m.searchInput.Value()))

			if newQuery != m.searchQuery {
				m.searchQuery = newQuery
				m.index = 0
				m = m.filterNotes() // <-- THE FIX: Instant in-memory filtering!
			}
			return m, cmd
		}

		if m.state == statePipe {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = stateTyping
				m.pipeInput.SetValue("")
				return m, nil
			case "enter":
				cmdStr := strings.TrimSpace(m.pipeInput.Value())
				if cmdStr != "" {
					m.undoStack = append(m.undoStack, textState{content: bufferToString(m.buffer), row: m.cursorRow, col: m.cursorCol})
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
					cmd := exec.Command("sh", "-c", cmdStr)
					cmd.Stdin = strings.NewReader(contentToPipe)
					out, err := cmd.CombinedOutput()

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
				return m, nil
			}
			m.pipeInput, cmd = m.pipeInput.Update(msg)
			return m, cmd
		}

		if m.state == stateHelp {
			switch msg.String() {
			case "esc", "ctrl+c", "enter", "f1":
				m.state = m.prevState // THE FIX: Restore the exact state we came from
				return m, nil
			case "up", "down", "pgup", "pgdown":
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
			return m, nil
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
				return m, nil
			case "f1":
				m.prevState = m.state
				m.state = stateHelp

				// We don't save here because you can't edit notes in the trash.
				// We just load the active count, and use our already-loaded trash for the trash count!
				m.statActive = len(loadNotes(m.db, false))
				m.statTrash = len(m.allNotes)

				m.viewport.SetContent(m.generateHelpText())
				m.viewport.GotoTop()
				return m, nil
			case "ctrl+f":
				m.prevState = m.state
				m.state = stateSearch
				m.searchInput.Focus()
				m.searchInput.SetValue(m.searchQuery)
				m.searchInput.CursorEnd()
				m = m.reload()
				return m, textinput.Blink
			case "alt+right":
				if m.index < len(m.notes)-1 {
					m.index++
					m.buffer = stringToBuffer(m.notes[m.index].Content)
					clampCursor()
					m.virtualPhysCol = 0
				}
			case "alt+left":
				if m.index > 0 {
					m.index--
					m.buffer = stringToBuffer(m.notes[m.index].Content)
					clampCursor()
					m.virtualPhysCol = 0
				}
			case "ctrl+r":
				if m.notes[m.index].ID > 0 {
					restoreNote(m.db, m.notes[m.index].ID)
					m.index = 0
					m = m.reload()
					clampCursor()
					m.virtualPhysCol = 0
				}
			case "up":
				if *m.viewportTop > 0 {
					*m.viewportTop--
				}
			case "down":
				*m.viewportTop++
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
					_ = os.WriteFile(path, []byte(bufferToString(m.buffer)), 0644)
				}
				m.state = stateTyping
				m.fileInput.SetValue("")
				return m, nil
			}
			m.fileInput, cmd = m.fileInput.Update(msg)
			return m, cmd
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

		// THE FIX: Anchor the pristine state of the document before the very first edit!
		// This ensures Ctrl+Z can always restore the file to exactly how it opened.
		if isModifying && len(m.undoStack) == 0 {
			m.undoStack = append(m.undoStack, textState{
				content: currentVal,
				row:     m.cursorRow,
				col:     m.cursorCol,
			})
		}

		// Standard Macro snapshot triggers
		if isMacro {
			if len(m.undoStack) == 0 || m.undoStack[len(m.undoStack)-1].content != currentVal {
				m.undoStack = append(m.undoStack, textState{
					content: currentVal,
					row:     m.cursorRow,
					col:     m.cursorCol,
				})
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

			// ADDED: If we have an active search filter, clear it and return to the full list
			if m.searchQuery != "" {
				m.searchQuery = ""
				m.searchInput.SetValue("")
				m.index = 0
				m = m.reload()
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				clampCursor()
				m.virtualPhysCol = 0
				// m.flashMsg = "Filter cleared"
				return m, nil
			}

			// Otherwise, quit the application
			return m, tea.Quit

		case "f1":
			if m.state == stateHelp {
				m.state = m.prevState // Or stateTyping depending on your setup
			} else {
				// ADDED: Save the live buffer to the database first!
				m.notes[m.index].Content = bufferToString(m.buffer)
				m.notes[m.index] = saveNote(m.db, m.notes[m.index])

				m.prevState = m.state
				m.state = stateHelp

				// ADDED: Reload the notes so the "Your Tags" aggregation catches the new tags
				m.allNotes = loadNotes(m.db, false)

				// Query the DB stats using our freshly loaded data
				m.statActive = len(m.allNotes)
				m.statTrash = len(loadNotes(m.db, true))

				// Generate the text and hand it to the viewport
				m.viewport.SetContent(m.generateHelpText())
				m.viewport.GotoTop()
			}
			return m, nil

		case "f2":
			m.showLineNums = !m.showLineNums
			return m, nil

		case "ctrl+f":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.prevState = m.state
			m.state = stateSearch
			m.searchInput.Focus()
			m.searchInput.SetValue(m.searchQuery)
			m.searchInput.CursorEnd()
			m = m.reload()
			return m, textinput.Blink

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
			return m, nil

		case "ctrl+z", "alt+z":
			if len(m.undoStack) > 0 {
				m.redoStack = append(m.redoStack, textState{content: bufferToString(m.buffer), row: m.cursorRow, col: m.cursorCol})
				last := m.undoStack[len(m.undoStack)-1]
				m.undoStack = m.undoStack[:len(m.undoStack)-1]
				m.buffer = stringToBuffer(last.content)
				m.cursorRow, m.cursorCol = last.row, last.col
				clampCursor()
			}
			return m, nil

		case "ctrl+y", "alt+y":
			if len(m.redoStack) > 0 {
				m.undoStack = append(m.undoStack, textState{content: bufferToString(m.buffer), row: m.cursorRow, col: m.cursorCol})
				next := m.redoStack[len(m.redoStack)-1]
				m.redoStack = m.redoStack[:len(m.redoStack)-1]
				m.buffer = stringToBuffer(next.content)
				m.cursorRow, m.cursorCol = next.row, next.col
				clampCursor()
			}
			return m, nil

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
			return m, nil

		case "ctrl+s":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.state = statePromptFile
			m.fileInput.SetValue(extractTitle(m.notes[m.index].Content) + ".md")
			m.fileInput.CursorEnd()
			m.fileInput.Focus()
			return m, textinput.Blink

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
			return m, nil

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
			return m, nil

		case "alt+c":
			copyToClipboard(bufferToString(m.buffer))
			m.flashMsg = "Copied!"
			return m, nil

		case "alt+x":
			m.state = statePipe
			m.pipeInput.SetValue("")
			m.pipeInput.Focus()
			return m, textinput.Blink

		case "alt+left":
			m.notes[m.index].Content = bufferToString(m.buffer)
			saveNote(m.db, m.notes[m.index])
			if m.index < len(m.notes)-1 {
				m.index++
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				m.cursorRow, m.cursorCol = 0, 0
				m.virtualPhysCol = 0
				m.undoStack, m.redoStack = nil, nil
			}
			return m, nil

		case "alt+right":
			m.notes[m.index].Content = bufferToString(m.buffer)
			saveNote(m.db, m.notes[m.index])
			if m.index > 0 {
				m.index--
				m.buffer = stringToBuffer(m.notes[m.index].Content)
				m.cursorRow, m.cursorCol = 0, 0
				m.virtualPhysCol = 0
				m.undoStack, m.redoStack = nil, nil
			}
			return m, nil

		// =====================================================
		// PERFECT PHYSICAL UP/DOWN MATH + VIRTUAL COLUMN
		// =====================================================
		case "up", "down", "pgup", "pgdown":
			gutterWidth := 0
			if m.showLineNums {
				gutterWidth = 6
			}
			textWidth := m.viewport.Width - gutterWidth - 1
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
			if m.cursorCol == 0 {
				// Jump to the end of the previous line
				if m.cursorRow > 0 {
					m.cursorRow--
					m.cursorCol = len(m.buffer[m.cursorRow])
				}
			} else {
				line := m.buffer[m.cursorRow]
				// Step back once to start the loop
				m.cursorCol--
				// 1. Skip any spaces immediately to our left
				for m.cursorCol > 0 && line[m.cursorCol-1] == ' ' {
					m.cursorCol--
				}
				// 2. Skip characters until we hit the next space (the start of the word)
				for m.cursorCol > 0 && line[m.cursorCol-1] != ' ' {
					m.cursorCol--
				}
			}

		case "ctrl+right":
			if m.cursorCol >= len(m.buffer[m.cursorRow]) {
				// Jump to the beginning of the next line
				if m.cursorRow < len(m.buffer)-1 {
					m.cursorRow++
					m.cursorCol = 0
				}
			} else {
				line := m.buffer[m.cursorRow]
				// 1. If we are on a space, skip forward through all spaces
				if line[m.cursorCol] == ' ' {
					for m.cursorCol < len(line) && line[m.cursorCol] == ' ' {
						m.cursorCol++
					}
				} else {
					// 2. Skip forward through the current word
					for m.cursorCol < len(line) && line[m.cursorCol] != ' ' {
						m.cursorCol++
					}
					// 3. Skip the trailing spaces to land on the first letter of the next word
					for m.cursorCol < len(line) && line[m.cursorCol] == ' ' {
						m.cursorCol++
					}
				}
			}
		case "home", "ctrl+a":
			gutterWidth := 0
			if m.showLineNums {
				gutterWidth = 6
			}
			textWidth := m.viewport.Width - gutterWidth - 1
			if textWidth <= 0 {
				textWidth = 80
			}

			starts, _ := getLineMap(m.buffer[m.cursorRow], textWidth)

			// Find which physical wrapped line the cursor is currently on
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

			// Find the first non-whitespace character of the logical line
			firstNonSpace := 0
			line := m.buffer[m.cursorRow]
			for firstNonSpace < len(line) && (line[firstNonSpace] == ' ' || line[firstNonSpace] == '\t') {
				firstNonSpace++
			}

			if physRow == 0 {
				// If we are on the very first physical line, toggle between first letter and absolute 0
				if m.cursorCol == firstNonSpace {
					m.cursorCol = 0
				} else {
					m.cursorCol = firstNonSpace
				}
			} else {
				// If we are on a wrapped physical line...
				if m.cursorCol == starts[physRow] {
					// ...and already at the start of the wrap, jump up to the first letter of the sentence
					m.cursorCol = firstNonSpace
				} else {
					// ...otherwise, just jump to the start of the current wrap
					m.cursorCol = starts[physRow]
				}
			}
		case "end", "ctrl+e":
			gutterWidth := 0
			if m.showLineNums {
				gutterWidth = 6
			}
			textWidth := m.viewport.Width - gutterWidth - 1
			if textWidth <= 0 {
				textWidth = 80
			}

			starts, lengths := getLineMap(m.buffer[m.cursorRow], textWidth)

			// Find which physical wrapped line the cursor is currently on
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
			// Clamp safety just in case trailing spaces were dropped by the wrap engine
			if physEnd > len(m.buffer[m.cursorRow]) {
				physEnd = len(m.buffer[m.cursorRow])
			}

			// If already at the end of the physical wrap, jump to the absolute logical end
			if m.cursorCol == physEnd {
				m.cursorCol = len(m.buffer[m.cursorRow])
			} else {
				// Otherwise, jump to the end of the current physical wrap
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
			// 1. Check if we are inside a markdown code block
			inCodeBlock := false
			for r := 0; r <= m.cursorRow; r++ {
				if strings.Count(string(m.buffer[r]), "```")%2 != 0 {
					inCodeBlock = !inCodeBlock
				}
			}

			// 2. If in a code block or Code Mode, just do a normal newline!
			if isCodeMode || inCodeBlock {
				rightPart := append([]rune{}, m.buffer[m.cursorRow][m.cursorCol:]...)
				m.buffer[m.cursorRow] = m.buffer[m.cursorRow][:m.cursorCol]
				m.buffer = append(m.buffer[:m.cursorRow+1], append([][]rune{rightPart}, m.buffer[m.cursorRow+1:]...)...)
				m.cursorRow++
				m.cursorCol = 0
				break
			}

			// 3. Normal smart-list enter logic
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
					prefixToInsert = leadingSpace + "1. " // autoRenumber fixes this instantly
				}
			}

			rightPart := append([]rune(prefixToInsert), m.buffer[m.cursorRow][m.cursorCol:]...)
			m.buffer[m.cursorRow] = m.buffer[m.cursorRow][:m.cursorCol]

			m.buffer = append(m.buffer[:m.cursorRow+1], append([][]rune{rightPart}, m.buffer[m.cursorRow+1:]...)...)
			m.cursorRow++

			lines := strings.Split(bufferToString(m.buffer), "\n")
			m.buffer = stringToBuffer(strings.Join(autoRenumber(lines), "\n"))

			// 4. THE FIX: Dynamically measure the new prefix length for perfect cursor placement
			renumberedLine := string(m.buffer[m.cursorRow])
			reNumPrefix := regexp.MustCompile(`^(\s*[a-zA-Z0-9]+\.\s)`)

			if match := reNumPrefix.FindString(renumberedLine); match != "" && reNumPrefix.MatchString(prefixToInsert) {
				m.cursorCol = len([]rune(match)) // Snaps exactly to the end of the new Roman numeral
			} else {
				m.cursorCol = len([]rune(prefixToInsert)) // Fallback for checkboxes and bullets
			}

		case " ":
			m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append([]rune{' '}, m.buffer[m.cursorRow][m.cursorCol:]...)...)
			m.cursorCol++

		case "tab":
			spaces := []rune("    ")
			m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append(spaces, m.buffer[m.cursorRow][m.cursorCol:]...)...)
			m.cursorCol += 4
			lines := strings.Split(bufferToString(m.buffer), "\n")
			m.buffer = stringToBuffer(strings.Join(autoRenumber(lines), "\n"))

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
				return m, nil
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

				// THE FIX: Intercept multi-line pastes!
				// If a terminal pastes a massive chunk of text containing newlines as a single event,
				// we must manually split it so it doesn't get crammed into a single row in m.buffer.
				if strings.Contains(strRunes, "\n") || strings.Contains(strRunes, "\r") {
					strRunes = strings.ReplaceAll(strRunes, "\r\n", "\n")
					strRunes = strings.ReplaceAll(strRunes, "\r", "\n")

					lines := strings.Split(strRunes, "\n")

					// 1. Keep what was to the left and right of the cursor
					leftPart := append([]rune{}, m.buffer[m.cursorRow][:m.cursorCol]...)
					rightPart := append([]rune{}, m.buffer[m.cursorRow][m.cursorCol:]...)

					// 2. The current row becomes the left part + the first line of the paste
					m.buffer[m.cursorRow] = append(leftPart, []rune(lines[0])...)

					// 3. Prepare the new intermediate rows to insert
					var newRows [][]rune
					for i := 1; i < len(lines)-1; i++ {
						newRows = append(newRows, []rune(lines[i]))
					}

					// 4. The last row gets the remainder of the paste + the original right part
					if len(lines) > 1 {
						lastRow := append([]rune(lines[len(lines)-1]), rightPart...)
						newRows = append(newRows, lastRow)
					}

					// 5. Splice the new rows safely into the 2D buffer
					if len(newRows) > 0 {
						newBuffer := make([][]rune, 0, len(m.buffer)+len(newRows))
						newBuffer = append(newBuffer, m.buffer[:m.cursorRow+1]...)
						newBuffer = append(newBuffer, newRows...)
						newBuffer = append(newBuffer, m.buffer[m.cursorRow+1:]...)
						m.buffer = newBuffer
					}

					// 6. Update the cursor position to the end of the paste
					m.cursorRow += len(lines) - 1
					if len(lines) > 1 {
						m.cursorCol = len([]rune(lines[len(lines)-1]))
					} else {
						m.cursorCol += len([]rune(lines[0]))
					}

				} else {
					// Normal single-line typing
					m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append(msg.Runes, m.buffer[m.cursorRow][m.cursorCol:]...)...)
					m.cursorCol += len(msg.Runes)
				}
			}
		}

		newValExp, expanded := expandKeywords(bufferToString(m.buffer), isCodeMode, msg.String())
		if expanded {
			diff := len([]rune(newValExp)) - len([]rune(bufferToString(m.buffer)))
			m.undoStack = append(m.undoStack, textState{content: bufferToString(m.buffer), row: m.cursorRow, col: m.cursorCol})
			m.redoStack = nil
			m.buffer = stringToBuffer(newValExp)
			m.cursorCol += diff
		}

		if isMathMode {
			newValMath, changed := updateMathBuffer(bufferToString(m.buffer), msg.String())
			if changed {
				m.undoStack = append(m.undoStack, textState{content: bufferToString(m.buffer), row: m.cursorRow, col: m.cursorCol})
				m.redoStack = nil
				m.buffer = stringToBuffer(newValMath)
			}
		}

		if len(m.buffer) > 0 && len(m.buffer[len(m.buffer)-1]) > 0 {
			m.buffer = append(m.buffer, []rune{})
		}

		clampCursor()

		// Save the target physical column on non-vertical movements
		kStr := msg.String()
		if kStr != "up" && kStr != "down" && kStr != "pgup" && kStr != "pgdown" {
			gutterWidth := 0
			if m.showLineNums {
				gutterWidth = 6
			}
			textWidth := m.viewport.Width - gutterWidth - 1
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

	return m, tea.Batch(cmds...)
}

func gatherAllTags(notes []Note) []string {
	tagCounts := make(map[string]int)
	for _, n := range notes {
		for _, tag := range n.Tags {
			tagCounts[tag]++
		}
	}

	var uniqueTags []string
	for tag, count := range tagCounts {
		uniqueTags = append(uniqueTags, fmt.Sprintf("%s (%d)", tag, count))
	}

	sort.Strings(uniqueTags)
	return uniqueTags
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

	if m.state == stateTrash {
		modeStr = "TRASH "
		modeColor = accentTrash
	} else if m.state == stateHelp {
		modeStr = "HELP "
		modeColor = accentNormal
	} else if m.state == stateSearch {
		modeStr = "FIND "
		modeColor = accentMath
	}

	modeBlock := lipgloss.NewStyle().Background(modeColor).Foreground(textDark).Bold(true).Padding(0, 1).Render(modeStr)

	searchTag := ""
	if m.searchQuery != "" {
		searchTag = fmt.Sprintf(" [Find: %s] ", m.searchQuery)
	}

	// THE FIX: Check if the only note in the array is our "No Matches" dummy note
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

	if m.state == stateHelp {
		return m.viewport.View()
	}

	if m.state == stateSearch {
		modeName := "Fuzzy"
		if m.sMode == modeLiteral {
			modeName = "Literal"
		} else if m.sMode == modeTag {
			modeName = "Tag"
		}

		p := lipgloss.NewStyle().Foreground(accentNormal).Bold(true).Render(
			fmt.Sprintf("Find in Notes [%s Mode] (Tab to switch, Esc to clear)", modeName),
		) + "\n\n" + m.searchInput.View()

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

	if m.state == statePipe {
		p := lipgloss.NewStyle().Foreground(accentNormal).Bold(true).Render("Filter Note through Shell Command") + "\n\n" + m.pipeInput.View()
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentNormal).Padding(1, 3).Render(p)
		centeredBox := lipgloss.Place(termWidth, m.viewport.Height, lipgloss.Center, lipgloss.Center, box)
		return lipgloss.JoinVertical(lipgloss.Left, centeredBox, statusBar)
	}

	return fmt.Sprintf("%s\n%s", m.renderEditorView(), statusBar)
}

func (m *model) renderEditorView() string {
	// 1. Get raw string WITHOUT cursor tokens!
	rawText := bufferToString(m.buffer)

	isCodeMode := strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawText)), "code:")

	// Helper to tokenize markdown formatting ONLY outside of multiline code blocks
	tokenizeMarkdown := func(text string, re *regexp.Regexp, baseRune rune, tokenMap map[string]string) string {
		chunks := strings.Split(text, "```")
		for i := range chunks {
			if i%2 == 1 { // Inside a code block! Skip it.
				continue
			}
			matches := re.FindAllString(chunks[i], -1)
			for _, match := range matches {
				token := string(rune(baseRune + rune(len(tokenMap))))
				tokenMap[token] = match
				chunks[i] = strings.Replace(chunks[i], match, token, 1)
			}
		}
		return strings.Join(chunks, "```")
	}

	strikeMap := make(map[string]string)
	h3Map := make(map[string]string)
	h2Map := make(map[string]string)
	h1Map := make(map[string]string)

	// Only apply Markdown Headings if we aren't globally in Code Mode
	if !isCodeMode {
		// THE FIX 1: Use [ \t] instead of \s so we don't accidentally consume newlines (\n)
		// and merge empty lines together!
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`~~(.*?)~~`), 0xF0000, strikeMap)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?m)^[ \t]*###[ \t]+.*`), 0xF2000, h3Map)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?m)^[ \t]*##[ \t]+.*`), 0xF3000, h2Map)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?m)^[ \t]*#[ \t]+.*`), 0xF4000, h1Map)
	}

	// THE FIX 2: These must be the ONLY other regex extractions! If you have older
	// reH1 or reStrike parsers below this point, they will break the code-block protection!
	reKeyword := regexp.MustCompile(`(?i)#(idea|todo)\b`)
	keywordMatches := reKeyword.FindAllString(rawText, -1)
	keywordMap := make(map[string]string)
	for i, match := range keywordMatches {
		token := string(rune(0xE500 + i))
		keywordMap[token] = match
		rawText = strings.Replace(rawText, match, token, 1)
	}

	reKill := regexp.MustCompile(`(?i)#kill\s+[0-9.]+\s*[a-zA-Z]+`)
	killMatches := reKill.FindAllString(rawText, -1)
	killMap := make(map[string]string)
	for i, match := range killMatches {
		token := string(rune(0xE100 + i))
		killMap[token] = match
		rawText = strings.Replace(rawText, match, token, 1)
	}

	reTag := regexp.MustCompile(`#[a-zA-Z0-9_-]+`)
	tagMatches := reTag.FindAllString(rawText, -1)
	tagMap := make(map[string]string)
	for i, match := range tagMatches {
		token := string(rune(0xE200 + i))
		tagMap[token] = match
		rawText = strings.Replace(rawText, match, token, 1)
	}

	// 3. Highlight the pure text via Chroma
	highlighted := highlightText(rawText, m.theme)

	// 4. Custom Line Styles (comments, blockquotes, lists)
	rawLines := strings.Split(rawText, "\n")
	hlLines := strings.Split(highlighted, "\n")

	if len(hlLines) > len(rawLines) {
		hlLines = hlLines[:len(rawLines)]
	} else if len(hlLines) < len(rawLines) {
		for len(hlLines) < len(rawLines) {
			hlLines = append(hlLines, "")
		}
	}

	commentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.Comment)).Italic(true)
	blockquoteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal))
	listStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal)).Bold(true)

	reList := regexp.MustCompile(`^([\s]*[a-zA-Z0-9]+\.[\s]+)`)

	inCodeBlock := false
	for i, rawLine := range rawLines {
		if strings.Count(rawLine, "```")%2 != 0 {
			inCodeBlock = !inCodeBlock
		}

		treatAsCode := inCodeBlock || isCodeMode
		trimmed := strings.TrimSpace(rawLine)

		if strings.HasPrefix(trimmed, "//") {
			hlLines[i] = commentStyle.Render(rawLine)
		} else if treatAsCode && strings.HasPrefix(trimmed, "#") {
			hlLines[i] = commentStyle.Render(rawLine)
		} else if !treatAsCode && strings.HasPrefix(trimmed, ">") {
			hlLines[i] = blockquoteStyle.Render(rawLine)
		} else if !treatAsCode && reList.MatchString(rawLine) {
			match := reList.FindStringSubmatch(rawLine)
			prefix := match[1]
			hlLines[i] = strings.Replace(hlLines[i], prefix, listStyle.Render(prefix), 1)
		}
	}
	highlighted = strings.Join(hlLines, "\n")

	// 5. Restore Tokens
	strikeStyle := lipgloss.NewStyle().Strikethrough(true)
	h3Style := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal)).Bold(true)
	h2Style := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal)).Bold(true).Underline(true)
	h1Style := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.BgDark)).Background(lipgloss.Color(m.theme.AccentNormal)).Bold(true)
	killStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.BgDark)).Background(lipgloss.Color(m.theme.AccentTrash)).Bold(true)
	tagStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(m.theme.AccentNormal)).Italic(true)
	keywordStyle := lipgloss.NewStyle().Background(lipgloss.Color(m.theme.AccentMath)).Foreground(lipgloss.Color(m.theme.BgDark)).Bold(true)

	for token, match := range strikeMap {
		highlighted = strings.Replace(highlighted, token, strikeStyle.Render(match), 1)
	}
	for token, match := range h3Map {
		highlighted = strings.Replace(highlighted, token, h3Style.Render(match), 1)
	}
	for token, match := range h2Map {
		highlighted = strings.Replace(highlighted, token, h2Style.Render(match), 1)
	}
	for token, match := range h1Map {
		highlighted = strings.Replace(highlighted, token, h1Style.Render(match), 1)
	}
	for token, match := range killMap {
		highlighted = strings.Replace(highlighted, token, killStyle.Render(match), 1)
	}
	for token, match := range keywordMap {
		highlighted = strings.Replace(highlighted, token, keywordStyle.Render(match), 1)
	}
	for token, match := range tagMap {
		highlighted = strings.Replace(highlighted, token, tagStyle.Render(match), 1)
	}

	// 6. Build Physical Lines and visually inject cursor!
	charUnderCursor := " "
	if m.cursorCol < len(m.buffer[m.cursorRow]) {
		charUnderCursor = string(m.buffer[m.cursorRow][m.cursorCol])
	}

	cursorStyle := lipgloss.NewStyle().Reverse(true)
	var visibleCursor string

	visibleCursor = cursorStyle.Render(charUnderCursor)
	// if m.cursorPhase || time.Since(m.lastActivity) < time.Millisecond*530 {
	// } else {
	// 	visibleCursor = charUnderCursor
	// }

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
	wrapToken := string(rune(0xF9000))

	gutterWidth := 0
	if m.showLineNums {
		gutterWidth = 6
	}
	textWidth := m.viewport.Width - gutterWidth - 1
	if textWidth <= 0 {
		textWidth = 80
	}

	for i, logicalLineHl := range logicalLinesHl {
		starts, lengths := getLineMap(m.buffer[i], textWidth)
		isCursorLine := (i == m.cursorRow)

		physSegments := buildPhysicalLines(logicalLineHl, m.cursorCol, isCursorLine, starts, lengths, visibleCursor)
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

	// 1. Only force the camera to the cursor if we aren't free-scrolling
	if !m.freeScroll {
		if cursorPhysicalRow < *m.viewportTop {
			*m.viewportTop = cursorPhysicalRow
		} else if cursorPhysicalRow >= *m.viewportTop+m.viewport.Height {
			*m.viewportTop = cursorPhysicalRow - m.viewport.Height + 1
		}
	}

	// 2. Prevent the scroll wheel from scrolling infinitely past the bottom
	maxTop := len(physicalLines) - m.viewport.Height + 1
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
	visibleText := strings.Join(visibleLines, "\n")
	visibleText = strings.ReplaceAll(visibleText, wrapToken, "")

	return lipgloss.NewStyle().
		Width(m.viewport.Width).
		Height(m.viewport.Height).
		Render(visibleText)
}

func highlightText(content string, t Theme) string {
	if content == "" {
		return ""
	}

	var lexer chroma.Lexer
	lines := strings.SplitN(content, "\n", 2)
	firstLineLower := strings.ToLower(strings.TrimSpace(lines[0]))

	if strings.HasPrefix(firstLineLower, "code:") {
		lang := strings.TrimSpace(firstLineLower[5:])
		if lang == "" {
			lexer = lexers.Get("markdown")
		} else {
			lexer = lexers.Get(lang)
			if lexer == nil {
				lexer = lexers.Analyse(content)
			}
		}
	} else {
		lexer = lexers.Get("markdown")
	}

	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	formatter := formatters.Get("terminal16m")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	// THE FIX: Dynamically generate the Chroma style dictionary from your JSON config!
	dynamicStyle := chroma.MustNewStyle("dynamicTheme", chroma.StyleEntries{
		chroma.Text:                  t.TextLight,
		chroma.Error:                 t.AccentTrash,
		chroma.Comment:               t.Comment,
		chroma.Keyword:               t.AccentNormal,
		chroma.KeywordNamespace:      t.AccentNormal,
		chroma.KeywordType:           t.AccentNormal,
		chroma.Operator:              t.AccentMath, // Reused AccentMath for operators
		chroma.Punctuation:           t.TextLight,
		chroma.Name:                  t.TextLight,
		chroma.NameAttribute:         t.AccentNormal,
		chroma.NameClass:             t.AccentMath,
		chroma.NameConstant:          t.AccentMath,
		chroma.NameDecorator:         t.AccentMath,
		chroma.NameException:         t.AccentTrash,
		chroma.NameFunction:          t.AccentNormal,
		chroma.NameOther:             t.TextLight,
		chroma.NameTag:               t.AccentNormal,
		chroma.LiteralNumber:         t.AccentMath,
		chroma.LiteralString:         t.AccentCode,
		chroma.LiteralStringEscape:   t.AccentMath,
		chroma.GenericDeleted:        t.AccentTrash,
		chroma.GenericInserted:       t.AccentCode,
		chroma.Background:            "bg:" + t.BgDark,
		chroma.GenericEmph:           t.AccentCode + " italic",
		chroma.GenericStrong:         t.AccentTrash + " bold",
		chroma.GenericPrompt:         t.AccentNormal,
		chroma.GenericTraceback:      t.AccentNormal,
		chroma.GenericHeading:        "bg:" + t.AccentNormal + " #FFFFFF bold",
		chroma.GenericSubheading:     "bg:" + t.AccentNormal + " #FFFFFF bold",
		chroma.LiteralStringBacktick: "bg:" + t.BgMed + " " + t.AccentTrash,
	})

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}

	var buf bytes.Buffer
	err = formatter.Format(&buf, dynamicStyle, iterator)
	if err != nil {
		return content
	}

	return buf.String()
}

func main() {
	startTime := time.Now()

	noteFlag := flag.String("n", "", "Create a note directly from the command line")
	quietFlag := flag.Bool("q", false, "Quiet mode: save note and exit without opening the UI")
	titleFlag := flag.String("t", "", "Title/Heading for the note (creates new or appends to existing)")
	lastFlag := flag.Bool("l", false, "Append to the most recently used note")
	cmdFlag := flag.String("c", "", "Execute a shell command, capture output, and use command as title")

	flag.Parse()

	var pipedText string
	var tuiInput *os.File = os.Stdin

	if *cmdFlag != "" {
		cmd := exec.Command("sh", "-c", *cmdFlag)
		out, err := cmd.CombinedOutput()

		pipedText = strings.TrimSpace(string(out))
		if err != nil {
			pipedText = fmt.Sprintf("Error executing command: %v\n\n%s", err, pipedText)
		}
		*titleFlag = *cmdFlag

	} else {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			bytes, _ := io.ReadAll(os.Stdin)
			pipedText = strings.TrimSpace(string(bytes))

			tty, err := os.Open("/dev/tty")
			if err == nil {
				tuiInput = tty
			}
		}
	}

	db := initDB()
	defer db.Close()

	initialContent := ""
	if *noteFlag != "" {
		initialContent = *noteFlag
	} else if pipedText != "" {
		initialContent = pipedText
	}

	if initialContent != "" {
		var targetID int
		var existingContent string

		if *lastFlag {
			err := db.QueryRow("SELECT id, content FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC LIMIT 1").Scan(&targetID, &existingContent)
			if err != nil {
				targetID = 0
			}
		} else if *titleFlag != "" {
			rows, _ := db.Query("SELECT id, content FROM notes WHERE deleted_at IS NULL ORDER BY updated_at DESC")
			for rows.Next() {
				var id int
				var content string
				rows.Scan(&id, &content)

				if strings.EqualFold(extractTitle(content), sanitizeFilename(*titleFlag)) {
					targetID = id
					existingContent = content
					break
				}
			}
			rows.Close()

			if targetID == 0 {
				initialContent = "# " + *titleFlag + "\n\n" + initialContent
			}
		}

		if targetID > 0 {
			newContent := strings.TrimRight(existingContent, " \n") + "\n" + initialContent
			_, err := db.Exec("UPDATE notes SET content = ?, updated_at = datetime('now', '+1 second') WHERE id = ?", newContent, targetID)
			if err != nil {
				log.Fatalf("Failed to append to CLI note: %v", err)
			}
		} else {
			_, err := db.Exec("INSERT INTO notes (content, updated_at) VALUES (?, datetime('now', '+1 second'))", initialContent)
			if err != nil {
				log.Fatalf("Failed to save CLI note: %v", err)
			}
		}

		if *quietFlag {
			fmt.Println("✓ Note saved to Quicknote.")
			return
		}
	}

	p := tea.NewProgram(
		initialModel(db, startTime),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // ADDED: Tells the terminal to track clicks
		tea.WithInput(tuiInput),
	)
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
