package main

import "time"

// Explicit helper makes the baseline UI tests independent of wall-clock
// assertions. Future model tests should inject a clock at the model boundary.
func nowForTest() time.Time {
    return time.Date(2026,time.October,11,12,0,0,0,time.UTC)
}
