package commands

import (
	"strings"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/output"
	"github.com/spf13/cobra"
)

func init() {
	output.RegisterColumns("users.list", []output.ColumnDef{
		{Header: "ID", Key: "id"},
		{Header: "NAME", Key: "name"},
		{Header: "EMAIL", Key: "email"},
	})
	Register(func(root *cobra.Command) { root.AddCommand(newUsersCmd()) })
}

// usersMaskEmail turns "jane.doe@example.com" into "j***@example.com".
func usersMaskEmail(s string) string {
	at := strings.LastIndex(s, "@")
	if at <= 0 {
		if s == "" {
			return s
		}
		return "***"
	}
	return s[:1] + "***" + s[at:]
}

func newUsersCmd() *cobra.Command {
	c := &cobra.Command{Use: "users", Short: "Dashboard users of the chat"}
	var showPII bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List users (emails masked unless --show-pii)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			users, err := e.Client.ListUsers(ctx(), e.ChatID)
			if err != nil {
				return err
			}
			if users == nil {
				users = []client.User{}
			}
			if !showPII {
				for i := range users {
					users[i].Email = usersMaskEmail(users[i].Email)
				}
			}
			return printValue("users.list", users)
		},
	}
	list.Flags().BoolVar(&showPII, "show-pii", false, "show full email addresses")
	c.AddCommand(list)
	return c
}
