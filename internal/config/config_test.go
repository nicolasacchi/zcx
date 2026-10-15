package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	for _, k := range []string{EnvToken, EnvChatID, EnvBaseURL} {
		t.Setenv(k, "")
	}
	return home
}

func TestParseEnvFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	writeFile(t, p, "# c\n\nA=1\nexport B=\"two words\"\nC='x'\nD=val # trailing\nbad line\n")
	got, err := ParseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "1", "B": "two words", "C": "x", "D": "val"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if m, err := ParseEnvFile(filepath.Join(t.TempDir(), "missing")); m != nil || err != nil {
		t.Errorf("missing file: %v %v", m, err)
	}
}

func TestTokenPrecedence(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, ".config", "zcx", "config.toml"), "token = \"zc-fromtoml\"\nchat_id = 7\n")
	r, err := Load(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Token != "zc-fromtoml" || r.ChatID != "7" || r.ChatSource != "file:"+r.ConfigPath {
		t.Fatalf("toml: %+v", r)
	}
	writeFile(t, filepath.Join(home, ".config", "zipchat", "env"), "ZIPCHAT_API_TOKEN=zcu-fromfile\n")
	r, _ = Load(Options{})
	if r.Token != "zcu-fromfile" || r.TokenSource != "file:"+r.EnvPath {
		t.Fatalf("envfile: %+v", r)
	}
	t.Setenv(EnvToken, "zcu-fromenv")
	t.Setenv(EnvChatID, "9")
	r, _ = Load(Options{})
	if r.Token != "zcu-fromenv" || r.TokenSource != "env:"+EnvToken || r.ChatID != "9" {
		t.Fatalf("env: %+v", r)
	}
	r, _ = Load(Options{TokenFlag: "zc-flag", ChatFlag: "1"})
	if r.Token != "zc-flag" || r.TokenSource != SourceFlag || r.ChatID != "1" || r.ChatSource != SourceFlag {
		t.Fatalf("flag: %+v", r)
	}
	if got := r.MaskedToken(); got == "zc-flag" || got == "" {
		t.Errorf("mask: %q", got)
	}
}

func TestDefaultsAndDataDir(t *testing.T) {
	home := isolate(t)
	r, err := Load(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.TokenSource != SourceNone || r.BaseURL != DefaultBaseURL || r.BaseSource != "default" {
		t.Fatalf("%+v", r)
	}
	if r.DataDir != filepath.Join(home, ".local", "share", "zcx") {
		t.Errorf("data dir %s", r.DataDir)
	}
	t.Setenv("XDG_DATA_HOME", "/x/y")
	t.Setenv(EnvBaseURL, "http://localhost:1/")
	r, _ = Load(Options{})
	if r.DataDir != "/x/y/zcx" || r.BaseURL != "http://localhost:1" {
		t.Errorf("%+v", r)
	}
}

func rome(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04", s, DefaultHours().Loc)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestDefaultHours(t *testing.T) {
	b := DefaultHours()
	cases := map[string]bool{
		"2026-10-14 09:00": true, // Wed
		"2026-10-14 18:59": true,
		"2026-10-14 19:00": false, // half-open
		"2026-10-14 08:59": false,
		"2026-10-17 10:00": false, // Sat
		"2026-10-18 10:00": false, // Sun
	}
	for in, want := range cases {
		if got := b.IsOpen(rome(t, in)); got != want {
			t.Errorf("%s: %v want %v", in, got, want)
		}
	}
	// 08:30 UTC in October (CEST, +2) is 10:30 Rome: open.
	if !b.IsOpen(time.Date(2026, 10, 14, 8, 30, 0, 0, time.UTC)) {
		t.Error("UTC instant not converted to Rome")
	}
}

func TestCustomHoursMultipleIntervals(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, ".config", "zcx", "config.toml"), `
[hours]
timezone = "Europe/Rome"
mon = ["09:00-19:00"]
sat = ["09:00-13:00", "14:00-18:00"]
sun = []
`)
	r, err := Load(Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := r.Hours
	if !h.IsOpen(rome(t, "2026-10-17 10:00")) || h.IsOpen(rome(t, "2026-10-17 13:30")) || !h.IsOpen(rome(t, "2026-10-17 14:00")) {
		t.Error("saturday intervals wrong")
	}
	if h.IsOpen(rome(t, "2026-10-14 10:00")) { // Wed not listed => closed
		t.Error("unlisted weekday must be closed once any day is configured")
	}
	if h.IsOpen(rome(t, "2026-10-18 10:00")) {
		t.Error("sunday closed")
	}
}

func TestParseHoursErrors(t *testing.T) {
	for _, h := range []HoursConfig{
		{Mon: []string{"9-19"}},
		{Mon: []string{"19:00-09:00"}},
		{Mon: []string{"09:00-12:00", "11:00-14:00"}},
		{Timezone: "Mars/Olympus", Mon: []string{"09:00-10:00"}},
	} {
		if _, err := ParseHours(h); err == nil {
			t.Errorf("expected error for %+v", h)
		}
	}
}

func TestMalformedTOML(t *testing.T) {
	home := isolate(t)
	writeFile(t, filepath.Join(home, ".config", "zcx", "config.toml"), "token = [")
	if _, err := Load(Options{}); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestEnvFileQuotedValueWithTrailingComment(t *testing.T) {
	p := filepath.Join(t.TempDir(), "env")
	writeFile(t, p, "A=\"zcu-abc\" # my token\nB='x y'   # c\nC=plain # c\nD=\"unterminated\n")
	got, err := ParseEnvFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "zcu-abc", "B": "x y", "C": "plain", "D": "\"unterminated"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestSourcesAreTrimmed(t *testing.T) {
	isolate(t)
	t.Setenv(EnvToken, "zcu-env\n")
	t.Setenv(EnvChatID, " 21356 ")
	r, err := Load(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Token != "zcu-env" || r.ChatID != "21356" {
		t.Fatalf("%q %q", r.Token, r.ChatID)
	}
	r, _ = Load(Options{TokenFlag: " zc-flag\n", ChatFlag: "5 "})
	if r.Token != "zc-flag" || r.ChatID != "5" {
		t.Fatalf("%q %q", r.Token, r.ChatID)
	}
}

func TestHolidaysAreClosed(t *testing.T) {
	b, err := ParseHours(HoursConfig{Holidays: []string{"2026-11-01", "2026-12-08"}})
	if err != nil {
		t.Fatal(err)
	}
	if b.IsOpen(rome(t, "2026-12-08 10:00")) { // Tuesday, holiday
		t.Error("holiday must be closed")
	}
	if !b.IsOpen(rome(t, "2026-12-09 10:00")) {
		t.Error("next day is open")
	}
	if _, err := ParseHours(HoursConfig{Holidays: []string{"1 Nov"}}); err == nil {
		t.Error("bad holiday date must fail")
	}
	// holidays combine with an explicit weekly schedule
	b, _ = ParseHours(HoursConfig{Mon: []string{"09:00-19:00"}, Holidays: []string{"2026-12-07"}})
	if b.IsOpen(rome(t, "2026-12-07 10:00")) {
		t.Error("Monday holiday must be closed")
	}
}
