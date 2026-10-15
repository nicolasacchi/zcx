package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Rome must resolve on minimal systems
)

// DefaultTimezone is the zone all human-facing times and business hours use.
const DefaultTimezone = "Europe/Rome"

// HoursConfig is the [hours] table of config.toml:
//
//	[hours]
//	timezone = "Europe/Rome"
//	mon = ["09:00-19:00"]
//	sat = ["09:00-13:00", "14:00-18:00"]   # several intervals per day are allowed
//	sun = []                               # explicitly closed
//
// When no weekday key is present at all the default Mon-Fri 09:00-19:00
// schedule applies. As soon as one weekday key is present only the listed
// days are open.
type HoursConfig struct {
	Timezone string   `toml:"timezone,omitempty"`
	Mon      []string `toml:"mon"`
	Tue      []string `toml:"tue"`
	Wed      []string `toml:"wed"`
	Thu      []string `toml:"thu"`
	Fri      []string `toml:"fri"`
	Sat      []string `toml:"sat"`
	Sun      []string `toml:"sun"`
	// Holidays lists Rome dates ("2026-11-01") treated as closed all day.
	// There is no built-in table: the shop decides which ones it observes.
	Holidays []string `toml:"holidays"`
}

// Interval is a half-open [Start, End) span in minutes since local midnight.
type Interval struct{ Start, End int }

// BusinessHours is a weekly schedule in a time zone. Days is indexed by
// time.Weekday (Sunday = 0).
type BusinessHours struct {
	Loc  *time.Location
	Days [7][]Interval
	// Holidays are closed all day ("2006-01-02" in Loc).
	Holidays map[string]bool
}

// DefaultHours is Mon-Fri 09:00-19:00 Europe/Rome.
func DefaultHours() *BusinessHours {
	loc, _ := time.LoadLocation(DefaultTimezone)
	b := &BusinessHours{Loc: loc}
	for d := time.Monday; d <= time.Friday; d++ {
		b.Days[d] = []Interval{{9 * 60, 19 * 60}}
	}
	return b
}

// ParseHours converts the TOML shape to a BusinessHours.
func ParseHours(h HoursConfig) (*BusinessHours, error) {
	tz := h.Timezone
	if tz == "" {
		tz = DefaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("hours.timezone %q: %w", tz, err)
	}
	perDay := [7][]string{time.Sunday: h.Sun, time.Monday: h.Mon, time.Tuesday: h.Tue,
		time.Wednesday: h.Wed, time.Thursday: h.Thu, time.Friday: h.Fri, time.Saturday: h.Sat}
	anyDay := false
	for _, d := range perDay {
		if d != nil {
			anyDay = true
		}
	}
	holidays := map[string]bool{}
	for _, d := range h.Holidays {
		if _, err := time.Parse("2006-01-02", strings.TrimSpace(d)); err != nil {
			return nil, fmt.Errorf("hours.holidays: bad date %q (want YYYY-MM-DD)", d)
		}
		holidays[strings.TrimSpace(d)] = true
	}
	if !anyDay {
		b := DefaultHours()
		b.Loc = loc
		b.Holidays = holidays
		return b, nil
	}
	b := &BusinessHours{Loc: loc, Holidays: holidays}
	names := [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	for d, specs := range perDay {
		for _, s := range specs {
			iv, err := parseInterval(s)
			if err != nil {
				return nil, fmt.Errorf("hours.%s: %w", names[d], err)
			}
			for _, prev := range b.Days[d] {
				if iv.Start < prev.End && prev.Start < iv.End {
					return nil, fmt.Errorf("hours.%s: overlapping intervals", names[d])
				}
			}
			b.Days[d] = append(b.Days[d], iv)
		}
	}
	return b, nil
}

func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		if strings.TrimSpace(s) == "24:00" {
			return 24 * 60, nil
		}
		return 0, fmt.Errorf("bad time %q (want HH:MM)", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

func parseInterval(s string) (Interval, error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return Interval{}, fmt.Errorf("bad interval %q (want HH:MM-HH:MM)", s)
	}
	start, err := parseClock(a)
	if err != nil {
		return Interval{}, err
	}
	end, err := parseClock(b)
	if err != nil {
		return Interval{}, err
	}
	if end <= start {
		return Interval{}, fmt.Errorf("interval %q must end after it starts (overnight spans are not supported)", s)
	}
	return Interval{start, end}, nil
}

// IsOpen reports whether the instant t falls inside business hours, evaluated
// in the schedule's time zone. Intervals are half-open: 19:00 is closed.
func (b *BusinessHours) IsOpen(t time.Time) bool {
	l := t.In(b.Loc)
	if b.Holidays[l.Format("2006-01-02")] {
		return false
	}
	m := l.Hour()*60 + l.Minute()
	for _, iv := range b.Days[l.Weekday()] {
		if m >= iv.Start && m < iv.End {
			return true
		}
	}
	return false
}

// Describe renders the schedule as {"mon": ["09:00-19:00"], ...} for display.
func (b *BusinessHours) Describe() map[string]any {
	names := []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	days := map[string]any{}
	for d, ivs := range b.Days {
		out := []string{}
		for _, iv := range ivs {
			out = append(out, fmt.Sprintf("%02d:%02d-%02d:%02d", iv.Start/60, iv.Start%60, iv.End/60, iv.End%60))
		}
		days[names[d]] = out
	}
	hol := []string{}
	for d := range b.Holidays {
		hol = append(hol, d)
	}
	sort.Strings(hol)
	return map[string]any{"timezone": b.Loc.String(), "schedule": days, "holidays": hol}
}
