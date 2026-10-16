// Package config resolves zcx credentials, the chat id, the base URL, the local
// data directory and the business-hours calendar.
//
// Precedence everywhere is flag > environment > file:
//
//	token    --token > ZIPCHAT_API_TOKEN > ~/.config/zipchat/env > ~/.config/zcx/config.toml
//	chat id  --chat  > ZCX_CHAT_ID > config.toml > (auto-detect, done by the commands layer)
//	base URL ZCX_BASE_URL > config.toml (base_url) > production default
//	data dir $XDG_DATA_HOME/zcx or ~/.local/share/zcx
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	cliconfig "github.com/nicolasacchi/clicore/config"
)

const (
	// DefaultBaseURL is the production Zipchat Backend API origin + prefix.
	DefaultBaseURL = "https://app.zipchat.ai/api/integrations/backend_api/v1"

	EnvToken   = "ZIPCHAT_API_TOKEN"
	EnvChatID  = "ZCX_CHAT_ID"
	EnvBaseURL = "ZCX_BASE_URL"

	// Token / chat sources reported by `config show` and `config doctor`.
	SourceFlag = "flag"
	SourceNone = "none"
)

// File is the on-disk shape of ~/.config/zcx/config.toml. Every field is
// optional.
type File struct {
	Token   string `toml:"token,omitempty"`
	ChatID  any    `toml:"chat_id,omitempty"` // int or string in TOML
	BaseURL string `toml:"base_url,omitempty"`
	// SyncConcurrency is the number of parallel conversation fetches of a sync
	// (also used by the automatic pre-sync); 0 = default, clamped to 1..8.
	SyncConcurrency int         `toml:"sync_concurrency,omitempty"`
	Hours           HoursConfig `toml:"hours"`
	Scope           ScopeConfig `toml:"scope"`
}

// ScopeConfig is the default analytics scope for stats and escalations.
// Host keeps only conversations started on that host (start_link, else
// current_url); LiveSince (RFC3339) is the go-live floor applied by --live.
type ScopeConfig struct {
	Host      string `toml:"host,omitempty"`
	LiveSince string `toml:"live_since,omitempty"`
}

// Options are the CLI flag values that feed resolution.
type Options struct {
	TokenFlag string
	ChatFlag  string
}

// Resolved is the fully resolved configuration for one invocation.
type Resolved struct {
	Token           string
	TokenSource     string // "flag", "env:ZIPCHAT_API_TOKEN", "file:<path>", "none"
	ChatID          string // may be empty: the commands layer auto-detects
	ChatSource      string // "flag", "env:ZCX_CHAT_ID", "file:<path>", "none"
	BaseURL         string
	BaseSource      string // "env:ZCX_BASE_URL", "file:<path>", "default"
	DataDir         string // root data dir ($XDG_DATA_HOME/zcx); per-chat subdirs live below it
	ConfigPath      string // ~/.config/zcx/config.toml
	EnvPath         string // ~/.config/zipchat/env
	Hours           *BusinessHours
	SyncConcurrency int        // sync_concurrency; 0 = default
	ScopeHost       string     // [scope] host; empty = every host
	LiveSince       *time.Time // [scope] live_since; nil = not configured
}

// MaskedToken renders a non-revealing preview of the token ("" when unset).
func (r *Resolved) MaskedToken() string { return MaskToken(r.Token) }

// MaskToken masks a token for display, never revealing more than a short
// prefix and suffix.
func MaskToken(t string) string {
	if t == "" {
		return ""
	}
	return cliconfig.MaskSecret(t)
}

// ConfigPath returns ~/.config/zcx/config.toml.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "zcx", "config.toml"), nil
}

// EnvFilePath returns ~/.config/zipchat/env (KEY=VALUE file shared with the
// other Zipchat tooling).
func EnvFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "zipchat", "env"), nil
}

// DataDir returns $XDG_DATA_HOME/zcx, falling back to ~/.local/share/zcx.
func DataDir() (string, error) {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "zcx"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "zcx"), nil
}

// ParseEnvFile parses a KEY=VALUE file: blank lines and #-comments are
// skipped, an optional "export " prefix is accepted, and values may be wrapped
// in single or double quotes. A missing file yields (nil, nil).
func ParseEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = unquoteEnvValue(v)
		if k != "" {
			out[k] = v
		}
	}
	return out, sc.Err()
}

