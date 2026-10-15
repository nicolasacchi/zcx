package commands

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/nicolasacchi/zcx/internal/analytics"
	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/timeparse"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// analyticsRefresh runs the incremental sync before analytics unless --offline.
// Tests replace it to seed the store.
var analyticsRefresh = EnsureFreshEnv

// channelFlag restricts stats and escalations to a comma list of channels
// (default: the website channels chat and ai_search; instagram is excluded).
// Empty means all channels.
var channelFlag = "chat,ai_search"

// hostFlag keeps only conversations whose start_link (fallback current_url)
// is on this host; URL-less conversations are test traffic and are dropped.
// When --host is not given, [scope] host from config.toml applies; "*" means
// every host. liveFlag applies the [scope] live_since floor.
var (
	hostFlag     string
	liveFlag     bool
	hostFlagSets []*pflag.FlagSet
)

// effectiveHost resolves --host against the config default.
func effectiveHost(cfgHost string) string {
	for _, fs := range hostFlagSets {
		if f := fs.Lookup("host"); f != nil && f.Changed {
			h := strings.ToLower(strings.TrimSpace(hostFlag))
			if h == "*" {
				return ""
			}
			return h
		}
	}
	return cfgHost
}

func convHost(c client.Conversation) string {
	for _, p := range []*string{c.StartLink, c.CurrentURL} {
		if p != nil && *p != "" {
			if u, err := url.Parse(*p); err == nil && u.Host != "" {
				return strings.ToLower(u.Host)
			}
		}
	}
	return ""
}

func addChannelFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&hostFlag, "host", "", "keep only conversations started on this host (start_link, else current_url); URL-less test conversations are dropped; default [scope] host from config.toml; '*' = every host")
	cmd.Flags().BoolVar(&liveFlag, "live", false, "only conversations created at or after [scope] live_since from config.toml (the go-live)")
	hostFlagSets = append(hostFlagSets, cmd.Flags())
	addStrictFlag(cmd)
	cmd.Flags().StringVar(&channelFlag, "channel", "chat,ai_search", "comma list of channels to include (website channels by default; instagram excluded); empty = all")
}

func init() {
	Register(func(root *cobra.Command) { root.AddCommand(newStatsCmd()) })
}

// statsLoad resolves the env, refreshes the cache (unless offline) and loads
// the conversations created in [from,to).
func statsLoad(from, to *time.Time) (*Env, []client.Conversation, error) {
	e, err := getEnv(true, offlineFlag)
	if err != nil {
		return nil, nil, err
	}
	if !offlineFlag {
		if err := refreshOrWarn(e, analyticsRefresh); err != nil {
			return nil, nil, err
		}
	}
	st, err := e.Store()
	if err != nil {
		return nil, nil, err
	}
	convs, err := st.LoadAll(from, to)
	if err != nil {
		return nil, nil, err
	}
	if channelFlag != "" {
		allowed := map[string]bool{}
		for _, ch := range strings.Split(channelFlag, ",") {
			allowed[strings.TrimSpace(ch)] = true
		}
		kept := convs[:0]
		for _, c := range convs {
			if c.Channel == "" || allowed[c.Channel] {
				kept = append(kept, c)
			}
		}
		convs = kept
	}
	host := effectiveHost(e.Cfg.ScopeHost)
	if liveFlag && e.Cfg.LiveSince == nil {
		return nil, nil, fmt.Errorf("--live needs [scope] live_since (RFC3339) in %s", e.Cfg.ConfigPath)
	}
	if host != "" || liveFlag {
		kept := convs[:0]
		for _, c := range convs {
			if host != "" && convHost(c) != host {
				continue
			}
			if liveFlag && c.CreatedAt.Before(*e.Cfg.LiveSince) {
				continue
			}
			kept = append(kept, c)
		}
		convs = kept
	}
	if len(convs) == 0 {
		progressf("cache is empty for this range; run 'zcx sync' first")
	}
	return e, convs, nil
}

func statsParseAsOf(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := timeparse.ParseEnd(s, now) // a bare date means the end of that Rome day
	if err != nil {
		return time.Time{}, fmt.Errorf("--as-of: %w", err)
	}
	return t, nil
}

