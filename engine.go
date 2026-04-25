package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Knetic/govaluate"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

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

// --- Buffer & Wrapping Helpers ---
func stringToBuffer(s string) [][]rune {
	if s == "" {
		return [][]rune{{}}
	}
	s = strings.ReplaceAll(s, "\t", "    ")
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
				if r > 127 {
					currentPhys.WriteString("\x1b[4m" + string(r) + "\x1b[24m")
				} else {
					currentPhys.WriteString(visibleCursor)
				}
				for _, ansi := range activeANSI {
					currentPhys.WriteString(ansi)
				}
			} else {
				currentPhys.WriteRune(r)
			}
		} else {
			if isCursorLine && visualIdx == cursorCol {
				if r > 127 {
					currentPhys.WriteString("\x1b[4m" + string(r) + "\x1b[24m")
				} else {
					currentPhys.WriteString(visibleCursor)
				}
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

func applySearchHighlight(highlighted string, indices []int, theme Theme) string {
	if len(indices) == 0 {
		return highlighted
	}

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
		currentWidth := 0
		end := start
		lastSpace := -1
		breakPoint := -1

		for end < len(runes) {
			rw := runewidth.RuneWidth(runes[end])

			if currentWidth == 0 && rw > width {
				end++
				break
			}
			if currentWidth+rw > width {
				break
			}

			switch runes[end] {
			case ' ':
				lastSpace = end
			case ',', '.', ';', ':', '-':
				breakPoint = end + 1
			}

			currentWidth += rw
			end++
		}

		if end == len(runes) {
			starts = append(starts, start)
			lengths = append(lengths, end-start)
			break
		}

		if lastSpace != -1 && lastSpace >= start {
			starts = append(starts, start)
			lengths = append(lengths, lastSpace-start)
			start = lastSpace + 1
		} else if breakPoint != -1 && breakPoint > start {
			starts = append(starts, start)
			lengths = append(lengths, breakPoint-start)
			start = breakPoint
		} else {
			starts = append(starts, start)
			lengths = append(lengths, end-start)
			start = end
		}
	}
	return starts, lengths
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

func getKillTimerInfo(content string) (string, *time.Duration) {
	re := regexp.MustCompile(`(?i)#kill\s+([0-9.]+)\s*([a-zA-Z]+)`)

	lines := strings.Split(content, "\n")
	inCodeBlock := false
	var validMatches [][]string

	for _, line := range lines {
		if strings.Count(line, "```")%2 != 0 {
			inCodeBlock = !inCodeBlock
		}
		if inCodeBlock || strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		matches := re.FindAllStringSubmatchIndex(line, -1)
		for _, m := range matches {
			if strings.Count(line[:m[0]], "`")%2 != 0 {
				continue
			}
			validMatches = append(validMatches, []string{
				line[m[0]:m[1]],
				line[m[2]:m[3]],
				line[m[4]:m[5]],
			})
		}
	}

	if len(validMatches) == 0 {
		return "", nil
	}

	var hashBuilder strings.Builder
	for _, m := range validMatches {
		hashBuilder.WriteString(m[0])
		hashBuilder.WriteString("|")
	}
	hashStr := hashBuilder.String()

	lastMatch := validMatches[len(validMatches)-1]
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

	inCodeBlock := false

	for i, line := range lines {
		if strings.Count(line, "```")%2 != 0 {
			inCodeBlock = !inCodeBlock
		}
		if inCodeBlock {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}

		if idx := strings.Index(line, ":="); idx != -1 {
			if strings.Count(line[:idx], "`")%2 != 0 {
				continue
			}
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
			if strings.Count(line[:idx], "`")%2 != 0 {
				continue
			}
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

func expandKeywords(val string, isCodeMode bool, lastKey string, cursorRow int, cursorCol int) (string, bool) {
	if isCodeMode {
		return val, false
	}
	if lastKey != ":" {
		return val, false
	}
	if !strings.Contains(val, ":") {
		return val, false
	}

	changed := false
	lines := strings.Split(val, "\n")
	inCodeBlock := false

	reDate := regexp.MustCompile(`:(today|Today|time|Time|now|Now)(?:\s*([\+\-])\s*(\d+(?:\.\d+)?)\s*([a-zA-Z]+))?:`)
	reUUID := regexp.MustCompile(`:uuid:`)

	for i := range lines {
		if strings.Count(lines[i], "```")%2 != 0 {
			inCodeBlock = !inCodeBlock
		}

		if i != cursorRow {
			continue
		}

		if inCodeBlock {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "//") {
			continue
		}

		dateMatches := reDate.FindAllStringSubmatchIndex(lines[i], -1)
		for j := len(dateMatches) - 1; j >= 0; j-- {
			start, end := dateMatches[j][0], dateMatches[j][1]

			endRuneIdx := len([]rune(lines[i][:end]))
			if endRuneIdx != cursorCol {
				continue
			}
			if strings.Count(lines[i][:start], "`")%2 != 0 {
				continue
			}

			base := lines[i][dateMatches[j][2]:dateMatches[j][3]]
			op := ""
			if dateMatches[j][4] != -1 {
				op = lines[i][dateMatches[j][4]:dateMatches[j][5]]
			}
			valStr := ""
			if dateMatches[j][6] != -1 {
				valStr = lines[i][dateMatches[j][6]:dateMatches[j][7]]
			}
			unit := ""
			if dateMatches[j][8] != -1 {
				unit = strings.ToLower(lines[i][dateMatches[j][8]:dateMatches[j][9]])
			}

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

			lines[i] = lines[i][:start] + formatted + lines[i][end:]
			changed = true
		}

		uuidMatches := reUUID.FindAllStringIndex(lines[i], -1)
		for j := len(uuidMatches) - 1; j >= 0; j-- {
			start, end := uuidMatches[j][0], uuidMatches[j][1]

			endRuneIdx := len([]rune(lines[i][:end]))
			if endRuneIdx != cursorCol {
				continue
			}

			if strings.Count(lines[i][:start], "`")%2 != 0 {
				continue
			}

			lines[i] = lines[i][:start] + generateUUID() + lines[i][end:]
			changed = true
		}
	}

	if changed {
		return strings.Join(lines, "\n"), true
	}
	return val, false
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

// --- Syntax Highlighting Engine ---
func computeHighlight(rawText string, t Theme) string {
	isCodeMode := strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawText)), "code:")

	tokenizeMarkdown := func(text string, re *regexp.Regexp, baseRune rune, tokenMap map[string]string) string {
		chunks := strings.Split(text, "```")
		for i := range chunks {
			if i%2 == 1 {
				continue
			}
			lines := strings.Split(chunks[i], "\n")
			for l, line := range lines {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				matches := re.FindAllStringIndex(line, -1)
				for j := len(matches) - 1; j >= 0; j-- {
					start, end := matches[j][0], matches[j][1]
					if strings.Count(line[:start], "`")%2 != 0 {
						continue
					}
					matchStr := line[start:end]
					token := string(rune(baseRune + rune(len(tokenMap))))
					tokenMap[token] = matchStr
					line = line[:start] + token + line[end:]
				}
				lines[l] = line
			}
			chunks[i] = strings.Join(lines, "\n")
		}
		return strings.Join(chunks, "```")
	}

	strikeMap, h3Map, h2Map, h1Map := make(map[string]string), make(map[string]string), make(map[string]string), make(map[string]string)
	keywordMap, killMap, tagMap := make(map[string]string), make(map[string]string), make(map[string]string)

	if !isCodeMode {
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`~~(.*?)~~`), 0xF0000, strikeMap)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?m)^[ \t]*###[ \t]+.*`), 0xF1000, h3Map)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?m)^[ \t]*##[ \t]+.*`), 0xF2000, h2Map)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?m)^[ \t]*#[ \t]+.*`), 0xF3000, h1Map)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?i)#(idea|todo)\b`), 0xF4000, keywordMap)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`(?i)#kill\s+[0-9.]+\s*[a-zA-Z]+`), 0xF5000, killMap)
		rawText = tokenizeMarkdown(rawText, regexp.MustCompile(`#[a-zA-Z0-9_-]+`), 0xF6000, tagMap)
	}

	highlighted := highlightText(rawText, t)
	rawLines := strings.Split(rawText, "\n")
	hlLines := strings.Split(highlighted, "\n")

	if len(hlLines) > len(rawLines) {
		hlLines = hlLines[:len(rawLines)]
	} else if len(hlLines) < len(rawLines) {
		for len(hlLines) < len(rawLines) {
			hlLines = append(hlLines, "")
		}
	}

	commentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.Comment)).Italic(true)
	blockquoteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.AccentNormal))
	listStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.AccentNormal)).Bold(true)
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
			hlLines[i] = strings.Replace(hlLines[i], match[1], listStyle.Render(match[1]), 1)
		}
	}
	highlighted = strings.Join(hlLines, "\n")

	strikeStyle := lipgloss.NewStyle().Strikethrough(true)
	h3Style := lipgloss.NewStyle().Foreground(lipgloss.Color(t.AccentNormal)).Bold(true)
	h2Style := lipgloss.NewStyle().Foreground(lipgloss.Color(t.AccentNormal)).Bold(true).Underline(true)
	h1Style := lipgloss.NewStyle().Foreground(lipgloss.Color(t.BgDark)).Background(lipgloss.Color(t.AccentNormal)).Bold(true)
	killStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.BgDark)).Background(lipgloss.Color(t.AccentTrash)).Bold(true)
	tagStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(t.AccentNormal)).Italic(true)
	keywordStyle := lipgloss.NewStyle().Background(lipgloss.Color(t.AccentMath)).Foreground(lipgloss.Color(t.BgDark)).Bold(true)

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

	return highlighted
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
				lexer = lexers.Get("markdown")
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

	dynamicStyle := chroma.MustNewStyle("dynamicTheme", chroma.StyleEntries{
		chroma.Text:                  t.TextLight,
		chroma.Error:                 t.AccentTrash,
		chroma.Comment:               t.Comment,
		chroma.Keyword:               t.AccentNormal,
		chroma.KeywordNamespace:      t.AccentNormal,
		chroma.KeywordType:           t.AccentNormal,
		chroma.Operator:              t.AccentMath,
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