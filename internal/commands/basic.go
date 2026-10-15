package commands

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/spf13/cobra"
)

func init() {
	Register(func(root *cobra.Command) {
		root.AddCommand(versionCmd(), chatsCmd(), configCmd())
	})
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the zcx version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if isJSONMode() {
				return printValue("", map[string]string{"version": version})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "zcx", version)
			return nil
		},
	}
}

func chatsCmd() *cobra.Command {
	c := &cobra.Command{Use: "chats", Short: "Chats reachable with the credential"}
	c.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List chats (discover chat ids)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := getEnv(false, false)
			if err != nil {
				return err
			}
			chats, err := e.Client.ListChats(ctx())
			if err != nil {
				return err
			}
			if chats == nil {
				chats = []client.Chat{}
			}
			return printValue("chats.list", chats)
		},
	})
	return c
}

func configCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Inspect zcx configuration"}
	c.AddCommand(
		&cobra.Command{
			Use:   "show",
			Short: "Show the resolved configuration (token masked)",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := loadConfig()
				if err != nil {
					return err
				}
				return printValue("", map[string]any{
					"token":        cfg.MaskedToken(),
					"token_source": cfg.TokenSource,
					"chat_id":      cfg.ChatID,
					"chat_source":  cfg.ChatSource,
					"base_url":     cfg.BaseURL,
					"base_source":  cfg.BaseSource,
					"data_dir":     cfg.DataDir,
					"config_file":  cfg.ConfigPath,
					"env_file":     cfg.EnvPath,
					"hours":        cfg.Hours.Describe(),
				})
			},
		},
		&cobra.Command{
			Use:   "doctor",
			Short: "Check token, chat id, API reachability, rate limit and data dir permissions",
			Args:  cobra.NoArgs,
			RunE:  runDoctor,
		},
	)
	return c
}

func runDoctor(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ok := true
	report := map[string]any{}

	report["token"] = map[string]any{
		"ok": cfg.Token != "", "source": cfg.TokenSource, "masked": cfg.MaskedToken(),
	}
	if cfg.Token == "" {
		ok = false
	}
	report["base_url"] = cfg.BaseURL

	exit := 1
	chatID, chatSource := cfg.ChatID, cfg.ChatSource
	if cfg.Token != "" {
		cl := newClient(cfg)
		start := time.Now()
		chats, err := cl.ListChats(ctx())
		api := map[string]any{"ok": err == nil, "latency_ms": time.Since(start).Milliseconds()}
		if err != nil {
			ok = false
			api["error"] = err.Error()
			var ae *client.APIError
			if errors.As(err, &ae) {
				exit = ae.ExitCode()
				api["hint"] = ae.Hint
			}
		} else {
			api["chats_visible"] = len(chats)
			if chatID == "" && len(chats) == 1 {
				chatID, chatSource = fmt.Sprint(chats[0].ID), "auto-detected"
			}
		}
		report["api"] = api
		rl := cl.RateLimit()
		report["rate_limit"] = map[string]any{
			"headers_seen": rl.Known, "limit": rl.Limit, "remaining": rl.Remaining,
			"resets_at": rl.Reset.UTC().Format(time.RFC3339),
		}
		if !rl.Known && err == nil {
			ok = false
		}
	}
	chat := map[string]any{"ok": chatID != "", "id": chatID, "source": chatSource}
	if chatID == "" {
		ok = false
		chat["hint"] = "pass --chat, set ZCX_CHAT_ID, or use a token that sees exactly one chat"
	}
	report["chat"] = chat

	dd := map[string]any{"path": cfg.DataDir}
	if _, err := os.Stat(cfg.DataDir); err != nil {
		dd["exists"] = false
		dd["ok"] = true // created on first sync
	} else {
		bad := dirModeProblems(cfg.DataDir)
		dd["exists"] = true
		dd["ok"] = len(bad) == 0
		if len(bad) > 0 {
			ok = false
			dd["loose_permissions"] = bad
		}
	}
	report["data_dir"] = dd
	report["hours"] = cfg.Hours.Describe()
	report["ok"] = ok

	if err := printValue("", report); err != nil {
		return err
	}
	if !ok {
		return &ExitError{Code: exit}
	}
	return nil
}
