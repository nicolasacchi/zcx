package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nicolasacchi/clicore/confirm"
	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/config"
	"github.com/nicolasacchi/zcx/internal/output"
	"github.com/nicolasacchi/zcx/internal/store"
)

// ExitError ends the process with Code. Msg (if any) is printed to stderr by
// main; no JSON error envelope is added, so a command can print its own report
// first and still exit non-zero.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("exit %d", e.Code)
}

// Env is the per-invocation plumbing a command needs.
type Env struct {
	Cfg    *config.Resolved
	Client *client.Client // nil when built with offline=true
	ChatID string
}

// Store opens the local cache of the resolved chat.
func (e *Env) Store() (*store.Store, error) { return store.OpenAt(e.Cfg.DataDir, e.ChatID) }

// ctx is the shared request context; the client enforces per-request timeouts.
func ctx() context.Context { return context.Background() }

// loadConfig resolves configuration from the global flags.
func loadConfig() (*config.Resolved, error) {
	return config.Load(config.Options{TokenFlag: tokenFlag, ChatFlag: chatFlag})
}

// progressf writes a progress line to stderr unless --quiet.
func progressf(format string, a ...any) {
	if !quietFlag {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
	}
}

// newClient builds the API client from the resolved config. Rate-limit pauses
// are announced on stderr.
func newClient(cfg *config.Resolved) *client.Client {
	return client.New(client.Options{
		Token:   cfg.Token,
		BaseURL: cfg.BaseURL,
		Verbose: verboseFlag,
		OnRateWait: func(d time.Duration) {
			progressf("rate limit nearly exhausted, waiting %s for the window to reset...", d.Round(time.Second))
		},
	})
}

func missingTokenErr() error {
	return &client.APIError{Status: 401, Kind: "auth_failed", Detail: "no API token configured",
		Hint: "set ZIPCHAT_API_TOKEN, add it to ~/.config/zipchat/env, or pass --token"}
}

// getEnv is the standard preamble of a command. needChat=false skips chat
// resolution (e.g. `chats list`). With offline=true no token is required and
// no network call is made: the chat id comes from flag/env/config or, failing
// that, from the only chat directory present in the data dir.
func getEnv(needChat, offline bool) (*Env, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	e := &Env{Cfg: cfg, ChatID: cfg.ChatID}
	if offline {
		if needChat && e.ChatID == "" {
			if e.ChatID, err = soleCachedChat(cfg.DataDir); err != nil {
				return nil, err
			}
		}
		return e, nil
	}
	if cfg.Token == "" {
		return nil, missingTokenErr()
	}
	e.Client = newClient(cfg)
	if needChat && e.ChatID == "" {
		if e.ChatID, err = detectChat(ctx(), e.Client); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// detectChat returns the id of the only chat the token can see.
func detectChat(c context.Context, cl *client.Client) (string, error) {
	chats, err := cl.ListChats(c)
	if err != nil {
		return "", err
	}
	switch len(chats) {
	case 0:
		return "", errors.New("the token sees no chats; pass --chat")
	case 1:
		return fmt.Sprint(chats[0].ID), nil
	}
	ids := make([]string, len(chats))
	for i, ch := range chats {
		ids[i] = fmt.Sprint(ch.ID)
	}
	return "", fmt.Errorf("the token sees %d chats (%s); pass --chat or set ZCX_CHAT_ID", len(chats), strings.Join(ids, ", "))
}

func soleCachedChat(dataDir string) (string, error) {
	ents, err := os.ReadDir(dataDir)
	var ids []string
	if err == nil {
		for _, e := range ents {
			if e.IsDir() {
				ids = append(ids, e.Name())
			}
		}
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	return "", errors.New("chat id unknown offline; pass --chat or set ZCX_CHAT_ID")
}

// isJSONMode reports whether output must be JSON.
func isJSONMode() bool { return output.IsJSON(jsonFlag, jqFilter()) }

func jqFilter() string { return jqFlag }

// printData applies the agent row cap then renders a table (TTY + registered
// command) or JSON. command is the output.RegisterColumns key ("" = JSON).
func printData(command string, data []byte) error {
	data = output.CapArray(data, limitFlag)
	return output.PrintData(command, data, isJSONMode(), jqFlag)
}

// printValue marshals v and calls printData.
func printValue(command string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return printData(command, b)
}

// gateWrite is the standard preamble of a state-changing verb (clicore confirm
// gate). handled=true means return err immediately: either --dry-run printed
// the preview (err nil) or the gate refused with write_locked (exit 6).
func gateWrite(action string) (handled bool, err error) {
	return confirm.Gate(os.Stdout, yesFlag, dryRunFlag, action)
}

// dirModeProblems lists paths under root whose permissions are looser than
// 0700 (dirs) / 0600 (files). Used by `config doctor`.
func dirModeProblems(root string) []string {
	var bad []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		perm := info.Mode().Perm()
		if (d.IsDir() && perm&0o077 != 0) || (!d.IsDir() && perm&0o177 != 0) {
			bad = append(bad, fmt.Sprintf("%s (%04o)", p, perm))
		}
		return nil
	})
	return bad
}
