package main

import (
	"database/sql"
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Clock interface{ Now() time.Time }
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func clockOrReal(c Clock) Clock {
	if c == nil {
		return RealClock{}
	}
	return c
}

type ExpirationSource string

const (
	ExpirationNone      ExpirationSource = "none"
	ExpirationDirective ExpirationSource = "directive"
	ExpirationPreset    ExpirationSource = "preset"
)

type DeleteReason string

const (
	DeleteManual  DeleteReason = "manual"
	DeleteExpired DeleteReason = "expired"
)

type Directive struct {
	Present     bool
	Fingerprint string
	Duration    time.Duration
}
type ExpirationChoice struct {
	Deadline *time.Time
	Source   ExpirationSource
}
type RestoreMode int

const (
	RestoreWithoutTimer RestoreMode = iota
	RestoreResetDirective
)

type LifecycleChange struct {
	ID        NoteID
	Revision  int64
	DeletedAt time.Time
	Reason    DeleteReason
}
type MaintenanceResult struct {
	Expired []LifecycleChange
	Purged  []NoteID
}

var ErrDirective = errors.New("expiration directive needs review")

const lifecycleTimeLayout = "2006-01-02T15:04:05.000Z"

func FormatLifecycleTime(t time.Time) string {
	t = t.UTC()
	if t.Nanosecond()%int(time.Millisecond) != 0 {
		t = t.Truncate(time.Millisecond).Add(time.Millisecond)
	}
	return t.Format(lifecycleTimeLayout)
}
func ParseLifecycleTime(raw string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02"} {
		if t, e := time.Parse(layout, raw); e == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, errors.New("invalid lifecycle timestamp")
}

var killToken = regexp.MustCompile(`(?i)#kill\b`)
var killValue = regexp.MustCompile(`(?i)^\s+([+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+))\s*([a-z]+)\b`)

// The last valid directive outside fenced/inline code and comments wins.
func ParseKillDirective(content string) (Directive, error) {
	var d Directive
	invalid := false
	fence := ""
	comment := false
	for _, line := range strings.Split(content, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			mark := trim[:3]
			if fence == "" {
				fence = mark
			} else if fence == mark {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		var clean strings.Builder
		inline := 0
		for i := 0; i < len(line); {
			if comment {
				if j := strings.Index(line[i:], "-->"); j >= 0 {
					i += j + 3
					comment = false
					clean.WriteByte(' ')
					continue
				}
				break
			}
			if strings.HasPrefix(line[i:], "<!--") {
				comment = true
				i += 4
				continue
			}
			if line[i] == '`' {
				run := 1
				for i+run < len(line) && line[i+run] == '`' {
					run++
				}
				if inline == 0 {
					inline = run
				} else if inline == run {
					inline = 0
				}
				clean.WriteString(strings.Repeat(" ", run))
				i += run
				continue
			}
			if inline == 0 && strings.HasPrefix(line[i:], "//") {
				break
			}
			if inline != 0 {
				clean.WriteByte(' ')
			} else {
				clean.WriteByte(line[i])
			}
			i++
		}
		text := clean.String()
		for _, ix := range killToken.FindAllStringIndex(text, -1) {
			m := killValue.FindStringSubmatch(text[ix[1]:])
			if m == nil {
				invalid = true
				continue
			}
			v, e := strconv.ParseFloat(m[1], 64)
			unit := strings.ToLower(m[2])
			var mult time.Duration
			switch unit {
			case "mo", "mon", "month", "months":
				mult = 30 * 24 * time.Hour
			case "w", "wk", "wks", "week", "weeks":
				mult = 7 * 24 * time.Hour
			case "d", "day", "days":
				mult = 24 * time.Hour
			case "h", "hr", "hrs", "hour", "hours":
				mult = time.Hour
			case "m", "min", "mins", "minute", "minutes":
				mult = time.Minute
			case "s", "sec", "secs", "second", "seconds":
				mult = time.Second
			}
			nanos := v * float64(mult)
			if e != nil || mult == 0 || math.IsNaN(nanos) || math.IsInf(nanos, 0) || nanos < 1 || nanos >= float64(math.MaxInt64) {
				invalid = true
				continue
			}
			dur := time.Duration(nanos)
			d = Directive{true, "#kill " + m[1] + " " + unit, dur}
		}
	}
	if !d.Present && invalid {
		return Directive{}, ErrDirective
	}
	return d, nil
}
func DeriveExpiration(n Note, content string, now time.Time) (Note, error) {
	n = cloneNote(n)
	n.Content = content
	original := n
	if n.IsGhost {
		return n, nil
	}
	d, e := ParseKillDirective(content)
	if e != nil {
		n.ExpirationNeedsReview = true
		return n, e
	}
	if n.ExpirationSource == "" {
		n.ExpirationSource = ExpirationNone
	}
	if d.Fingerprint == n.KillHash {
		return n, nil
	}
	n.KillHash = d.Fingerprint
	n.ExpirationNeedsReview = false
	if n.IsPinned {
		n.ExpiresAt = sql.NullString{}
		n.ExpirationSource = ExpirationNone
		return n, nil
	}
	if d.Present {
		deadline := now.Add(d.Duration)
		if deadline.Year() > 9999 || deadline.Year() < 1 {
			original.ExpirationNeedsReview = true
			return original, ErrDirective
		}
		n.ExpiresAt = sql.NullString{String: FormatLifecycleTime(deadline), Valid: true}
		n.ExpirationSource = ExpirationDirective
		n.KillStart = now
	} else if n.ExpirationSource == ExpirationDirective {
		n.ExpiresAt = sql.NullString{}
		n.ExpirationSource = ExpirationNone
	}
	return n, nil
}
func noteDue(n Note, now time.Time) bool {
	if n.IsGhost || n.ID <= 0 || n.IsPinned || n.ExpirationNeedsReview || n.DeletedAt != "" || !n.ExpiresAt.Valid {
		return false
	}
	t, e := ParseLifecycleTime(n.ExpiresAt.String)
	return e == nil && !t.After(now)
}