// unquoteEnvValue parses a value shell-style: a leading quote runs to the
// matching closing quote and anything after it (comment, spaces) is ignored;
// an unquoted value is cut at " #".
func unquoteEnvValue(v string) string {
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		if i := strings.IndexByte(v[1:], v[0]); i >= 0 {
			return v[1 : 1+i]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// LoadFile reads config.toml. A missing file yields an empty File.
func LoadFile(path string) (*File, error) {
	var f File
	if _, err := toml.DecodeFile(path, &f); err != nil {
		if os.IsNotExist(err) {
			return &f, nil
		}
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

func chatString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	default:
		return fmt.Sprint(x)
	}
}

// Load resolves the configuration for this invocation. It never fails on a
// missing token or chat id (the commands layer reports that with a hint); it
// fails only on unreadable/malformed files.
func Load(opts Options) (*Resolved, error) {
	r := &Resolved{TokenSource: SourceNone, ChatSource: SourceNone, BaseSource: "default"}
	var err error
	if r.ConfigPath, err = ConfigPath(); err != nil {
		return nil, err
	}
	if r.EnvPath, err = EnvFilePath(); err != nil {
		return nil, err
	}
	if r.DataDir, err = DataDir(); err != nil {
		return nil, err
	}
	file, err := LoadFile(r.ConfigPath)
	if err != nil {
		return nil, err
	}
	envFile, err := ParseEnvFile(r.EnvPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", r.EnvPath, err)
	}

	// Token. Every source is trimmed: a stray newline or space (secret
	// managers, copy/paste) would otherwise corrupt the Authorization header.
	envTok := strings.TrimSpace(os.Getenv(EnvToken))
	switch {
	case strings.TrimSpace(opts.TokenFlag) != "":
		r.Token, r.TokenSource = strings.TrimSpace(opts.TokenFlag), SourceFlag
	case envTok != "":
		r.Token, r.TokenSource = envTok, "env:"+EnvToken
	case strings.TrimSpace(envFile[EnvToken]) != "":
		r.Token, r.TokenSource = strings.TrimSpace(envFile[EnvToken]), "file:"+r.EnvPath
	case strings.TrimSpace(file.Token) != "":
		r.Token, r.TokenSource = strings.TrimSpace(file.Token), "file:"+r.ConfigPath
	}

	// Chat id.
	envChat := strings.TrimSpace(os.Getenv(EnvChatID))
	switch {
	case strings.TrimSpace(opts.ChatFlag) != "":
		r.ChatID, r.ChatSource = strings.TrimSpace(opts.ChatFlag), SourceFlag
	case envChat != "":
		r.ChatID, r.ChatSource = envChat, "env:"+EnvChatID
	case strings.TrimSpace(chatString(file.ChatID)) != "":
		r.ChatID, r.ChatSource = strings.TrimSpace(chatString(file.ChatID)), "file:"+r.ConfigPath
	}

	// Base URL.
	r.BaseURL = DefaultBaseURL
	switch {
	case os.Getenv(EnvBaseURL) != "":
		r.BaseURL, r.BaseSource = os.Getenv(EnvBaseURL), "env:"+EnvBaseURL
	case file.BaseURL != "":
		r.BaseURL, r.BaseSource = file.BaseURL, "file:"+r.ConfigPath
	}
	r.BaseURL = strings.TrimRight(r.BaseURL, "/")

	r.SyncConcurrency = file.SyncConcurrency

	// Business hours.
	if r.Hours, err = ParseHours(file.Hours); err != nil {
		return nil, fmt.Errorf("%s: %w", r.ConfigPath, err)
	}

	// Analytics scope.
	r.ScopeHost = strings.ToLower(strings.TrimSpace(file.Scope.Host))
	if ls := strings.TrimSpace(file.Scope.LiveSince); ls != "" {
		t, err := time.Parse(time.RFC3339, ls)
		if err != nil {
			return nil, fmt.Errorf("%s: [scope] live_since must be RFC3339: %w", r.ConfigPath, err)
		}
		r.LiveSince = &t
	}
	return r, nil
}
