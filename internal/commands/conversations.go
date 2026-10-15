package commands

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/output"
	"github.com/nicolasacchi/zcx/internal/timeparse"
	"github.com/spf13/cobra"
)

func init() {
	output.RegisterColumns("conversations.list", []output.ColumnDef{
		{Header: "ID", Key: "id"},
		{Header: "CHANNEL", Key: "channel"},
		{Header: "LAST MESSAGE (Rome)", Key: "last_message_at", Format: convRomeCell},
		{Header: "CREATED (Rome)", Key: "created_at", Format: convRomeCell},
		{Header: "ESCALATED (Rome)", Key: "last_escalated_at", Format: convRomeCell},
		{Header: "ARCHIVED", Key: "archived"},
		{Header: "LEAD", Key: "lead.email"},
	})
	Register(func(root *cobra.Command) { root.AddCommand(newConversationsCmd()) })
}

// convRomeCell renders an RFC3339 string cell in Europe/Rome.
func convRomeCell(v any) string {
	s, ok := v.(string)
	if !ok || s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return s
	}
	return timeparse.FormatRome(t)
}

func newConversationsCmd() *cobra.Command {
	c := &cobra.Command{Use: "conversations", Short: "Read conversations"}

	var since, updatedSince string
	var all bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List conversations (newest activity first; one page unless --all or --limit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			var o client.ListConversationsOptions
			if since != "" {
				t, err := timeparse.Parse(since, now)
				if err != nil {
					return err
				}
				o.LastMessageAtSince = t
			}
			if updatedSince != "" {
				t, err := timeparse.Parse(updatedSince, now)
				if err != nil {
					return err
				}
				o.UpdatedAtSince = t
			}
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			var convs []client.Conversation
			if all || limitFlag > 0 {
				convs, err = e.Client.ListAllConversations(ctx(), e.ChatID, o, limitFlag)
			} else {
				var p *client.ConversationPage
				p, err = e.Client.ListConversations(ctx(), e.ChatID, o)
				if p != nil {
					convs = p.Conversations
				}
			}
			if err != nil {
				return err
			}
			if convs == nil {
				convs = []client.Conversation{}
			}
			return printValue("conversations.list", convs)
		},
	}
	list.Flags().StringVar(&since, "since", "", "only conversations with a message since (7d, 24h, RFC3339, Rome date)")
	list.Flags().StringVar(&updatedSince, "updated-since", "", "only conversations updated since")
	list.Flags().BoolVar(&all, "all", false, "follow every page")

	get := &cobra.Command{
		Use:   "get <id>",
		Short: "Get one conversation with all its messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid conversation id %q", args[0])
			}
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			conv, err := e.Client.GetConversation(ctx(), e.ChatID, id)
			if err != nil {
				return err
			}
			if isJSONMode() {
				return printValue("", conv)
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Conversation %d  channel=%s  created=%s (Rome)\n", conv.ID, conv.Channel, timeparse.FormatRome(conv.CreatedAt))
			if conv.ShareURL != "" {
				fmt.Fprintf(&b, "Share: %s\n", conv.ShareURL)
			}
			for _, m := range conv.Messages {
				who := m.Role
				if m.ManualReply {
					who += " (operator)"
				}
				fmt.Fprintf(&b, "[%s] %s: %s\n", timeparse.FormatRome(m.CreatedAt), who, strings.TrimSpace(m.Message))
			}
			_, err = io.WriteString(cmd.OutOrStdout(), b.String())
			return err
		},
	}
	c.AddCommand(list, get)
	return c
}
