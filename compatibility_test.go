package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestQN048T35Math(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"500 * 0.15 =", "500 * 0.15 = 75"},
		{"log(100) + log2(8) =", "log(100) + log2(8) = 5"},
		{"ceil(1.2) + floor(2.8) =", "ceil(1.2) + floor(2.8) = 4"},
		{"pi - pi =", "pi - pi = 0"}, {"10 ÷ 2 x 3 =", "10 ÷ 2 x 3 = 15"},
		{"2^3 =", "2^3 = 8"}, {"20% of 50 =", "20% of 50 = 10"},
		{"100 + 10% =", "100 + 10% = 110"}, {"√64 + ∛27 =", "√64 + ∛27 = 11"},
		{"5! + 6!! =", "5! + 6!! = 168"}, {"sqrt(64) + 10 =", "sqrt(64) + 10 = 18"},
		{"x := 4\nx * 3 =", "x := 4\nx * 3 = 12"},
		{"100 c to f =", "100 c to f = 212"}, {"2 km to m =", "2 km to m = 2000"},
		{"FF hex to dec =", "FF hex to dec = 255"},
		{"28 Feb 2024 + 1 day =", "28 Feb 2024 + 1 day = 29 Feb 2024"},
		{"3:30PM + 1.5 hours =", "3:30PM + 1.5 hours = 5:00PM"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, changed := updateMathBuffer(tc.in, "=")
			if !changed || got != tc.want {
				t.Fatalf("got %q, changed %v; want %q", got, changed, tc.want)
			}
			again, c := updateMathBuffer(got, "right")
			if c || again != got {
				t.Fatal("unstable evaluation")
			}
		})
	}
	for _, in := range []string{"```go\n2+2 =\n```", "`2+2 =`", "// 2+2 =", "unknown + 1 =", "2 + ="} {
		if got, c := updateMathBuffer(in, "="); c || got != in {
			t.Fatalf("modified protected/invalid expression %q -> %q", in, got)
		}
	}
	if got, c := updateMathBuffer("2+2 =", "right"); c || got != "2+2 =" {
		t.Fatal("navigation evaluated incomplete equation")
	}
}

func TestQN048T35InvalidMathCalls(t *testing.T) {
	for _, expr := range []string{"sqrt()", "sqrt('text')", "sqrt(1,2)", "fact(-1)", "fact(1.5)", "sqrt(-1)", "1/0", "fact(171)", "doublefact(1000000000000)", "2==2", "x := 1/0\nx ="} {
		t.Run(expr, func(t *testing.T) {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("invalid input panicked: %v", p)
				}
			}()
			in := expr + " ="
			got, c := updateMathBuffer(in, "=")
			if c || got != in {
				t.Fatalf("invalid input changed: %q", got)
			}
		})
	}
}

func TestQN048T35Snippets(t *testing.T) {
	for _, token := range []string{":date:", ":today:", ":Today:", ":time:", ":Time:", ":now:", ":Now:", ":uuid:"} {
		t.Run(token, func(t *testing.T) {
			in := "你好 e\u0301 " + token + " tail\n"
			col := len([]rune("你好 e\u0301 " + token))
			before := time.Now()
			got, c := expandKeywords(in, false, ":", 0, col)
			after := time.Now()
			if !c || !strings.HasPrefix(got, "你好 e\u0301 ") || !strings.HasSuffix(got, " tail\n") {
				t.Fatalf("expansion %q %v", got, c)
			}
			value := strings.TrimSuffix(strings.TrimPrefix(got, "你好 e\u0301 "), " tail\n")
			if token == ":uuid:" {
				if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(value) {
					t.Fatal(value)
				}
			} else {
				layouts := map[string]string{":date:": "02 Jan 2006", ":today:": "02 Jan 2006", ":Today:": "Mon 02 Jan 2006", ":time:": "3:04PM", ":Time:": "3:04:05PM", ":now:": "3:04PM 02 Jan 2006", ":Now:": "3:04PM Mon 02 Jan 2006"}
				if value != before.Format(layouts[token]) && value != after.Format(layouts[token]) {
					t.Fatal(value)
				}
			}
		})
	}
	for _, in := range []string{"`:today:`", "// :today:", "```\n:today:\n```"} {
		row := 0
		if strings.Contains(in, "\n") {
			row = 1
		}
		line := strings.Split(in, "\n")[row]
		col := strings.Index(line, ":today:") + len(":today:")
		if got, c := expandKeywords(in, false, ":", row, col); c || got != in {
			t.Fatal("expanded protected text")
		}
	}
	for _, tc := range []struct {
		code bool
		key  string
		col  int
	}{{true, ":", 7}, {false, "x", 7}, {false, ":", 6}} {
		if got, c := expandKeywords(":today:", tc.code, tc.key, 0, tc.col); c || got != ":today:" {
			t.Fatal("expanded outside trigger")
		}
	}
}

