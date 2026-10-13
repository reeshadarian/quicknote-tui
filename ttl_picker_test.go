package main

import (
	"testing"
	"time"
)

func TestQN029T20Presets(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	for input, d := range map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "3d": 72 * time.Hour, "7d": 168 * time.Hour} {
		c, e := ResolveTTLInput(input, now, loc)
		if e != nil || c.Deadline == nil || !c.Deadline.Equal(now.Add(d)) || c.Source != ExpirationPreset {
			t.Fatalf("%s: %+v %v", input, c, e)
		}
	}
	c, e := ResolveTTLInput("Never", now, loc)
	if e != nil || c.Deadline != nil || c.Source != ExpirationNone {
		t.Fatal(c, e)
	}
	c, e = ResolveTTLInput("Today", now, loc)
	want := time.Date(2026, 10, 12, 23, 59, 59, 0, loc)
	if e != nil || !c.Deadline.Equal(want) {
		t.Fatal(c, e)
	}
	for _, input := range []string{"0m", "-1h", "999999999999999999d", "1h junk", "2026-10-12T11:59:59Z", "2026-02-30 12:00", "NaNh", "", "1e9h"} {
		if _, e := ResolveTTLInput(input, now, loc); e == nil {
			t.Errorf("accepted %q", input)
		}
	}
	for _, ns := range []int{0, 1, 999999999} {
		late := time.Date(2026, 10, 12, 23, 59, 59, ns, loc)
		if _, e := ResolveTTLInput("today", late, loc); e == nil {
			t.Errorf("accepted past/equal day end %v", late)
		}
	}
}
func TestQN029DSTCustomInput(t *testing.T) {
	loc, e := time.LoadLocation("America/Los_Angeles")
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, input := range []string{"2026-03-08 02:30", "2026-11-01 01:30"} {
		if _, e := ResolveTTLInput(input, now, loc); e == nil {
			t.Errorf("accepted DST gap/fold %s", input)
		}
	}
	for _, input := range []string{"2026-11-01T01:30:00-07:00", "2026-11-01T08:30:00Z"} {
		c, e := ResolveTTLInput(input, now, loc)
		if e != nil || !c.Deadline.Equal(time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC)) {
			t.Fatal(c, e)
		}
	}
	for _, date := range [][3]int{{2026, 3, 8}, {2026, 11, 1}} {
		start := time.Date(date[0], time.Month(date[1]), date[2], 0, 0, 0, 0, loc)
		c, e := ResolveTTLInput("today", start, loc)
		if e != nil || c.Deadline.Sub(start) == 24*time.Hour-time.Second || c.Deadline.In(loc).Hour() != 23 {
			t.Fatal(c, e)
		}
	}
	c, e := ResolveTTLInput("2026-03-08 03:30", now, loc)
	if e != nil || !c.Deadline.Equal(time.Date(2026, 3, 8, 10, 30, 0, 0, time.UTC)) {
		t.Fatal(c, e)
	}
}

func TestQN029StrictOffsetsAndDurationBounds(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, input := range []string{"2026-11-01T01:30:00+24:00", "2026-11-01T01:30:00+00:60", "2026-11-01T1:30:00Z", "2026-11-01T01:30:00,5Z"} {
		if _, e := ResolveTTLInput(input, now, time.UTC); e == nil {
			t.Errorf("non-RFC3339 accepted %q", input)
		}
	}
	c, e := ResolveTTLInput("1h30m", now, time.UTC)
	if e != nil || !c.Deadline.Equal(now.Add(90*time.Minute)) {
		t.Fatal(c, e)
	}
	for _, input := range []string{"9223372036854775808ns", "9223372036854775807.1ns"} {
		if _, e := ResolveTTLInput(input, now, time.UTC); e == nil {
			t.Fatal("overflow accepted", input)
		}
	}
	c, e = ResolveTTLInput("9223372036854775807ns", now, time.UTC)
	if e != nil || !c.Deadline.Equal(now.Add(time.Duration(1<<63-1))) {
		t.Fatal(c, e)
	}
}
