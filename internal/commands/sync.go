package commands

import (
	"context"
	"errors"
	"os"
	"os/signal"

	"github.com/nicolasacchi/zcx/internal/syncer"
	"github.com/nicolasacchi/zcx/internal/timeparse"
	"github.com/spf13/cobra"
)

func init() {
	Register(func(root *cobra.Command) { root.AddCommand(syncCmd()) })
}

func syncCmd() *cobra.Command {
	var full bool
	var concurrency int
	c := &cobra.Command{
		Use:   "sync",
		Short: "Incrementally sync conversations into the local cache",
		Long: "Fetches new and changed conversations (with all messages) into the local cache.\n" +
			"Conversations are downloaded by a small worker pool (--concurrency, default 4, max 8;\n" +
			"or sync_concurrency in config.toml). The API client keeps all workers under the\n" +
			"120 requests/minute limit and pauses until the window resets when it runs low.\n" +
			"The cache contains customer PII: directories are 0700, files 0600.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if offlineFlag {
				return errors.New("sync needs the network; drop --offline")
			}
			e, err := getEnv(true, false)
			if err != nil {
				return err
			}
			sum, err := syncRun(e, full, concurrency)
			if sum != nil {
				if perr := printValue("", sum); perr != nil && err == nil {
					err = perr
				}
			}
			return err
		},
	}
	c.Flags().BoolVar(&full, "full", false, "ignore the stored high-water marks and refetch everything")
	c.Flags().IntVar(&concurrency, "concurrency", 0, "conversations fetched in parallel (default 4, or sync_concurrency from config.toml; max 8)")
	return c
}

// syncRun runs one sync. concurrency 0 falls back to sync_concurrency from
// config.toml, then to the syncer default; Ctrl-C cancels the workers.
func syncRun(e *Env, full bool, concurrency int) (*syncer.Summary, error) {
	st, err := e.Store()
	if err != nil {
		return nil, err
	}
	if concurrency == 0 && e.Cfg != nil {
		concurrency = e.Cfg.SyncConcurrency
	}
	sctx, stop := signal.NotifyContext(ctx(), os.Interrupt)
	defer stop()
	return syncer.Run(sctx, e.Client, st, syncer.Options{Full: full, Concurrency: concurrency, Progress: progressf})
}

// EnsureFresh runs an incremental sync before a command reads the cache.
// With offline=true it does nothing. Other commands call it as
// `EnsureFresh(ctx(), offlineFlag)` after resolving env; it resolves its own
// env, which costs no extra request when the chat id is configured.
func EnsureFresh(_ context.Context, offline bool) error {
	if offline {
		return nil
	}
	e, err := getEnv(true, false)
	if err != nil {
		return err
	}
	return EnsureFreshEnv(e)
}

// EnsureFreshEnv is EnsureFresh for callers that already hold an Env.
func EnsureFreshEnv(e *Env) error {
	if e.Client == nil {
		return nil
	}
	_, err := syncRun(e, false, 0)
	return err
}

// strictSync makes a failed pre-sync fatal for stats/escalations/export.
var strictSync bool

func addStrictFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&strictSync, "strict-sync", false, "fail if the automatic pre-sync fails instead of using the cached data")
}

// refreshOrWarn runs the pre-sync with refresh. When it fails but a usable
// cache exists it warns on stderr and carries on with the cached data (unless
// --strict-sync); with no cache the error is returned.
func refreshOrWarn(e *Env, refresh func(*Env) error) error {
	err := refresh(e)
	if err == nil || strictSync {
		return err
	}
	st, serr := e.Store()
	if serr != nil || st.CountConversations() == 0 {
		return err
	}
	last := "never"
	if state, serr := st.State(); serr == nil && state.LastSync != nil {
		last = state.LastSync.In(timeparse.Rome).Format("2006-01-02 15:04 MST")
	}
	progressf("WARNING: pre-sync failed (%v); using cached data, which may be stale (last successful sync: %s)", err, last)
	return nil
}
