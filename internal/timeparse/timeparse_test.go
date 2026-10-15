package timeparse

import (
	"testing"
	"time"
)

var now = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"now", now},
		{"7d", now.AddDate(0, 0, -7)},
		{"24h", now.Add(-24 * time.Hour)},
		{"2w", now.AddDate(0, 0, -14)},
		{"2026-10-14T10:00:00Z", time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)},
		{"2026-10-14", time.Date(2026, 10, 13, 22, 0, 0, 0, time.UTC)},       // Rome midnight, CEST
		{"2026-10-14T09:30", time.Date(2026, 10, 14, 7, 30, 0, 0, time.UTC)}, // Rome local
		{"2026-12-01T09:30", time.Date(2026, 12, 1, 8, 30, 0, 0, time.UTC)},  // CET
	}
	for _, c := range cases {
		got, err := Parse(c.in, now)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("%s: got %v err %v want %v", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "yesterday-ish", "7x", "2026-13-01"} {
		if _, err := Parse(bad, now); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestParseRange(t *testing.T) {
	f, to, err := ParseRange("2026-10-01", "2026-10-02", now)
	if err != nil {
		t.Fatal(err)
	}
	// bare --to date includes the whole day: bound is the next Rome midnight.
	if !to.Equal(time.Date(2026, 10, 2, 22, 0, 0, 0, time.UTC)) || !f.Equal(time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)) {
		t.Errorf("f=%v to=%v", f, to)
	}
	f, to, err = ParseRange("", "", now)
	if err != nil || f != nil || to != nil {
		t.Errorf("empty range: %v %v %v", f, to, err)
	}
	if _, _, err := ParseRange("2026-10-05", "2026-10-01", now); err == nil {
		t.Error("from after to must fail")
	}
}

func TestFormat(t *testing.T) {
	u := time.Date(2026, 10, 15, 8, 5, 9, 0, time.UTC)
	if FormatRome(u) != "2026-10-15 10:05" || FormatRomeSec(u) != "2026-10-15 10:05:09" {
		t.Errorf("%s %s", FormatRome(u), FormatRomeSec(u))
	}
	if FormatRomeRFC3339(u) != "2026-10-15T10:05:09+02:00" || FormatUTC(u) != "2026-10-15T08:05:09Z" {
		t.Error("rfc3339 formats")
	}
	if FormatRomePtr(nil) != "" {
		t.Error("nil ptr")
	}
}

func TestRelativeDaysAreRomeCalendarDays(t *testing.T) {
	// Monday 2026-10-26 09:00 Rome (CET), one day after the autumn DST change.
	n := time.Date(2026, 10, 26, 9, 0, 0, 0, Rome)
	got, err := Parse("7d", n)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 19, 9, 0, 0, 0, Rome); !got.Equal(want) {
		t.Errorf("7d: got %v want %v", got.In(Rome), want)
	}
	got, _ = Parse("1w", n)
	if want := time.Date(2026, 10, 19, 9, 0, 0, 0, Rome); !got.Equal(want) {
		t.Errorf("1w: got %v want %v", got.In(Rome), want)
	}
	got, _ = Parse("24h", n) // hours stay elapsed time
	if !got.Equal(n.Add(-24 * time.Hour)) {
		t.Errorf("24h: %v", got)
	}
}

func TestAmbiguousAndNonexistentLocalTimes(t *testing.T) {
	for _, in := range []string{"2026-10-25T02:30", "2026-03-29T02:30"} {
		if _, err := Parse(in, now); err == nil {
			t.Errorf("%s should be rejected", in)
		}
	}
	if _, err := Parse("2026-10-25T03:30", now); err != nil {
		t.Errorf("unambiguous time rejected: %v", err)
	}
}

func TestParseEndBareDateIsNextMidnight(t *testing.T) {
	got, err := ParseEnd("2026-10-14", now)
	if err != nil || !got.Equal(time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC)) {
		t.Errorf("%v %v", got, err)
	}
	got, _ = ParseEnd("2026-10-14T09:30", now)
	if !got.Equal(time.Date(2026, 10, 14, 7, 30, 0, 0, time.UTC)) {
		t.Errorf("%v", got)
	}
}
