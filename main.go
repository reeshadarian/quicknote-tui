package main

import (
	"bytes"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Knetic/govaluate"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
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
	cursorPhase  bool
	lastActivity time.Time
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

// THE FIX: Parses the ORIGINAL segment to prevent exponential memory leaks!
func carryANSI(segments []string) []string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	var activeANSI []string

	for i := range segments {
		// Capture the raw text BEFORE we inject massive amounts of prepended colors
		originalSegment := segments[i]
		
		// Apply previously active ANSI codes to the visual string
		prefix := strings.Join(activeANSI, "")
		segments[i] = prefix + originalSegment

		// Recalculate active ANSI codes for the NEXT segment using ONLY the original text
		matches := re.FindAllString(originalSegment, -1)
		for _, match := range matches {
			if match == "\x1b[0m" {
				activeANSI = nil // Clear on reset
			} else {
				activeANSI = append(activeANSI, match)
			}
		}

		// Close off the current visual segment so colors don't bleed into the gutter
		if len(activeANSI) > 0 && !strings.HasSuffix(segments[i], "\x1b[0m") {
			segments[i] = segments[i] + "\x1b[0m"
		}
	}
	return segments
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
	"e":  math.E, "E": math.E,
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
		m.buffer = stringToBuffer(m.notes[m.index].Content)
		m.cursorRow, m.cursorCol = 0, 0
		m.virtualPhysCol = 0
	}
	return m
}

