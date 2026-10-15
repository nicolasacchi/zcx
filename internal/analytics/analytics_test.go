package analytics

import (
	"testing"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/timeparse"
)

func rome(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation("2006-01-02 15:04:05", s, timeparse.Rome)
	if err != nil {
		t.Fatal(err)
	}
	return tm.UTC()
}

func msg(id int64, at time.Time, role, text string, manual bool) client.Message {
	return client.Message{ID: id, CreatedAt: at, Role: role, Message: text, ManualReply: manual}
}

func ptr(t time.Time) *time.Time { return &t }
func sp(s string) *string        { return &s }

func approx(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestHoursEdges(t *testing.T) {
	// 2026-10-14 is a Wednesday, CEST.
	mk := func(id int64, at string) client.Conversation {
		return client.Conversation{ID: id, CreatedAt: rome(t, at)}
	}
	convs := []client.Conversation{
		mk(1, "2026-10-14 18:59:59"), // in
		mk(2, "2026-10-14 19:00:00"), // out
		mk(3, "2026-10-14 08:59:59"), // out
		mk(4, "2026-10-14 09:00:00"), // in
		mk(5, "2026-10-17 11:00:00"), // Saturday: out
	}
	st := Compute(convs, Options{})
	if st.StartedOutOfHours.Count != 3 {
		t.Fatalf("ooh = %d", st.StartedOutOfHours.Count)
	}
	approx(t, "share", st.StartedOutOfHours.Share, 0.6)
}

func TestHoursDST(t *testing.T) {
	// Last Sunday of Oct 2026 is the 25th: clocks go back. Monday 26th 18:30 Rome is
	// 17:30 UTC (CET) and must still be in hours; on 2026-10-23 18:30 is 16:30 UTC (CEST).
	cs := []client.Conversation{
		{ID: 1, CreatedAt: time.Date(2026, 10, 26, 17, 30, 0, 0, time.UTC)},
		{ID: 2, CreatedAt: time.Date(2026, 10, 23, 16, 30, 0, 0, time.UTC)},
		{ID: 3, CreatedAt: time.Date(2026, 10, 26, 18, 30, 0, 0, time.UTC)}, // 19:30 CET: out
	}
	st := Compute(cs, Options{})
	if st.StartedOutOfHours.Count != 1 {
		t.Fatalf("ooh = %d", st.StartedOutOfHours.Count)
	}
}

func TestComputeMetrics(t *testing.T) {
	base := rome(t, "2026-10-14 10:00:00")
	s := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }
	loopText := "Mi dispiace, non ho capito la tua richiesta, puoi riformulare?"
	c1 := client.Conversation{
		ID: 1, CreatedAt: base, StartLink: sp("https://shop.example/prodotto-x.html"),
		Messages: []client.Message{
			msg(1, s(0), "user", "ciao scrivo a mario@example.com ordine F123456789", false),
			msg(2, s(10), "assistant", loopText, false),
			msg(3, s(20), "user", "boh", false),
			msg(4, s(50), "assistant", "<p>"+loopText+"</p>", false),
		},
	}
	rate := "good"
	c1.Messages[1].Rate = &rate
	// single customer message, no email
	c2 := client.Conversation{
		ID: 2, CreatedAt: base, StartLink: sp("https://shop.example/"),
		Messages: []client.Message{
			msg(5, s(0), "user", "salve", false),
			msg(6, s(30), "assistant", "Buongiorno, come posso aiutarla oggi?", false),
		},
	}
	// in-hours escalation answered after 12.5 min
	esc := base.Add(5 * time.Minute)
	c3 := client.Conversation{
		ID: 3, CreatedAt: base, StartLink: sp("https://shop.example/checkout/order/success/1"),
		LastEscalatedAt: ptr(esc), ShareURL: "https://share.example/3",
		Messages: []client.Message{
			msg(7, s(0), "user", "voglio un operatore", false),
			msg(8, s(20), "assistant", "Ti passo a un operatore, ti risponderà a breve.", false),
			msg(9, esc.Add(750*time.Second), "assistant", "Buongiorno, sono Anna", true),
		},
	}
	// out-of-hours escalation (Wed 21:00), bot promised, still waiting
	night := rome(t, "2026-10-14 21:00:00")
	c4 := client.Conversation{
		ID: 4, CreatedAt: night, StartLink: sp("https://shop.example/cart"),
		LastEscalatedAt: ptr(night.Add(time.Minute)),
		Messages: []client.Message{
			msg(10, night, "user", "problema", false),
			msg(11, night.Add(30*time.Second), "assistant", "Un nostro operatore ti contatterà domani mattina.", false),
		},
	}
	// out-of-hours escalation answered next morning, no promise
	night2 := rome(t, "2026-10-14 22:00:00")
	c5 := client.Conversation{
		ID: 5, CreatedAt: night2, StartLink: sp("https://shop.example/blog/x"),
		LastEscalatedAt: ptr(night2.Add(time.Minute)),
		Messages: []client.Message{
			msg(12, night2, "user", "aiuto", false),
			msg(13, night2.Add(20*time.Second), "assistant", "Certo.", false),
			msg(14, rome(t, "2026-10-15 09:30:00"), "assistant", "Buongiorno", true),
		},
	}
	st := Compute([]client.Conversation{c1, c2, c3, c4, c5}, Options{})
	if st.Conversations != 5 || st.CustomerMessages != 6 || st.OperatorMessages != 2 || st.BotMessages != 6 {
		t.Errorf("counts %+v", st)
	}
	if st.WithOperatorMessages.Count != 2 {
		t.Errorf("with operator %d", st.WithOperatorMessages.Count)
	}
	if st.SingleCustomerMessage.Count != 4 {
		t.Errorf("single %d", st.SingleCustomerMessage.Count)
	}
	if st.CustomerTypedEmail.Count != 1 || st.CustomerTypedOrder.Count != 1 {
		t.Errorf("email/order %+v %+v", st.CustomerTypedEmail, st.CustomerTypedOrder)
	}
	if st.BotLoops.Count != 1 {
		t.Errorf("loops %d", st.BotLoops.Count)
	}
	if st.RatedBotMessages.Count != 1 {
		t.Errorf("rated %d", st.RatedBotMessages.Count)
	}
	approx(t, "rating share", st.RatedBotMessages.Share, round4(1.0/6))
	if st.StartedOutOfHours.Count != 2 {
		t.Errorf("ooh %d", st.StartedOutOfHours.Count)
	}
	if st.Escalations != 3 || st.EscalationsInHours != 1 || st.EscalationsOutOfHours != 2 {
		t.Errorf("esc %d %d %d", st.Escalations, st.EscalationsInHours, st.EscalationsOutOfHours)
	}
	if st.InHoursWithHumanReply.Count != 1 {
		t.Errorf("in-hours replied %d", st.InHoursWithHumanReply.Count)
	}
	approx(t, "ih p50", st.InHoursFirstReplyMin.P50, 12.5)
	if st.OOHBotPromisedAgent.Count != 1 || st.OOHAnswered != 1 || st.OOHWaiting != 1 {
		t.Errorf("ooh promised=%d answered=%d waiting=%d", st.OOHBotPromisedAgent.Count, st.OOHAnswered, st.OOHWaiting)
	}
	// latencies sorted: 10,20,20,30,30,30 -> p50 = index 3 = 30
	if st.BotLatencySeconds.N != 6 {
		t.Errorf("lat n %d", st.BotLatencySeconds.N)
	}
	approx(t, "lat p50", st.BotLatencySeconds.P50, 30)
	want := map[string]int{"pdp": 1, "home": 1, "thank-you": 1, "cart": 1, "blog": 1}
	for k, v := range want {
		if st.StartPages[k] != v {
			t.Errorf("start page %s = %d", k, st.StartPages[k])
		}
	}
}

