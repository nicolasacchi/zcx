package commands

import (
	"time"

	"github.com/nicolasacchi/zcx/internal/analytics"
	"github.com/nicolasacchi/zcx/internal/output"
	"github.com/nicolasacchi/zcx/internal/timeparse"
	"github.com/spf13/cobra"
)

func init() {
	output.RegisterColumns("escalations", []output.ColumnDef{
		{Header: "ID", Key: "id"},
		{Header: "Escalated (Rome)", Key: "escalated_at"},
		{Header: "In hours", Key: "in_hours"},
		{Header: "Resolved", Key: "resolved"},
		{Header: "Operator replied", Key: "operator_replied"},
		{Header: "First reply (min)", Key: "first_reply_delay_min"},
		{Header: "Waiting (min)", Key: "minutes_waiting"},
		{Header: "Share URL", Key: "share_url"},
	})
	Register(func(root *cobra.Command) { root.AddCommand(newEscalationsCmd()) })
}

func newEscalationsCmd() *cobra.Command {
	var from, to, asOf string
	var waiting bool
	cmd := &cobra.Command{
		Use:   "escalations",
		Short: "Escalated conversations with operator reply delays",
		Long: `One row per conversation with an escalation (last_escalated_at) whose
escalation instant falls in [--from, --to). --waiting keeps only conversations
with no operator reply since the escalation and not resolved. An incremental
sync runs first unless --offline.`,
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
			e, convs, err := statsLoad(nil, nil)
			if err != nil {
				return err
			}
			rows := analytics.Escalations(convs, analytics.Options{Hours: e.Cfg.Hours, AsOf: ao, Now: now})
			out := rows[:0]
			for _, r := range rows {
				if f != nil && r.EscalatedAt.Before(*f) {
					continue
				}
				if t != nil && !r.EscalatedAt.Before(*t) {
					continue
				}
				if waiting && !r.Waiting {
					continue
				}
				out = append(out, r)
			}
			return printValue("escalations", out)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "escalation range start (Rome local date/time, RFC3339, or relative)")
	cmd.Flags().StringVar(&to, "to", "", "escalation range end, exclusive (bare date includes that day)")
	cmd.Flags().StringVar(&asOf, "as-of", "", "snapshot instant: ignore anything after it")
	addChannelFlag(cmd)
	cmd.Flags().BoolVar(&waiting, "waiting", false, "only escalations still waiting for an operator reply")
	return cmd
}