func (m *model) generateHelpText() string {
	accentNormal := lipgloss.Color("#89B4FA")
	headerStyle := lipgloss.NewStyle().Foreground(accentNormal).Bold(true)

	currentVal := bufferToString(m.buffer)
	words := len(strings.Fields(currentVal))
	chars := len(currentVal)

	val := strings.ToLower(strings.TrimSpace(currentVal))
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
	fi := textinput.New()
	fi.Prompt = "Save as: "

	si := textinput.New()
	si.Prompt = "Search: "

	pi := textinput.New()
	pi.Prompt = "Pipe through: "

	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().Padding(0, 1)

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

	if len(m.notes) > 0 && m.state != stateTrash {
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

	case cursorBlinkMsg:
		m.cursorPhase = !m.cursorPhase
		cmds = append(cmds, cursorBlinkCmd())

	case tea.WindowSizeMsg:
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 1
		return m, nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelUp && *m.viewportTop > 0 {
				*m.viewportTop--
			} else if msg.Button == tea.MouseButtonWheelDown {
				*m.viewportTop++
			}
		}
		return m, nil

	case tea.KeyMsg:
		m.lastActivity = time.Now()
		m.cursorPhase = true

		if m.flashMsg != "" {
			m.flashMsg = ""
		}

		if m.state == stateSearch {
			switch msg.String() {
			case "esc", "ctrl+c":
				m.state = m.prevState
				m.searchQuery = ""
				m.searchInput.SetValue("")
				m = m.reload()
				clampCursor()
				m.virtualPhysCol = 0
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
				m.state = stateTyping
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
		switch msg.String() {
		case "space", "enter", "alt+enter", "ctrl+o", "tab", "shift+tab", "ctrl+x", "ctrl+b", "alt+i", "ctrl+k", "=", "`", "ctrl+s":
			isMacro = true
		}

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
			return m, tea.Quit

		case "f1":
			m.notes[m.index].Content = bufferToString(m.buffer)
			m.notes[m.index] = saveNote(m.db, m.notes[m.index])
			m.viewport.SetContent(m.generateHelpText())
			m.viewport.GotoTop()
			m.state = stateHelp
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
			newNote := Note{ID: 0, Content: "", UpdatedAt: time.Now().Format("2006-01-02 15:04:05")}
			m.allNotes = append([]Note{newNote}, m.allNotes...)
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
		case "home", "ctrl+a":
			m.cursorCol = 0
		case "end", "ctrl+e":
			m.cursorCol = len(m.buffer[m.cursorRow])

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
			if isCodeMode {
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
					prefixToInsert = leadingSpace + "1. " // autoRenumber fixes this instantly
				}
			}

			rightPart := append([]rune(prefixToInsert), m.buffer[m.cursorRow][m.cursorCol:]...)
			m.buffer[m.cursorRow] = m.buffer[m.cursorRow][:m.cursorCol]

			m.buffer = append(m.buffer[:m.cursorRow+1], append([][]rune{rightPart}, m.buffer[m.cursorRow+1:]...)...)
			m.cursorRow++

			lines := strings.Split(bufferToString(m.buffer), "\n")
			m.buffer = stringToBuffer(strings.Join(autoRenumber(lines), "\n"))
			m.cursorCol = len([]rune(prefixToInsert))

		case "space":
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
			if strings.Contains(lineStr, "- [ ]") {
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
				runes := msg.Runes
				m.buffer[m.cursorRow] = append(m.buffer[m.cursorRow][:m.cursorCol], append(runes, m.buffer[m.cursorRow][m.cursorCol:]...)...)
				m.cursorCol += len(runes)
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

		// Reset virtual column on horizontal movements
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

		if m.cursorRow < m.viewport.YOffset {
			m.viewport.SetYOffset(m.cursorRow)
		} else if m.cursorRow >= m.viewport.YOffset+m.viewport.Height {
			m.viewport.SetYOffset(m.cursorRow - m.viewport.Height + 1)
		}

		return m, tea.Batch(cmds...)
	}

	return m, nil
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

	noteInfoStr := fmt.Sprintf(" Note %d/%d%s ", m.index+1, len(m.notes), searchTag)
	if m.notes[m.index].IsGhost {
		noteInfoStr = lipgloss.NewStyle().Background(lipgloss.Color(bgMed)).Foreground(textLight).Bold(true).Padding(0, 1).Render("󰊠")
	}
	if m.flashMsg != "" {
		noteInfoStr = " ★ " + m.flashMsg + " "
	}

	noteBlock := lipgloss.NewStyle().Background(bgMed).Foreground(textLight).Padding(0, 1).Render(noteInfoStr)
	statBlock := lipgloss.NewStyle().Background(bgDark).Foreground(textLight).Padding(0, 1).Render(fmt.Sprintf("%dW %dC", words, chars))

	headingBlock := ""
	heading := getFirstHeading(currentText)
	if heading != "Untitled" && heading != "" {
		headingBlock = lipgloss.NewStyle().Background(bgDark).Foreground(accentNormal).Padding(0, 1).Render("│ " + heading)
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

	if m.state == statePipe {
		p := lipgloss.NewStyle().Foreground(accentNormal).Bold(true).Render("Filter Note through Shell Command") + "\n\n" + m.pipeInput.View()
		box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accentNormal).Padding(1, 3).Render(p)
		centeredBox := lipgloss.Place(termWidth, m.viewport.Height, lipgloss.Center, lipgloss.Center, box)
		return lipgloss.JoinVertical(lipgloss.Left, centeredBox, statusBar)
	}

	return fmt.Sprintf("%s\n%s", m.renderEditorView(), statusBar)
}

func (m *model) renderEditorView() string {
	var builder strings.Builder
	cursorToken := string(rune(0xE000))
	wrapToken := string(rune(0xE002))
	charUnderCursor := " "

	gutterWidth := 0
	if m.showLineNums {
		gutterWidth = 6
	}

	textWidth := m.viewport.Width - gutterWidth - 1
	if textWidth <= 0 {
		textWidth = 80
	}

	var physicalLineIdx int
	cursorPhysicalRow := 0

	for r, line := range m.buffer {
		starts, lengths := getLineMap(line, textWidth)
		isCursorLine := (r == m.cursorRow)

		for p, start := range starts {
			length := lengths[p]
			end := start + length
			segment := line[start:end]

			if isCursorLine {
				isCursorInThisSegment := false
				if p < len(starts)-1 {
					if m.cursorCol >= start && m.cursorCol < starts[p+1] {
						isCursorInThisSegment = true
					}
				} else {
					if m.cursorCol >= start && m.cursorCol <= len(line) {
						isCursorInThisSegment = true
					}
				}

				if isCursorInThisSegment {
					cursorPhysicalRow = physicalLineIdx
					segmentCol := m.cursorCol - start

					if segmentCol < len(segment) {
						charUnderCursor = string(segment[segmentCol])
						builder.WriteString(string(segment[:segmentCol]))
						builder.WriteString(cursorToken)
						builder.WriteString(string(segment[segmentCol+1:]))
					} else {
						builder.WriteString(string(segment))
						builder.WriteString(cursorToken)
					}
				} else {
					builder.WriteString(string(segment))
				}
			} else {
				builder.WriteString(string(segment))
			}

			physicalLineIdx++

			if p < len(starts)-1 {
				builder.WriteString(wrapToken)
			}
		}

		if r < len(m.buffer)-1 {
			builder.WriteRune('\n')
		}
	}

	rawWithCursorToken := builder.String()

	reStrike := regexp.MustCompile(`~~(.*?)~~`)
	strikeMatches := reStrike.FindAllString(rawWithCursorToken, -1)
	strikeMap := make(map[string]string)
	for i, match := range strikeMatches {
		token := string(rune(0xE001 + i))
		strikeMap[token] = match
		rawWithCursorToken = strings.Replace(rawWithCursorToken, match, token, 1)
	}

	reKill := regexp.MustCompile(`(?i)#kill\s+[0-9.]+\s*[a-zA-Z]+`)
	killMatches := reKill.FindAllString(rawWithCursorToken, -1)
	killMap := make(map[string]string)
	for i, match := range killMatches {
		token := string(rune(0xE0A0 + i))
		killMap[token] = match
		rawWithCursorToken = strings.Replace(rawWithCursorToken, match, token, 1)
	}

	reH3 := regexp.MustCompile(`(?m)^[\s` + cursorToken + `]*###\s+.*`)
	h3Matches := reH3.FindAllString(rawWithCursorToken, -1)
	h3Map := make(map[string]string)
	for i, match := range h3Matches {
		token := string(rune(0xE100 + i))
		h3Map[token] = match
		rawWithCursorToken = strings.Replace(rawWithCursorToken, match, token, 1)
	}

	reH2 := regexp.MustCompile(`(?m)^[\s` + cursorToken + `]*##\s+.*`)
	h2Matches := reH2.FindAllString(rawWithCursorToken, -1)
	h2Map := make(map[string]string)
	for i, match := range h2Matches {
		token := string(rune(0xE200 + i))
		h2Map[token] = match
		rawWithCursorToken = strings.Replace(rawWithCursorToken, match, token, 1)
	}

	reH1 := regexp.MustCompile(`(?m)^[\s` + cursorToken + `]*#\s+.*`)
	h1Matches := reH1.FindAllString(rawWithCursorToken, -1)
	h1Map := make(map[string]string)
	for i, match := range h1Matches {
		token := string(rune(0xE300 + i))
		h1Map[token] = match
		rawWithCursorToken = strings.Replace(rawWithCursorToken, match, token, 1)
	}

	reTag := regexp.MustCompile(`#[a-zA-Z0-9_-]+`)
	tagMatches := reTag.FindAllString(rawWithCursorToken, -1)
	tagMap := make(map[string]string)
	for i, match := range tagMatches {
		token := string(rune(0xE400 + i))
		tagMap[token] = match
		rawWithCursorToken = strings.Replace(rawWithCursorToken, match, token, 1)
	}

	highlighted := highlightText(rawWithCursorToken)

	rawLines := strings.Split(rawWithCursorToken, "\n")
	hlLines := strings.Split(highlighted, "\n")

	commentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Italic(true)
	blockquoteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA"))
	listStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#89B4FA")).Bold(true)

	reList := regexp.MustCompile(`^([\s\x{E000}]*[a-zA-Z0-9\x{E000}]+\.[\s\x{E000}]+)`)

	for i, rawLine := range rawLines {
		cleanLine := strings.ReplaceAll(rawLine, string(rune(0xE000)), "")
		cleanLine = strings.ReplaceAll(cleanLine, wrapToken, "")
		trimmed := strings.TrimSpace(cleanLine)

		if strings.HasPrefix(trimmed, "//") {
			hlLines[i] = commentStyle.Render(rawLine)
		} else if strings.HasPrefix(trimmed, ">") {
			hlLines[i] = blockquoteStyle.Render(rawLine)
		} else if match := reList.FindStringSubmatch(rawLine); match != nil {
			prefix := match[1]
			hlLines[i] = strings.Replace(hlLines[i], prefix, listStyle.Render(prefix), 1)
		}
	}
	highlighted = strings.Join(hlLines, "\n")

	strikeStyle := lipgloss.NewStyle().Strikethrough(true).Foreground(lipgloss.Color("#6C7086"))
	for token, match := range strikeMap {
		highlighted = strings.Replace(highlighted, token, strikeStyle.Render(match), 1)
	}
	killStyle := lipgloss.NewStyle().Background(lipgloss.Color("#F38BA8")).Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	for token, match := range killMap {
		highlighted = strings.Replace(highlighted, token, killStyle.Render(match), 1)
	}
	h1Style := lipgloss.NewStyle().Background(lipgloss.Color("#89B4FA")).Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
	for token, match := range h1Map {
		highlighted = strings.Replace(highlighted, token, h1Style.Render(match), 1)
	}
	h2Style := lipgloss.NewStyle().Foreground(lipgloss.Color("#A6E3A1")).Bold(true)
	for token, match := range h2Map {
		highlighted = strings.Replace(highlighted, token, h2Style.Render(match), 1)
	}
	h3Style := lipgloss.NewStyle().Foreground(lipgloss.Color("#FAB387")).Bold(true)
	for token, match := range h3Map {
		highlighted = strings.Replace(highlighted, token, h3Style.Render(match), 1)
	}
	tagStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#CBA6F7")).Italic(true)
	for token, match := range tagMap {
		highlighted = strings.Replace(highlighted, token, tagStyle.Render(match), 1)
	}

	cursorStyle := lipgloss.NewStyle().Reverse(true)
	var visibleCursor string

	if m.cursorPhase || time.Since(m.lastActivity) < time.Millisecond*530 {
		visibleCursor = cursorStyle.Render(charUnderCursor)
	} else {
		visibleCursor = charUnderCursor
	}

	highlighted = strings.Replace(highlighted, cursorToken, visibleCursor, 1)

	logicalLinesHl := strings.Split(highlighted, "\n")
	var physicalLines []string

	for i, logicalLineHl := range logicalLinesHl {
		physSegments := strings.Split(logicalLineHl, wrapToken)
		
		physSegments = carryANSI(physSegments)

		for j, pLine := range physSegments {
			if m.showLineNums {
				if j == 0 {
					numStr := fmt.Sprintf("%3d │ ", i+1)
					gutter := lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Render(numStr)
					physicalLines = append(physicalLines, gutter+pLine)
				} else {
					emptyGutter := lipgloss.NewStyle().Foreground(lipgloss.Color("#6C7086")).Render("    │ ")
					physicalLines = append(physicalLines, emptyGutter+pLine)
				}
			} else {
				physicalLines = append(physicalLines, pLine)
			}
		}
	}

	if cursorPhysicalRow < *m.viewportTop {
		*m.viewportTop = cursorPhysicalRow
	} else if cursorPhysicalRow >= *m.viewportTop+m.viewport.Height {
		*m.viewportTop = cursorPhysicalRow - m.viewport.Height + 1
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

	return lipgloss.NewStyle().
		Width(m.viewport.Width).
		Height(m.viewport.Height).
		Render(visibleText)
}

// --- Syntax Highlighting Styles ---

var quicknoteStyle = styles.Register(chroma.MustNewStyle("quicknote", chroma.StyleEntries{
	chroma.Text:                "#CDD6F4",
	chroma.Error:               "#F38BA8",
	chroma.Comment:             "#6C7086",
	chroma.Keyword:             "#89B4FA",
	chroma.KeywordNamespace:    "#89B4FA",
	chroma.KeywordType:         "#89B4FA",
	chroma.Operator:            "#89DCEB",
	chroma.Punctuation:         "#94E2D5",
	chroma.Name:                "#CDD6F4",
	chroma.NameAttribute:       "#89B4FA",
	chroma.NameClass:           "#F9E2AF",
	chroma.NameConstant:        "#FAB387",
	chroma.NameDecorator:       "#F9E2AF",
	chroma.NameException:       "#F38BA8",
	chroma.NameFunction:        "#89B4FA",
	chroma.NameOther:           "#CDD6F4",
	chroma.NameTag:             "#89B4FA",
	chroma.LiteralNumber:       "#FAB387",
	chroma.LiteralString:       "#A6E3A1",
	chroma.LiteralStringEscape: "#F9E2AF",
	chroma.GenericDeleted:      "#F38BA8",
	chroma.GenericInserted:     "#A6E3A1",
	chroma.Background:          "bg:#1E1E2E",
	chroma.GenericEmph:         "#A6E3A1 italic",
	chroma.GenericStrong:       "#F38BA8 bold",
	chroma.GenericPrompt:       "#89B4FA",
	chroma.GenericTraceback:    "#89B4FA",
	chroma.GenericHeading:      "bg:#89B4FA #FFFFFF bold",
	chroma.GenericSubheading:   "bg:#89B4FA #FFFFFF bold",
	chroma.LiteralStringBacktick: "bg:#11111B #F38BA8",
}))

func highlightText(content string) string {
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

	style := styles.Get("quicknote")
	if style == nil {
		style = styles.Fallback
	}

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}

	var buf bytes.Buffer
	err = formatter.Format(&buf, style, iterator)
	if err != nil {
		return content
	}

	return buf.String()
}

func main() {
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

	p := tea.NewProgram(initialModel(db), tea.WithAltScreen(), tea.WithInput(tuiInput))
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}