func TestAsOfCutoff(t *testing.T) {
	night := rome(t, "2026-10-14 21:00:00")
	esc := night.Add(time.Minute)
	reply := rome(t, "2026-10-15 09:30:00")
	c := client.Conversation{
		ID: 9, CreatedAt: night, LastEscalatedAt: ptr(esc), LastEscalationResolvedAt: ptr(reply.Add(time.Hour)),
		Messages: []client.Message{
			msg(1, night, "user", "aiuto", false),
			msg(2, reply, "assistant", "Buongiorno", true),
		},
	}
	// Snapshot before the reply: waiting, no operator message counted.
	asOf := rome(t, "2026-10-15 08:00:00")
	st := Compute([]client.Conversation{c}, Options{AsOf: asOf})
	if st.OperatorMessages != 0 || st.OOHWaiting != 1 || st.OOHAnswered != 0 {
		t.Errorf("as-of before reply: %+v", st)
	}
	rows := Escalations([]client.Conversation{c}, Options{AsOf: asOf})
	if len(rows) != 1 || !rows[0].Waiting || rows[0].Resolved {
		t.Fatalf("rows %+v", rows)
	}
	approx(t, "waiting", rows[0].MinutesWaiting, round1(asOf.Sub(esc).Minutes()))
	// Snapshot before the escalation itself: escalation absent.
	st = Compute([]client.Conversation{c}, Options{AsOf: night.Add(30 * time.Second)})
	if st.Escalations != 0 {
		t.Errorf("escalation should be absent: %d", st.Escalations)
	}
	// Conversation created after as-of is excluded.
	st = Compute([]client.Conversation{c}, Options{AsOf: night.Add(-time.Hour)})
	if st.Conversations != 0 {
		t.Errorf("conversations %d", st.Conversations)
	}
	// After the reply and resolution: answered, resolved.
	rows = Escalations([]client.Conversation{c}, Options{AsOf: reply.Add(2 * time.Hour)})
	if rows[0].Waiting || !rows[0].Resolved || !rows[0].OperatorReplied {
		t.Errorf("late rows %+v", rows[0])
	}
	approx(t, "delay", rows[0].FirstReplyDelayMin, round1(reply.Sub(esc).Minutes()))
	// View must not mutate the input.
	if len(c.Messages) != 2 || c.LastEscalatedAt == nil {
		t.Error("input mutated")
	}
}

