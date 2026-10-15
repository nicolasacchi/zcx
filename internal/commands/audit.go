package commands

import (
	"time"

	"github.com/spf13/cobra"
)

func init() {
	Register(func(root *cobra.Command) { root.AddCommand(newAuditCmd()) })
}

func newAuditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "audit",
		Short: "Audit prompt size, custom tools, variables and (from the cache) rating and lead coverage",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			p, err := e.Client.GetPrompt(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			tools, err := e.Client.ListCustomTools(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			vars, err := e.Client.ListVariables(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			runes := len([]rune(p.MasterPrompt))
			pct := 0.0
			if p.MasterPromptMaxLength > 0 {
				pct = round1(float64(runes) * 100 / float64(p.MasterPromptMaxLength))
			}
			active := 0
			for _, t := range tools {
				if t.Active {
					active++
				}
			}
			varRows := make([]map[string]any, 0, len(vars))
			for _, v := range vars {
				varRows = append(varRows, map[string]any{"name": v.Name, "has_value": v.HasValue, "usage_count": v.UsageCount})
			}
			report := map[string]any{
				"chat_id":      e.ChatID,
				"prompt":       map[string]any{"length": runes, "max_length": p.MasterPromptMaxLength, "percent_used": pct},
				"custom_tools": map[string]any{"count": len(tools), "active": active},
				"variables":    varRows,
				"cache":        auditCache(e),
			}
			return printValue("", report)
		},
	}
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// auditCache computes coverage from the local cache; it never fails the audit.
func auditCache(e *Env) map[string]any {
	st, err := e.Store()
	if err != nil {
		return map[string]any{"available": false, "error": err.Error()}
	}
	convs, err := st.LoadAll(nil, nil)
	if err != nil || len(convs) == 0 {
		return map[string]any{"available": false, "conversations": 0, "hint": "run `zcx sync` to populate the cache"}
	}
	var bot, rated, withLead int
	for _, c := range convs {
		if c.Lead != nil && c.Lead.Email != "" {
			withLead++
		}
		for _, m := range c.Messages {
			if m.Role == "assistant" && !m.ManualReply {
				bot++
				if m.Rate != nil {
					rated++
				}
			}
		}
	}
	out := map[string]any{
		"available":       true,
		"conversations":   len(convs),
		"bot_messages":    bot,
		"rated_messages":  rated,
		"lead_coverage":   ratio(withLead, len(convs)),
		"rating_coverage": ratio(rated, bot),
	}
	if s, err := st.State(); err == nil && s.LastSync != nil {
		out["last_sync"] = s.LastSync
		out["age_hours"] = round1(time.Since(*s.LastSync).Hours())
	}
	return out
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(int(float64(a)/float64(b)*10000+0.5)) / 10000
}
