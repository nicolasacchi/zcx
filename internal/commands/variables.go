package commands

import (
	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/output"
	"github.com/spf13/cobra"
)

func init() {
	output.RegisterColumns("variables.list", []output.ColumnDef{
		{Header: "ID", Key: "id"},
		{Header: "NAME", Key: "name"},
		{Header: "KIND", Key: "kind"},
		{Header: "HAS VALUE", Key: "has_value"},
		{Header: "USAGE", Key: "usage_count"},
	})
	Register(func(root *cobra.Command) { root.AddCommand(newVariablesCmd()) })
}

func newVariablesCmd() *cobra.Command {
	c := &cobra.Command{Use: "variables", Short: "Variables used by custom tools (values are never returned)"}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List variables (names and has_value only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			vars, err := e.Client.ListVariables(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			if vars == nil {
				vars = []client.Variable{}
			}
			return printValue("variables.list", vars)
		},
	})
	return c
}