func TestEscalationBoundaryAndOperatorBefore(t *testing.T) {
	in := rome(t, "2026-10-14 18:59:00")
	out := rome(t, "2026-10-14 19:00:00")
	// Operator message BEFORE escalation must not count as a reply.
	c := client.Conversation{ID: 1, CreatedAt: in.Add(-time.Hour), LastEscalatedAt: ptr(in),
		Messages: []client.Message{msg(1, in.Add(-time.Minute), "assistant", "ciao", true)}}
	e, _ := AnalyzeEscalation(c, Options{Now: in.Add(10 * time.Minute)})
	if !e.InHours || e.OperatorReplied || !e.Waiting {
		t.Errorf("%+v", e)
	}
	approx(t, "waiting", e.MinutesWaiting, 10)
	e2, _ := AnalyzeEscalation(client.Conversation{ID: 2, LastEscalatedAt: ptr(out)}, Options{})
	if e2.InHours {
		t.Error("19:00 must be out of hours")
	}
	if _, ok := AnalyzeEscalation(client.Conversation{ID: 3}, Options{}); ok {
		t.Error("no escalation expected")
	}
}

func TestClassifyStartPage(t *testing.T) {
	cases := map[string]string{
		"": "other", "https://x.example": "home", "https://x.example/a-b.html": "pdp",
		"https://x.example/cart": "cart", "https://x.example/checkout": "checkout",
		"https://x.example/checkout/order/success/9": "thank-you", "https://x.example/account/orders": "account",
		"https://x.example/cerca?q=a": "search", "https://x.example/contatti": "help-pages",
		"https://x.example/care-plan": "care-plan", "https://x.example/rimedi-per-tosse": "blog",
		"https://x.example/categoria": "other",
	}
	for in, want := range cases {
		if got := ClassifyStartPage(in); got != want {
			t.Errorf("%q = %s want %s", in, got, want)
		}
	}
}

