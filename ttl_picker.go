package main

import (
	"fmt"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var ttlRFC3339 = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

var ttlDurationToken = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h|d)`)

// ResolveTTLInput resolves elapsed durations or local calendar input to an owned
// absolute deadline. Offset-free wall times must identify exactly one instant.
func ResolveTTLInput(input string, now time.Time, loc *time.Location) (ExpirationChoice, error) {
	if loc == nil {
		loc = time.Local
	}
	input = strings.TrimSpace(input)
	var deadline time.Time
	switch strings.ToLower(input) {
	case "never":
		return ExpirationChoice{Source: ExpirationNone}, nil
	case "today":
		local := now.In(loc)
		deadline = time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 0, loc)
	default:
		if parsed, e := time.Parse(time.RFC3339Nano, input); e == nil && ttlRFC3339.MatchString(input) {
			deadline = parsed
		} else if wall, e := time.Parse("2006-01-02 15:04", input); e == nil {
			// Enumerate the offsets around this date rather than accepting time.Date's
			// implementation-dependent choice for a DST gap or fold.
			offsets := map[int]bool{}
			for h := -48; h <= 48; h++ {
				_, offset := wall.Add(time.Duration(h) * time.Hour).In(loc).Zone()
				offsets[offset] = true
			}
			matches := []time.Time{}
			for offset := range offsets {
				candidate := wall.Add(-time.Duration(offset) * time.Second)
				if candidate.In(loc).Format("2006-01-02 15:04") == input {
					matches = append(matches, candidate)
				}
			}
			if len(matches) != 1 {
				return ExpirationChoice{}, fmt.Errorf("local time is ambiguous or nonexistent; use RFC3339 with an explicit offset")
			}
			deadline = matches[0]
		} else {
			units := map[string]int64{"ns": 1, "us": 1000, "µs": 1000, "ms": int64(time.Millisecond), "s": int64(time.Second), "m": int64(time.Minute), "h": int64(time.Hour), "d": int64(24 * time.Hour)}
			tokens := ttlDurationToken.FindAllStringSubmatch(input, -1)
			consumed := ""
			sum := new(big.Rat)
			for _, token := range tokens {
				consumed += token[0]
				value, ok := new(big.Rat).SetString(token[1])
				if !ok {
					return ExpirationChoice{}, fmt.Errorf("invalid duration")
				}
				value.Mul(value, new(big.Rat).SetInt64(units[token[2]]))
				sum.Add(sum, value)
			}
			if consumed != input || sum.Sign() <= 0 || sum.Cmp(new(big.Rat).SetInt64(int64(^uint64(0)>>1))) > 0 {
				return ExpirationChoice{}, fmt.Errorf("enter a positive duration (15m, 2h, 3d) or future date-time")
			}
			// Round fractional nanoseconds upward; positive input must never expire early.
			ns := new(big.Int).Quo(sum.Num(), sum.Denom())
			if new(big.Int).Rem(sum.Num(), sum.Denom()).Sign() != 0 {
				ns.Add(ns, big.NewInt(1))
			}
			deadline = now.Add(time.Duration(ns.Int64()))
		}
	}
	if !deadline.After(now) || deadline.Year() < 1 || deadline.Year() > 9999 {
		return ExpirationChoice{}, fmt.Errorf("expiration must be a future instant within years 1–9999")
	}
	deadline = deadline.UTC()
	return ExpirationChoice{Deadline: &deadline, Source: ExpirationPreset}, nil
}

type TTLPicker struct {
	Index    int
	Input    textinput.Model
	Error    string
	Key      int
	Version  uint64
	Location *time.Location
}

var ttlLabels = []string{"15 minutes", "1 hour", "Today", "3 days", "7 days", "Never", "Custom"}
var ttlValues = []string{"15m", "1h", "today", "3d", "7d", "never", ""}

func ttlEnabled(c ActionContext) (bool, string) {
	if c.Note.IsGhost {
		return false, "Ghosts have no persisted expiration"
	}
	if c.State == stateTrash || c.Note.DeletedAt != "" {
		return false, "Restore from Trash before setting expiration"
	}
	if c.Note.ID <= 0 {
		return false, "Save this scratch draft before setting expiration"
	}
	return true, ""
}

// Confirmation samples the injected clock at the user's explicit action.
// Production models without an injected test clock use the real wall clock.
func (m model) ttlNow() time.Time {
	if m.clock != nil {
		return m.clock.Now()
	}
	return (RealClock{}).Now()
}
func (m *model) openTTLPicker() tea.Cmd {
	if enabled, reason := ttlEnabled(m.actionContext()); !enabled {
		m.flashMsg = reason
		return nil
	}
	input := textinput.New()
	input.Prompt = "Custom: "
	input.CharLimit = 120
	m.ttlPicker = TTLPicker{Input: input, Key: m.activeKey(), Version: m.localVersion, Location: time.Local}
	m.timerNow = m.ttlNow()
	m.pushOverlay(stateTTLPicker)
	return nil
}
func (m model) ttlInput() string {
	if m.ttlPicker.Index == len(ttlValues)-1 {
		return m.ttlPicker.Input.Value()
	}
	return ttlValues[max(0, min(m.ttlPicker.Index, len(ttlValues)-1))]
}
func (m model) ttlConfirmationBody(now time.Time) string {
	choice, err := ResolveTTLInput(m.ttlInput(), now, m.ttlPicker.Location)
	if err != nil {
		return "Expiration unavailable: " + err.Error()
	}
	if choice.Deadline == nil {
		return "Never expire automatically.\nThe current directive stays observed."
	}
	return "Expires at " + choice.Deadline.In(m.ttlPicker.Location).Format("2006-01-02 15:04:05 MST -07:00") + "\nRelative choices start when confirmed."
}
func (m *model) ttlPickerKey(key string) tea.Cmd {
	switch key {
	case "esc", "ctrl+c":
		m.popOverlay()
		return nil
	case "up":
		if m.state == stateTTLPicker {
			m.ttlPicker.Index = max(0, m.ttlPicker.Index-1)
			m.ttlPicker.Error = ""
			return nil
		}
	case "down":
		if m.state == stateTTLPicker {
			m.ttlPicker.Index = min(len(ttlLabels)-1, m.ttlPicker.Index+1)
			m.ttlPicker.Error = ""
			return nil
		}
	case "enter":
		if m.ttlPicker.Key != m.activeKey() || m.ttlPicker.Version != m.localVersion || m.Note.DeletedAt != "" {
			m.ttlPicker.Error = "Draft changed; reopen expiration"
			return nil
		}
		if m.ttlPicker.Index == len(ttlLabels)-1 && m.state == stateTTLPicker {
			m.pushOverlay(stateTTLCustom)
			return m.ttlPicker.Input.Focus()
		}
		now := m.ttlNow()
		choice, err := ResolveTTLInput(m.ttlInput(), now, m.ttlPicker.Location)
		if err != nil {
			m.ttlPicker.Error = err.Error()
			return nil
		}
		if m.Note.IsPinned && choice.Deadline != nil {
			m.ttlPicker.Error = "Unpin first to set a finite expiration"
			return nil
		}
		m.timerNow = now
		m.confirm("ttl", "Set expiration?", m.ttlConfirmationBody(now), m.ttlInput())
		return nil
	}
	if m.state == stateTTLCustom {
		var cmd tea.Cmd
		m.ttlPicker.Input, cmd = m.ttlPicker.Input.Update(keyMessage(key))
		m.ttlPicker.Error = ""
		return cmd
	}
	return nil
}
func (m *model) confirmTTL(c Confirmation) tea.Cmd {
	if m.ttlPicker.Key != m.activeKey() || m.ttlPicker.Version != m.localVersion || m.Note.DeletedAt != "" {
		m.flashMsg = "Expiration canceled: draft changed"
		return nil
	}
	now := m.ttlNow()
	m.timerNow = now
	choice, err := ResolveTTLInput(c.Payload, now, m.ttlPicker.Location)
	if err != nil {
		m.ttlPicker.Error = err.Error()
		return nil
	}
	if m.Note.IsPinned && choice.Deadline != nil {
		m.ttlPicker.Error = "Unpin first to set a finite expiration"
		return nil
	}
	// Close just this picker and its custom field, preserving any parent overlays.
	if m.state == stateTTLCustom {
		m.popOverlay()
	}
	if m.state == stateTTLPicker {
		m.popOverlay()
	}
	m.flashMsg = "Setting expiration…"
	return m.requestLifecycle(lifecycleIntent{Kind: "expiration", Expiration: choice, GuardTTL: true, SourceKey: m.activeKey(), SourceVersion: m.localVersion})
}
func (m model) ttlPickerView() string {
	if m.state == stateTTLCustom {
		body := m.ttlPicker.Input.View() + "\nPositive duration: 15m, 2h, 3d\nRFC3339 with offset, or YYYY-MM-DD HH:MM local\n" + m.ttlPicker.Error
		return m.panel("Custom expiration", body, m.shortcut("ttl.cancel")+" Cancel · "+m.shortcut("ttl.review")+" Review")
	}
	// Keep the selected row at the top in tiny panels. Error/help text stays in
	// the body so the cancel and review controls remain visible at every size.
	l := m.layout()
	available := max(1, l.Height-3)
	start := max(0, m.ttlPicker.Index-available+1)
	lines := []string{}
	for i := start; i < min(len(ttlLabels), start+available); i++ {
		prefix := "  "
		if i == m.ttlPicker.Index {
			prefix = "> "
		}
		lines = append(lines, prefix+ttlLabels[i])
	}
	title := "Set expiration"
	if m.ttlPicker.Error != "" {
		title = m.ttlPicker.Error
	}
	return m.panel(title, strings.Join(lines, "\n"), m.shortcut("ttl.cancel")+" Cancel · ↑↓ Choose\n"+m.shortcut("ttl.review")+" Review")
}

// lifecycleStatus only reads saved metadata and the last timer message.
func (m model) lifecycleStatus() string {
	if m.Note.IsGhost {
		return "Ghost · unsaved by design"
	}
	parts := []string{}
	if m.Note.IsPinned {
		parts = append(parts, "Pinned")
	} else if m.Note.ExpiresAt.Valid {
		if at, err := ParseLifecycleTime(m.Note.ExpiresAt.String); err == nil {
			parts = append(parts, "Expires in "+formatKillTime(max(time.Duration(0), at.Sub(m.timerNow))))
		}
	}
	if m.Note.ExpirationNeedsReview {
		parts = append(parts, "Expiration needs review")
	}
	if len(parts) == 0 {
		return "TTL Never"
	}
	return strings.Join(parts, " · ")
}
func (m model) statusLayout() (string, HitRegion) {
	l := m.layout()
	status := "[" + m.saveStatus() + "]"
	region := HitRegion{}
	if m.state == stateTrash {
		return "Trash · " + m.shortcut("note.restore") + " Restore · Esc Back", region
	}
	label := m.lifecycleStatus()
	if !m.Note.IsGhost {
		// Keep save failure first even when a toast or countdown is present.
		fullWidth := ansi.StringWidth(label)
		space := l.Width - ansi.StringWidth(status) - 3
		if fullWidth > space && !m.Note.ExpirationNeedsReview {
			label = "TTL"
			fullWidth = 3
		}
		if fullWidth <= space {
			x := ansi.StringWidth(status) + 3
			status += " · " + label
			if l.Height > 1 && m.state == stateTyping {
				if enabled, _ := ttlEnabled(m.actionContext()); enabled {
					region = HitRegion{X: x, Y: l.Height - 1, Width: fullWidth, Height: 1, ActionID: "ttl.set"}
				}
			}
		}
	} else {
		status += " · " + label
	}
	status += fmt.Sprintf(" · Ln %d Col %d · %s Commands", m.cursorRow+1, m.cursorCol+1, m.shortcut("commands.open"))
	if m.flashMsg != "" {
		status += " · " + m.flashMsg
	}
	return status, region
}
func (m model) ttlStatusRegion() HitRegion { _, region := m.statusLayout(); return region }
