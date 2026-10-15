// Package analytics computes the go-live dossier KPIs from cached conversations.
//
// Definitions (kept identical to the reference analysis scripts):
//   - customer message: role "user"
//   - operator message: role "assistant" with manual_reply=true
//   - bot message:      role "assistant" with manual_reply=false
//   - escalation:       conversation with last_escalated_at set (only the last
//     escalation is known); "resolved" = last_escalation_resolved_at >= it
//   - operator reply after escalation: operator message with created_at >= escalated_at
//   - bot reply latency: customer message immediately followed by a bot message
//   - in hours: the instant falls inside the configured business hours
package analytics

import (
	"html"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/config"
)

var (
	emailRe = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.]+`)
	orderRe = regexp.MustCompile(`\bF\d{9}\b`)
	tagRe   = regexp.MustCompile(`<[^>]+>`)
	spaceRe = regexp.MustCompile(`\s+`)
	// promiseRe detects a bot message promising that a human will follow up.
	// Word-anchored so "collegamento" or "seguente" do not match.
	promiseRe = regexp.MustCompile(`(?is)\b(operator\w*|collega|collegh[ei]|team|assistenza clienti|agente)\b[^.]{0,80}\b(rispond\w*|contatt\w*|ricontatt\w*|scriv\w*|aiut\w*|segu(?:ir|it)\w*|prender\w*)|\b(rispond|contatt|ricontatt)\w*[^.]{0,60}\b(operator\w*|collega|team)\b`)
	// reopenRe detects a message that tells the customer the shop is closed
	// and when the reply will come ("alla riapertura", "lunedì", ...).
	reopenRe = regexp.MustCompile(`(?i)\b(riapertur\w*|riapr\w*|luned\w*|fuori orario|orari\w* di (?:apertura|servizio)|chius\w+)\b`)
)

// promiseTolerance is how long before the escalation flag a bot message still
// counts as the handoff message (the flag is set within seconds of it).
const promiseTolerance = 5 * time.Minute

// Options configure a computation.
type Options struct {
	Hours *config.BusinessHours // nil = default Mon-Fri 09-19 Rome
	AsOf  time.Time             // zero = no cutoff
	Now   time.Time             // used for "minutes waiting" when AsOf is zero
}

func (o Options) hours() *config.BusinessHours {
	if o.Hours == nil {
		return config.DefaultHours()
	}
	return o.Hours
}

// Dist is a percentile summary.
type Dist struct {
	N   int      `json:"n"`
	P50 *float64 `json:"p50"`
	P90 *float64 `json:"p90"`
}

// Share is a count with its share of a denominator.
type Share struct {
	Count int      `json:"count"`
	Share *float64 `json:"share"`
}

// Stats is the full KPI set.
type Stats struct {
	Conversations         int   `json:"conversations"`
	CustomerMessages      int   `json:"customer_messages"`
	BotMessages           int   `json:"bot_messages"`
	OperatorMessages      int   `json:"operator_messages"`
	WithOperatorMessages  Share `json:"conversations_with_operator_messages"`
	EngagedConversations  int   `json:"engaged_conversations"`
	SingleCustomerMessage Share `json:"single_customer_message"`
	BotLatencySeconds     Dist  `json:"bot_reply_latency_seconds"`
	StartedOutOfHours     Share `json:"started_out_of_hours"`
	Escalations           int   `json:"escalations"`
	EscalationsInHours    int   `json:"escalations_in_hours"`
	EscalationsOutOfHours int   `json:"escalations_out_of_hours"`
	InHoursWithHumanReply Share `json:"in_hours_escalations_with_human_reply"`
	InHoursFirstReplyMin  Dist  `json:"in_hours_first_reply_minutes"`
	// OOHBotPromisedAgent: the handoff message promises a human reply and does
	// NOT say the shop is closed / when it reopens (the dossier's defect).
	OOHBotPromisedAgent Share `json:"out_of_hours_escalations_bot_promised_agent"`
	// OOHBotStatedReopening: the handoff message tells the customer about the closure.
	OOHBotStatedReopening Share `json:"out_of_hours_escalations_bot_stated_reopening"`
	OOHAnswered           int   `json:"out_of_hours_escalations_answered"`
	// OOHWaiting: no operator reply and not resolved (same as 'escalations --waiting').
	OOHWaiting int `json:"out_of_hours_escalations_waiting"`
	// OOHResolvedNoReply: resolved without any operator reply.
	OOHResolvedNoReply int            `json:"out_of_hours_escalations_resolved_without_reply"`
	CustomerTypedEmail Share          `json:"customer_typed_email"`
	CustomerTypedOrder Share          `json:"customer_typed_order_number"`
	BotLoops           Share          `json:"bot_loops"`
	RatedBotMessages   Share          `json:"rating_coverage"`
	StartPages         map[string]int `json:"start_pages"`
	AsOf               *time.Time     `json:"as_of,omitempty"`
	// EscalationsUnknownAsOf counts conversations re-escalated after --as-of
	// that already had operator replies by then: only the LAST escalation time
	// is stored, so their earlier escalation is missing from the snapshot.
	EscalationsUnknownAsOf int    `json:"escalations_unknown_as_of,omitempty"`
	AsOfCaveat             string `json:"as_of_caveat,omitempty"`
}

// StartPageTypes is the fixed list of start page classes.
var StartPageTypes = []string{"pdp", "home", "help-pages", "account", "cart", "checkout", "search", "blog", "care-plan", "thank-you", "other"}

// ClassifyStartPage classifies a URL (same rules as the reference script).
func ClassifyStartPage(raw string) string {
	if raw == "" {
		return "other"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "other"
	}
	p := u.Path
	switch {
	case p == "" || p == "/":
		return "home"
	case strings.HasSuffix(p, ".html"):
		return "pdp"
	case strings.HasPrefix(p, "/cart"):
		return "cart"
	case strings.HasPrefix(p, "/checkout/order/success"):
		return "thank-you"
	case strings.HasPrefix(p, "/checkout"):
		return "checkout"
	case strings.HasPrefix(p, "/account"):
		return "account"
	case strings.HasPrefix(p, "/cerca"):
		return "search"
	case hasAnyPrefix(p, "/domande-frequenti", "/contatti", "/tempi-di-consegna", "/pagamenti", "/diritto-di-recesso", "/chi-siamo"):
		return "help-pages"
	case strings.HasPrefix(p, "/care-plan"):
		return "care-plan"
	case strings.HasPrefix(p, "/blog"), strings.HasPrefix(p, "/rimedi-per"):
		return "blog"
	}
	return "other"
}

func hasAnyPrefix(s string, ps ...string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// Escalation describes one escalated conversation.
type Escalation struct {
	ConversationID     int64     `json:"id"`
	EscalatedAt        time.Time `json:"-"`
	EscalatedAtRome    string    `json:"escalated_at"`
	InHours            bool      `json:"in_hours"`
	Resolved           bool      `json:"resolved"`
	OperatorReplied    bool      `json:"operator_replied"`
	FirstReplyDelayMin *float64  `json:"first_reply_delay_min"`
	MinutesWaiting     *float64  `json:"minutes_waiting"`
	Waiting            bool      `json:"waiting"`
	BotPromisedAgent   bool      `json:"bot_promised_agent"`
	BotStatedReopening bool      `json:"bot_stated_reopening"`
	ShareURL           string    `json:"share_url"`
}

// View returns the conversation as seen at asOf (zero = unchanged): messages
// after asOf are dropped and escalation/resolution timestamps after asOf are
// treated as absent. The input is not modified.
func View(c client.Conversation, asOf time.Time) client.Conversation {
	if asOf.IsZero() {
		return c
	}
	msgs := make([]client.Message, 0, len(c.Messages))
	for _, m := range c.Messages {
		if !m.CreatedAt.After(asOf) {
			msgs = append(msgs, m)
		}
	}
	c.Messages = msgs
	if c.LastEscalatedAt != nil && c.LastEscalatedAt.After(asOf) {
		c.LastEscalatedAt = nil
	}
	if c.LastEscalationResolvedAt != nil && c.LastEscalationResolvedAt.After(asOf) {
		c.LastEscalationResolvedAt = nil
	}
	return c
}

func sortedMsgs(c client.Conversation) []client.Message {
	ms := append([]client.Message(nil), c.Messages...)
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].CreatedAt.Before(ms[j].CreatedAt) })
	return ms
}

func isCustomer(m client.Message) bool { return m.Role == "user" }
func isOperator(m client.Message) bool { return m.Role == "assistant" && m.ManualReply }
func isBot(m client.Message) bool      { return m.Role == "assistant" && !m.ManualReply }

func plain(s string) string { return tagRe.ReplaceAllString(html.UnescapeString(s), " ") }

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(spaceRe.ReplaceAllString(plain(s), " ")))
}

func ratio(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	return fp(round4(float64(n) / float64(d)))
}

func fp(v float64) *float64 { return &v }

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// dist uses the index rule of the reference scripts: sorted[int(n*q)] (the
// upper median for even n, as quant.py does for bot latency). With
// trueMedian, P50 is the real median (mean of the two middle values for even
// n, as quant2.py's statistics.median does for the first operator reply).
func dist(vals []float64, trueMedian bool) Dist {
	d := Dist{N: len(vals)}
	if len(vals) == 0 {
		return d
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	at := func(q float64) *float64 {
		i := int(float64(len(s)) * q)
		if i >= len(s) {
			i = len(s) - 1
		}
		return fp(round1(s[i]))
	}
	d.P50, d.P90 = at(0.5), at(0.9)
	if trueMedian && len(s)%2 == 0 {
		d.P50 = fp(round1((s[len(s)/2-1] + s[len(s)/2]) / 2))
	}
	return d
}

// AnalyzeEscalation builds the escalation row for a conversation already
// passed through View. ok=false if it has no escalation.
func AnalyzeEscalation(c client.Conversation, o Options) (Escalation, bool) {
	if c.LastEscalatedAt == nil {
		return Escalation{}, false
	}
	esc := *c.LastEscalatedAt
	e := Escalation{
		ConversationID:  c.ID,
		EscalatedAt:     esc,
		EscalatedAtRome: esc.In(o.hours().Loc).Format("2006-01-02 15:04"),
		InHours:         o.hours().IsOpen(esc),
		ShareURL:        c.ShareURL,
	}
	e.Resolved = c.LastEscalationResolvedAt != nil && !c.LastEscalationResolvedAt.Before(esc)
	for _, m := range sortedMsgs(c) {
		if isOperator(m) && !m.CreatedAt.Before(esc) && !e.OperatorReplied {
			e.OperatorReplied = true
			e.FirstReplyDelayMin = fp(round1(m.CreatedAt.Sub(esc).Minutes()))
		}
		if isBot(m) && !m.CreatedAt.Before(esc.Add(-promiseTolerance)) {
			t := plain(m.Message)
			stated := reopenRe.MatchString(t)
			if stated {
				e.BotStatedReopening = true
			}
			if promiseRe.MatchString(t) && !stated {
				e.BotPromisedAgent = true
			}
		}
	}
	e.Waiting = !e.OperatorReplied && !e.Resolved
	if !e.OperatorReplied {
		end := o.AsOf
		if end.IsZero() {
			end = o.Now
		}
		if !end.IsZero() && end.After(esc) {
			e.MinutesWaiting = fp(round1(end.Sub(esc).Minutes()))
		} else {
			e.MinutesWaiting = fp(0)
		}
	}
	return e, true
}

// Escalations returns one row per escalated conversation (after the as-of
// view), ordered by escalation time.
func Escalations(convs []client.Conversation, o Options) []Escalation {
	out := []Escalation{}
	for _, c := range convs {
		c = View(c, o.AsOf)
		if e, ok := AnalyzeEscalation(c, o); ok {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].EscalatedAt.Before(out[j].EscalatedAt) })
	return out
}

// Compute calculates every KPI over convs (already filtered by created_at).
func Compute(convs []client.Conversation, o Options) Stats {
	h := o.hours()
	st := Stats{StartPages: map[string]int{}}
	for _, t := range StartPageTypes {
		st.StartPages[t] = 0
	}
	if !o.AsOf.IsZero() {
		a := o.AsOf.UTC()
		st.AsOf = &a
	}
	var lat, inHoursDelay []float64
	var ratedTotal, rated, ooh, oohPromised, oohReopening, withOp, single, typedEmail, typedOrder, loops int
	for _, raw := range convs {
		if !o.AsOf.IsZero() && raw.CreatedAt.After(o.AsOf) {
			continue
		}
		c := View(raw, o.AsOf)
		if !o.AsOf.IsZero() && raw.LastEscalatedAt != nil && raw.LastEscalatedAt.After(o.AsOf) {
			for _, m := range raw.Messages {
				if isOperator(m) && !m.CreatedAt.After(o.AsOf) {
					st.EscalationsUnknownAsOf++
					break
				}
			}
		}
		st.Conversations++
		page := c.StartLink
		if page == nil || *page == "" {
			page = c.CurrentURL
		}
		if page != nil {
			st.StartPages[ClassifyStartPage(*page)]++
		} else {
			st.StartPages["other"]++
		}
		if !h.IsOpen(c.CreatedAt) {
			ooh++
		}
		ms := sortedMsgs(c)
		var cust, op int
		seen := map[string]int{}
		looped := false
		email, order := false, false
		for i, m := range ms {
			switch {
			case isCustomer(m):
				cust++
				st.CustomerMessages++
				t := plain(m.Message)
				email = email || emailRe.MatchString(t)
				order = order || orderRe.MatchString(t)
				if i+1 < len(ms) && isBot(ms[i+1]) {
					lat = append(lat, ms[i+1].CreatedAt.Sub(m.CreatedAt).Seconds())
				}
			case isOperator(m):
				op++
				st.OperatorMessages++
			case isBot(m):
				st.BotMessages++
				ratedTotal++
				if m.Rate != nil {
					rated++
				}
				if n := normalize(m.Message); len(n) >= 20 {
					seen[n]++
					if seen[n] >= 2 {
						looped = true
					}
				}
			}
		}
		if cust >= 1 {
			st.EngagedConversations++
		}
		if cust == 1 {
			single++
		}
		if op > 0 {
			withOp++
		}
		if email {
			typedEmail++
		}
		if order {
			typedOrder++
		}
		if looped {
			loops++
		}
		if e, ok := AnalyzeEscalation(c, o); ok {
			st.Escalations++
			if e.InHours {
				st.EscalationsInHours++
				if e.OperatorReplied {
					st.InHoursWithHumanReply.Count++
					inHoursDelay = append(inHoursDelay, *e.FirstReplyDelayMin)
				}
			} else {
				st.EscalationsOutOfHours++
				if e.BotPromisedAgent {
					oohPromised++
				}
				if e.BotStatedReopening {
					oohReopening++
				}
				switch {
				case e.OperatorReplied:
					st.OOHAnswered++
				case e.Resolved:
					st.OOHResolvedNoReply++
				default:
					st.OOHWaiting++
				}
			}
		}
	}
	n := st.Conversations
	st.WithOperatorMessages = Share{withOp, ratio(withOp, n)}
	st.SingleCustomerMessage = Share{single, ratio(single, n)}
	st.StartedOutOfHours = Share{ooh, ratio(ooh, n)}
	st.CustomerTypedEmail = Share{typedEmail, ratio(typedEmail, n)}
	st.CustomerTypedOrder = Share{typedOrder, ratio(typedOrder, n)}
	st.BotLoops = Share{loops, ratio(loops, n)}
	st.RatedBotMessages = Share{rated, ratio(rated, ratedTotal)}
	st.InHoursWithHumanReply.Share = ratio(st.InHoursWithHumanReply.Count, st.EscalationsInHours)
	st.OOHBotPromisedAgent = Share{oohPromised, ratio(oohPromised, st.EscalationsOutOfHours)}
	st.OOHBotStatedReopening = Share{oohReopening, ratio(oohReopening, st.EscalationsOutOfHours)}
	if !o.AsOf.IsZero() {
		st.AsOfCaveat = "only the last escalation timestamp is stored: a conversation re-escalated after --as-of loses its earlier escalation in this snapshot (see escalations_unknown_as_of)"
	}
	st.BotLatencySeconds = dist(lat, false)
	st.InHoursFirstReplyMin = dist(inHoursDelay, true)
	return st
}