func TestEmptyInput(t *testing.T) {
	st := Compute(nil, Options{})
	if st.Conversations != 0 || st.StartedOutOfHours.Share != nil || st.BotLatencySeconds.P50 != nil {
		t.Errorf("%+v", st)
	}
}

func TestPromiseRegexFalsePositivesAndClosure(t *testing.T) {
	night := rome(t, "2026-10-14 21:00:00")
	esc := night.Add(time.Minute)
	mk := func(text string, at time.Time) Escalation {
		c := client.Conversation{ID: 1, CreatedAt: night, LastEscalatedAt: ptr(esc),
			Messages: []client.Message{msg(1, at, "assistant", text, false)}}
		e, _ := AnalyzeEscalation(c, Options{})
		return e
	}
	for _, txt := range []string{"Ecco il collegamento al seguente passo.", "Guarda su steam, prenderà tempo", "Un team di esperti sceglie i prodotti."} {
		if mk(txt, night).BotPromisedAgent {
			t.Errorf("false positive: %q", txt)
		}
	}
	if !mk("Un operatore ti risponderà a breve.", night).BotPromisedAgent {
		t.Error("real promise missed")
	}
	closed := mk("Un operatore risponderà alla riapertura, lunedì.", night)
	if closed.BotPromisedAgent || !closed.BotStatedReopening {
		t.Errorf("closure disclaimer: %+v", closed)
	}
	// A promise made long before the escalation is not the handoff message.
	if mk("Un operatore ti risponderà a breve.", esc.Add(-time.Hour)).BotPromisedAgent {
		t.Error("old message must not count")
	}
}

func TestFirstReplyMedianEvenN(t *testing.T) {
	d := dist([]float64{10, 20, 30, 40}, true)
	approx(t, "p50", d.P50, 25)
	d = dist([]float64{10, 20, 30, 40}, false)
	approx(t, "upper p50", d.P50, 30)
	d = dist([]float64{10, 20, 30}, true)
	approx(t, "odd p50", d.P50, 20)
}

func TestOOHResolvedWithoutReplyNotWaiting(t *testing.T) {
	night := rome(t, "2026-10-14 21:00:00")
	esc := night.Add(time.Minute)
	c := client.Conversation{ID: 1, CreatedAt: night, LastEscalatedAt: ptr(esc), LastEscalationResolvedAt: ptr(esc.Add(time.Hour)),
		Messages: []client.Message{msg(1, night, "user", "aiuto", false)}}
	st := Compute([]client.Conversation{c}, Options{})
	if st.OOHWaiting != 0 || st.OOHResolvedNoReply != 1 {
		t.Errorf("%+v", st)
	}
	rows := Escalations([]client.Conversation{c}, Options{})
	if rows[0].Waiting {
		t.Error("escalations disagrees")
	}
}

func TestAsOfReEscalationIsFlagged(t *testing.T) {
	t1 := rome(t, "2026-10-13 10:00:00")
	t2 := rome(t, "2026-10-15 10:00:00")
	c := client.Conversation{ID: 1, CreatedAt: t1.Add(-time.Minute), LastEscalatedAt: ptr(t2),
		Messages: []client.Message{
			msg(1, t1.Add(-30*time.Second), "user", "aiuto", false),
			msg(2, t1.Add(5*time.Minute), "assistant", "ciao", true),
		}}
	st := Compute([]client.Conversation{c}, Options{AsOf: rome(t, "2026-10-14 00:00:00")})
	if st.Escalations != 0 || st.EscalationsUnknownAsOf != 1 || st.AsOfCaveat == "" {
		t.Errorf("%+v", st)
	}
	if st := Compute([]client.Conversation{c}, Options{}); st.AsOfCaveat != "" || st.EscalationsUnknownAsOf != 0 {
		t.Errorf("no as-of must have no caveat: %+v", st)
	}
}
