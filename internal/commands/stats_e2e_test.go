package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/tidwall/gjson"
)

func statsSeed(t *testing.T) func(e *Env) error {
	t.Helper()
	rome, _ := time.LoadLocation("Europe/Rome")
	night := time.Date(2026, 10, 14, 21, 0, 0, 0, rome).UTC()
	day := time.Date(2026, 10, 14, 10, 0, 0, 0, rome).UTC()
	esc := night.Add(time.Minute)
	dayEsc := day.Add(2 * time.Minute)
	prod := "https://www.1000farmacie.it/"
	convs := []client.Conversation{
		{ID: 1, StartLink: &prod, CreatedAt: night, ShareURL: "https://share.example/1", LastEscalatedAt: &esc,
			Messages: []client.Message{
				{ID: 1, CreatedAt: night, Role: "user", Message: "serve aiuto"},
				{ID: 2, CreatedAt: night.Add(20 * time.Second), Role: "assistant", Message: "Un operatore ti risponderà domani."},
			}},
		{ID: 2, StartLink: &prod, CreatedAt: day, ShareURL: "https://share.example/2", LastEscalatedAt: &dayEsc,
			Messages: []client.Message{
				{ID: 3, CreatedAt: day, Role: "user", Message: "ordine F000000001"},
				{ID: 4, CreatedAt: dayEsc.Add(4 * time.Minute), Role: "assistant", Message: "ciao", ManualReply: true},
			}},
		{ID: 3, StartLink: &prod, CreatedAt: day, Messages: []client.Message{{ID: 5, CreatedAt: day, Role: "user", Message: "ciao"}}},
	}
	return func(e *Env) error {
		st, err := e.Store()
		if err != nil {
			return err
		}
		for i := range convs {
			if err := st.SaveConversation(&convs[i]); err != nil {
				return err
			}
		}
		return nil
	}
}

func TestStatsE2E(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	old := analyticsRefresh
	analyticsRefresh = statsSeed(t)
	defer func() { analyticsRefresh = old }()

	_, out, _, err := runZcx(t, nil, "stats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]int64{
		"conversations": 3, "escalations": 2, "escalations_in_hours": 1, "escalations_out_of_hours": 1,
		"out_of_hours_escalations_waiting": 1, "customer_typed_order_number.count": 1,
		"out_of_hours_escalations_bot_promised_agent.count": 1,
	} {
		if got := gjson.Get(out, path).Int(); got != want {
			t.Errorf("%s = %d want %d\n%s", path, got, want, out)
		}
	}
	if got := gjson.Get(out, "in_hours_first_reply_minutes.p50").Float(); got != 4 {
		t.Errorf("p50 %v", got)
	}

	// as-of before the daytime operator reply: that escalation is waiting.
	_, out, _, err = runZcx(t, nil, "stats", "--json", "--as-of", "2026-10-14T10:03")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(out, "operator_messages").Int() != 0 || gjson.Get(out, "conversations").Int() != 2 {
		t.Errorf("as-of: %s", out)
	}
}

func TestEscalationsE2E(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	old := analyticsRefresh
	analyticsRefresh = statsSeed(t)
	defer func() { analyticsRefresh = old }()

	_, out, _, err := runZcx(t, nil, "escalations", "--json", "--as-of", "2026-10-15T08:00")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(out, "#").Int() != 2 || gjson.Get(out, "0.id").Int() != 2 || gjson.Get(out, "0.first_reply_delay_min").Float() != 4 {
		t.Errorf("rows: %s", out)
	}
	if gjson.Get(out, "1.escalated_at").String() != "2026-10-14 21:01" || gjson.Get(out, "1.in_hours").Bool() {
		t.Errorf("row1: %s", out)
	}
	_, out, _, err = runZcx(t, nil, "escalations", "--json", "--waiting", "--as-of", "2026-10-15T08:00")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(out, "#").Int() != 1 || gjson.Get(out, "0.id").Int() != 1 || gjson.Get(out, "0.share_url").String() == "" {
		t.Errorf("waiting: %s", out)
	}
}

func TestStatsHostFilterAndLive(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	local := "http://localhost:3000/"
	prod := "https://www.example-shop.test/"
	old := analyticsRefresh
	defer func() { analyticsRefresh = old }()
	analyticsRefresh = func(e *Env) error {
		st, err := e.Store()
		if err != nil {
			return err
		}
		pre := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
		post := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
		for _, c := range []client.Conversation{
			{ID: 1, StartLink: &local, CreatedAt: post},
			{ID: 2, CreatedAt: post}, // URL-less test traffic
			{ID: 3, CurrentURL: &prod, CreatedAt: pre},
			{ID: 4, StartLink: &prod, CreatedAt: post},
		} {
			c := c
			if err := st.SaveConversation(&c); err != nil {
				return err
			}
		}
		return nil
	}
	count := func(args ...string) int64 {
		_, out, _, err := runZcx(t, nil, append([]string{"stats", "--json"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return gjson.Get(out, "conversations").Int()
	}
	// No [scope] configured: every conversation counts and --live is refused.
	if n := count(); n != 4 {
		t.Errorf("no scope: %d", n)
	}
	if _, _, _, err := runZcx(t, nil, "stats", "--json", "--live"); err == nil {
		t.Error("--live without [scope] live_since must fail")
	}

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "zcx"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "[scope]\nhost = \"www.example-shop.test\"\nlive_since = \"2026-10-14T08:30:00Z\"\n"
	if err := os.WriteFile(filepath.Join(home, ".config", "zcx", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZCX_TEST_HOME", home)
	if n := count(); n != 2 {
		t.Errorf("config host filter: %d", n)
	}
	if n := count("--live"); n != 1 {
		t.Errorf("--live: %d", n)
	}
	if n := count("--host", "*"); n != 4 {
		t.Errorf("--host '*': %d", n)
	}
	if n := count("--host", "localhost:3000"); n != 1 {
		t.Errorf("--host localhost:3000: %d", n)
	}
}

func TestStatsSurvivesFailedPreSync(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	old := analyticsRefresh
	defer func() { analyticsRefresh = old }()
	seed := statsSeed(t)
	// each runZcx has a fresh data dir: seed a cache, then fail like a 429.
	analyticsRefresh = func(e *Env) error {
		if err := seed(e); err != nil {
			return err
		}
		return errors.New("status 429 after 5 retries")
	}
	_, out, errOut, err := runZcx(t, nil, "stats", "--json")
	if err != nil || gjson.Get(out, "conversations").Int() != 3 || !strings.Contains(errOut, "may be stale") {
		t.Fatalf("soft failure: %v %s %s", err, out, errOut)
	}
	if _, _, _, err := runZcx(t, nil, "stats", "--json", "--strict-sync"); err == nil {
		t.Fatal("--strict-sync must fail")
	}
}

func TestStatsBareAsOfIsEndOfDay(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	old := analyticsRefresh
	analyticsRefresh = statsSeed(t)
	defer func() { analyticsRefresh = old }()
	_, out, _, err := runZcx(t, nil, "stats", "--json", "--as-of", "2026-10-14")
	if err != nil || gjson.Get(out, "conversations").Int() != 3 || gjson.Get(out, "as_of_caveat").String() == "" {
		t.Fatalf("%v %s", err, out)
	}
}
