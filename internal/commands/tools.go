package commands

import (
	"fmt"
	"strconv"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/output"
	"github.com/spf13/cobra"
)

func init() {
	output.RegisterColumns("tools.list", []output.ColumnDef{
		{Header: "ID", Key: "id"},
		{Header: "NAME", Key: "name"},
		{Header: "ACTIVE", Key: "active"},
		{Header: "CHANNELS", Key: "channels"},
		{Header: "UPDATED", Key: "updated_at"},
	})
	Register(func(root *cobra.Command) { root.AddCommand(newToolsCmd()) })
}

func newToolsCmd() *cobra.Command {
	c := &cobra.Command{Use: "tools", Short: "Custom tools of the chat"}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List custom tools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			tools, err := e.Client.ListCustomTools(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			if tools == nil {
				tools = []client.CustomTool{}
			}
			return printValue("tools.list", tools)
		},
	}, &cobra.Command{
		Use:   "get <id>",
		Short: "Get one custom tool (with instructions)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid tool id %q", args[0])
			}
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			t, err := e.Client.GetCustomTool(ctx(), e.ChatID, id)
			if err != nil {
				return err
			}
			return printValue("", t)
		},
	})
	return c
}
