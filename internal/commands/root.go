// Package commands wires the zcx cobra tree.
//
// Feature files NEVER edit this file. Each one registers itself from init():
//
//	func init() {
//		Register(func(root *cobra.Command) { root.AddCommand(newFooCmd()) })
//	}
//
// Registered functions run (once each, in registration order) the first time
// Root() or Execute() is called.
package commands

import (
	"github.com/spf13/cobra"
)

// Global flag values (persistent flags on the root command). Feature files in
// this package read them directly.
var (
	version     = "dev"
	tokenFlag   string
	chatFlag    string
	jsonFlag    bool
	jqFlag      string
	limitFlag   int
	verboseFlag bool
	quietFlag   bool
	yesFlag     bool
	dryRunFlag  bool
	offlineFlag bool
)

var (
	registry []func(root *cobra.Command)
	applied  int
)

var rootCmd = &cobra.Command{
	Use:   "zcx",
	Short: "zcx - Zipchat Backend API explorer",
	Long: `zcx is a CLI for the Zipchat Backend API (Zipchat is an AI shopping and
support assistant for e-commerce) - built for humans AND for LLM/agent use.

Humans get tables on a TTY; pipes, --json, --jq and Claude Code (CLAUDECODE=1)
get clean JSON with an agent row cap and a truncated marker. Times shown to
humans are Europe/Rome; the API speaks UTC.

Auth: ZIPCHAT_API_TOKEN, ~/.config/zipchat/env, or --token (zc- chat token or
zcu- user token). Chat id: --chat, ZCX_CHAT_ID, config.toml, or auto-detected
when the token sees exactly one chat. Run 'zcx config doctor' to check setup.

The only write zcx performs is 'prompt push' / 'prompt restore', guarded by a
base-sha check, a diff, --yes, a local backup and a post-write verification.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Register queues fn to attach commands to the root. Call it from init().
func Register(fn func(root *cobra.Command)) { registry = append(registry, fn) }

// Root returns the root command with every registered feature attached.
func Root() *cobra.Command {
	for ; applied < len(registry); applied++ {
		registry[applied](rootCmd)
	}
	return rootCmd
}

// SetVersion records the build version (set from main via -ldflags).
func SetVersion(v string) {
	version = v
	rootCmd.Version = v
}

// Execute runs the CLI.
func Execute() error { return Root().Execute() }

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&tokenFlag, "token", "", "API token (overrides ZIPCHAT_API_TOKEN and config files)")
	pf.StringVar(&chatFlag, "chat", "", "Chat id (overrides ZCX_CHAT_ID/config; auto-detected when the token sees one chat)")
	pf.BoolVar(&jsonFlag, "json", false, "Force JSON output (auto-enabled when stdout is not a TTY)")
	pf.StringVar(&jqFlag, "jq", "", "Apply a gjson path filter to JSON output (gjson syntax, not jq)")
	pf.IntVar(&limitFlag, "limit", 0, "Max rows to return (0 = unbounded; agent default 100 under CLAUDECODE)")
	pf.BoolVarP(&verboseFlag, "verbose", "v", false, "Print request details to stderr (URLs redacted)")
	pf.BoolVarP(&quietFlag, "quiet", "q", false, "Suppress progress and non-error stderr output")
	pf.BoolVar(&yesFlag, "yes", false, "Confirm a state-changing operation (alias: --confirm)")
	pf.BoolVar(&yesFlag, "confirm", false, "Alias for --yes")
	pf.BoolVar(&dryRunFlag, "dry-run", false, "Preview a state-changing operation without sending it")
	pf.BoolVar(&offlineFlag, "offline", false, "Use only the local cache; make no network calls")
}