func TestQN048T35EditorShortcuts(t *testing.T) {
	for _, tc := range []struct {
		key      tea.KeyMsg
		in, want string
		col      int
	}{
		{tea.KeyMsg{Type: tea.KeyCtrlB}, "你好", "你好****", 4},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i"), Alt: true}, "你好", "你好**", 3},
		{tea.KeyMsg{Type: tea.KeyCtrlX}, "- [ ] café", "- [x] ~~café~~", 10},
		{tea.KeyMsg{Type: tea.KeyCtrlX}, "- [x] ~~café~~", "- [ ] café", 10},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m"), Alt: true}, "a\tb\n", "math:\na\tb\n", 3},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v"), Alt: true}, "a\tb\n", "code:\na\tb\n", 3},
	} {
		t.Run(tc.want, func(t *testing.T) {
			m, _, _, _, _ := asyncModel(t)
			m.buffer = stringToBuffer(tc.in)
			m.cursorRow = 0
			m.cursorCol = len(m.buffer[0])
			m.undoStack = nil
			m.redoStack = nil
			m = updateTest(t, m, tc.key)
			if m.buffer.Text() != tc.want {
				t.Fatalf("got %q want %q", m.buffer.Text(), tc.want)
			}
			if m.cursorCol != tc.col {
				t.Fatalf("cursor col %d want %d", m.cursorCol, tc.col)
			}
			m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlZ})
			if m.buffer.Text() != tc.in {
				t.Fatalf("undo got %q want %q", m.buffer.Text(), tc.in)
			}
		})
	}
}

func TestQN048T35HeadingsAndHighlight(t *testing.T) {
	for _, tc := range []struct{ in, want string }{{"```\n# hidden\n```\n// # ignored\n## Café 👩‍💻", "Café 👩‍💻"}, {"plain", "Untitled"}, {"\t### Title", "Title"}} {
		if got := getFirstHeading(tc.in); got != tc.want {
			t.Fatalf("heading %q", got)
		}
	}
	theme := Theme{BgDark: "#1E1E2E", BgMed: "#313244", TextLight: "#CDD6F4", TextDark: "#11111B", AccentNormal: "#89B4FA", AccentMath: "#F9E2AF", AccentCode: "#A6E3A1", AccentTrash: "#F38BA8", Comment: "#6C7086"}
	for _, in := range []string{"# Café\n**bold** ~~done~~\n", "code: go\n\tprintln(\"你好\")\n", "code: unknown\nraw\ttext", "```json\n{\"a\":1}\n```", "math:\n2+2 = 4"} {
		got := stripANSI(computeHighlight(in, theme))
		if strings.TrimSuffix(got, "\n") != strings.TrimSuffix(in, "\n") {
			t.Fatalf("highlight changed content %q -> %q", in, got)
		}
	}
}

func TestQN048T35MathModeSaveUndo(t *testing.T) {
	m, store, _, first, second := asyncModel(t)
	m.buffer = stringToBuffer("math:\n2+2")
	m.cursorRow, m.cursorCol = 1, 3
	m.undoStack, m.redoStack = nil, nil
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("=")})
	if m.buffer.Text() != "math:\n2+2 = 4" || !m.needsSave {
		t.Fatalf("calculation %q dirty %v", m.buffer.Text(), m.needsSave)
	}
	m = drainTestCmd(t, m, m.requestSave())
	got, err := store.GetNote(first.ID)
	if err != nil || got.Content != "math:\n2+2 = 4" {
		t.Fatalf("save %+v %v", got, err)
	}
	got, err = store.GetNote(second.ID)
	if err != nil || got.Content != "beta" {
		t.Fatal("calculation changed another note")
	}
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyCtrlZ})
	if m.buffer.Text() != "math:\n2+2" {
		t.Fatalf("atomic calculation undo %q", m.buffer.Text())
	}
}

func TestQN048T35SnippetOffsetsAndLists(t *testing.T) {
	before := time.Now().AddDate(0, 0, 2).Format("02 Jan 2006")
	got, changed := expandKeywords(":today + 2 days:", false, ":", 0, 16)
	after := time.Now().AddDate(0, 0, 2).Format("02 Jan 2006")
	if !changed || (got != before && got != after) {
		t.Fatalf("offset snippet %q %v", got, changed)
	}
	gotLines := autoRenumber([]string{"1. first", "1. second", "    1. nested", "    1. next", "", "1. reset"})
	if strings.Join(gotLines, "\n") != "1. first\n2. second\n    a. nested\n    b. next\n\n1. reset" {
		t.Fatal(gotLines)
	}
}

func TestQN048T35CodeModeKeepsSnippetsLiteral(t *testing.T) {
	m, _, _, _, _ := asyncModel(t)
	m.buffer = stringToBuffer("code: go\n:date")
	m.cursorRow, m.cursorCol = 1, 5
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	if m.buffer.Text() != "code: go\n:date:" {
		t.Fatalf("code snippet expanded: %q", m.buffer.Text())
	}
	m.buffer = stringToBuffer("code: go\n2+2")
	m.cursorRow, m.cursorCol = 1, 3
	m = updateTest(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("=")})
	// Preserve the existing equals-key spacing macro without evaluating code.
	if m.buffer.Text() != "code: go\n2+2 =" {
		t.Fatalf("code evaluated: %q", m.buffer.Text())
	}
}
