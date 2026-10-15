// Package timeparse parses --from/--to/--as-of style flags and formats times
// for humans. Humans think in Europe/Rome; the API speaks UTC.
package timeparse

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Rome must resolve on minimal systems
)

// Rome is the zone for all human-facing times.
var Rome = mustLoad("Europe/Rome")

func mustLoad(name string) *time.Location {
	l, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return l
}

// Parse converts a flag value into an instant. Accepted forms:
//
//	"now"
//	relative "7d", "2w", "24h", "30m" (that long BEFORE now)
//	RFC3339 ("2026-10-15T10:00:00Z", with offset)
//	Rome-local "2006-01-02T15:04" and "2006-01-02" (midnight Rome)
func Parse(s string, now time.Time) (time.Time, error) {
	t, _, err := parse(s, now)
	return t, err
}

// parse also reports whether the input was a bare date.
func parse(s string, now time.Time) (time.Time, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false, fmt.Errorf("empty time")
	}
	if strings.EqualFold(s, "now") {
		return now, false, nil
	}
	if n, unit, ok := parseRelative(s); ok {
		switch unit {
		case 'd': // calendar days in Rome, so DST changes do not shift the wall clock
			return now.In(Rome).AddDate(0, 0, -n), false, nil
		case 'w':
			return now.In(Rome).AddDate(0, 0, -7*n), false, nil
		case 'h':
			return now.Add(-time.Duration(n) * time.Hour), false, nil
		default:
			return now.Add(-time.Duration(n) * time.Minute), false, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, false, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04", s, Rome); err == nil {
		if t.Format("2006-01-02T15:04") != s {
			return time.Time{}, false, fmt.Errorf("%q does not exist in Europe/Rome (DST gap); use another time or an RFC3339 offset", s)
		}
		if t.Add(-time.Hour).Format("2006-01-02T15:04") == s || t.Add(time.Hour).Format("2006-01-02T15:04") == s {
			return time.Time{}, false, fmt.Errorf("%q is ambiguous in Europe/Rome (DST overlap); use RFC3339 with an offset, e.g. %s+02:00", s, s+":00")
		}
		return t, false, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, Rome); err == nil {
		return t, true, nil
	}
	return time.Time{}, false, fmt.Errorf("cannot parse time %q (use RFC3339, 2006-01-02, 2006-01-02T15:04 Rome-local, or 7d/24h)", s)
}

// parseRelative splits "7d" into (7, 'd').
func parseRelative(s string) (int, byte, bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, 0, false
	}
	switch u := s[len(s)-1]; u {
	case 'd', 'w', 'h', 'm':
		return n, u, true
	}
	return 0, 0, false
}

// ParseEnd parses a window end or snapshot instant, exclusive: a bare date
// ("2026-10-14") means the end of that whole Rome day (the next midnight);
// every other form is an exact instant.
func ParseEnd(s string, now time.Time) (time.Time, error) {
	v, bare, err := parse(s, now)
	if err != nil {
		return time.Time{}, err
	}
	if bare {
		v = v.AddDate(0, 0, 1)
	}
	return v, nil
}

// ParseRange parses a half-open window [from, to). An empty flag yields a nil
// bound. A bare-date --to ("2026-10-15") is treated as inclusive of that whole
// Rome day, i.e. the returned bound is the NEXT midnight; every other form is
// an exact instant. It rejects from >= to.
func ParseRange(from, to string, now time.Time) (*time.Time, *time.Time, error) {
	var f, t *time.Time
	if strings.TrimSpace(from) != "" {
		v, err := Parse(from, now)
		if err != nil {
			return nil, nil, fmt.Errorf("--from: %w", err)
		}
		f = &v
	}
	if strings.TrimSpace(to) != "" {
		v, err := ParseEnd(to, now)
		if err != nil {
			return nil, nil, fmt.Errorf("--to: %w", err)
		}
		t = &v
	}
	if f != nil && t != nil && !f.Before(*t) {
		return nil, nil, fmt.Errorf("--from must be before --to")
	}
	return f, t, nil
}

// InRome converts t to Europe/Rome.
func InRome(t time.Time) time.Time { return t.In(Rome) }

// FormatRome renders "2006-01-02 15:04" in Rome time.
func FormatRome(t time.Time) string { return t.In(Rome).Format("2006-01-02 15:04") }

// FormatRomeSec renders "2006-01-02 15:04:05" in Rome time.
func FormatRomeSec(t time.Time) string { return t.In(Rome).Format("2006-01-02 15:04:05") }

// FormatRomeRFC3339 renders RFC3339 with the Rome offset.
func FormatRomeRFC3339(t time.Time) string { return t.In(Rome).Format(time.RFC3339) }

// FormatUTC renders RFC3339 in UTC (API format).
func FormatUTC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// FormatRomePtr is FormatRome for a nullable time ("" when nil).
func FormatRomePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return FormatRome(*t)
}