func newStatsCmd() *cobra.Command {
	var from, to, asOf string
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Go-live KPIs computed from the local cache",
		Long: `Computes the dossier KPIs over conversations created in [--from, --to).

Customer message = role user; bot message = assistant without manual_reply;
operator message = assistant with manual_reply. Business hours come from
config.toml (default Mon-Fri 09:00-19:00 Europe/Rome). --as-of ignores messages
and escalation/resolution timestamps after that instant, so a past snapshot is
reproducible, with one limit: Zipchat stores only the LAST escalation time, so
a conversation re-escalated after --as-of loses its earlier escalation
(counted in escalations_unknown_as_of, warned on stderr). A bare-date --as-of
means the end of that Rome day. Scope: [scope] host in config.toml limits the
count to conversations started on that host (--host overrides, '*' = every
host); --live adds the [scope] live_since floor. An incremental sync runs
first unless --offline.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			f, t, err := timeparse.ParseRange(from, to, now)
			if err != nil {
				return err
			}
			ao, err := statsParseAsOf(asOf, now)
			if err != nil {
				return err
			}
			if f != nil && !ao.IsZero() && ao.Before(*f) {
				progressf("WARNING: --as-of %s is before --from %s: the snapshot will be empty", timeparse.FormatRome(ao), timeparse.FormatRome(*f))
			}
			e, convs, err := statsLoad(f, t)
			if err != nil {
				return err
			}
			res := analytics.Compute(convs, analytics.Options{Hours: e.Cfg.Hours, AsOf: ao, Now: now})
			if res.EscalationsUnknownAsOf > 0 {
				progressf("WARNING: %d conversation(s) were re-escalated after --as-of; their earlier escalation is missing from this snapshot", res.EscalationsUnknownAsOf)
			}
			if isJSONMode() {
				return printValue("", res)
			}
			statsTable(res)
			return nil
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "range start (Rome local date/time, RFC3339, or relative like 7d)")
	cmd.Flags().StringVar(&to, "to", "", "range end, exclusive (a bare date includes that whole Rome day)")
	cmd.Flags().StringVar(&asOf, "as-of", "", "snapshot instant: ignore anything after it")
	addChannelFlag(cmd)
	return cmd
}

func statsPct(s analytics.Share) string {
	if s.Share == nil {
		return fmt.Sprintf("%d", s.Count)
	}
	return fmt.Sprintf("%d (%.1f%%)", s.Count, *s.Share*100)
}

func statsDist(d analytics.Dist, unit string) string {
	if d.P50 == nil {
		return fmt.Sprintf("n=%d", d.N)
	}
	return fmt.Sprintf("p50 %.1f%s / p90 %.1f%s (n=%d)", *d.P50, unit, *d.P90, unit, d.N)
}

func statsTable(s analytics.Stats) {
	tw := table.NewWriter()
	tw.SetOutputMirror(os.Stdout)
	tw.AppendHeader(table.Row{"Metric", "Value"})
	rows := [][2]string{
		{"Conversations", fmt.Sprint(s.Conversations)},
		{"Customer / bot / operator messages", fmt.Sprintf("%d / %d / %d", s.CustomerMessages, s.BotMessages, s.OperatorMessages)},
		{"With operator messages", statsPct(s.WithOperatorMessages)},
		{"Single customer message", statsPct(s.SingleCustomerMessage)},
		{"Bot reply latency", statsDist(s.BotLatencySeconds, "s")},
		{"Started out of hours", statsPct(s.StartedOutOfHours)},
		{"Escalations (in / out of hours)", fmt.Sprintf("%d (%d / %d)", s.Escalations, s.EscalationsInHours, s.EscalationsOutOfHours)},
		{"In-hours escalations with human reply", statsPct(s.InHoursWithHumanReply)},
		{"In-hours first reply delay", statsDist(s.InHoursFirstReplyMin, "min")},
		{"Out-of-hours: bot promised an agent (no closure notice)", statsPct(s.OOHBotPromisedAgent)},
		{"Out-of-hours: bot stated the reopening", statsPct(s.OOHBotStatedReopening)},
		{"Out-of-hours: answered / waiting / resolved w/o reply", fmt.Sprintf("%d / %d / %d", s.OOHAnswered, s.OOHWaiting, s.OOHResolvedNoReply)},
		{"Customer typed an email", statsPct(s.CustomerTypedEmail)},
		{"Customer typed an order number", statsPct(s.CustomerTypedOrder)},
		{"Bot loops", statsPct(s.BotLoops)},
		{"Rating coverage (bot messages)", statsPct(s.RatedBotMessages)},
	}
	for _, r := range rows {
		tw.AppendRow(table.Row{r[0], r[1]})
	}
	var pages []string
	for _, k := range analytics.StartPageTypes {
		if n := s.StartPages[k]; n > 0 {
			pages = append(pages, fmt.Sprintf("%s=%d", k, n))
		}
	}
	tw.AppendRow(table.Row{"Start pages", strings.Join(pages, " ")})
	tw.Render()
}
