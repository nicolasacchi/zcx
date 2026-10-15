// Package store is the local per-chat cache: full conversations (with every
// message), the incremental-sync state, and prompt backups. The cache holds
// customer PII, so directories are 0700 and files 0600, and every write is
// atomic (temp file + rename).
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	cliconfig "github.com/nicolasacchi/clicore/config"
	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/config"
)

// ErrNotFound is returned when a conversation or backup is not cached.
var ErrNotFound = errors.New("not found in local cache")

// State is the incremental-sync checkpoint (state.json).
type State struct {
	// High-water marks: the max updated_at / last_message_at seen so far.
	UpdatedAtHighWater     *time.Time `json:"updated_at_high_water,omitempty"`
	LastMessageAtHighWater *time.Time `json:"last_message_at_high_water,omitempty"`
	LastSync               *time.Time `json:"last_sync,omitempty"`
	LastFullSync           *time.Time `json:"last_full_sync,omitempty"`
	// CachedTotal is the number of cached conversations after the last
	// successful sync; a smaller cache on disk means files were lost.
	CachedTotal int `json:"cached_total,omitempty"`
}

// PromptBackup describes one stored prompt backup.
type PromptBackup struct {
	Name   string    `json:"name"`
	Path   string    `json:"path"`
	Time   time.Time `json:"time"`
	SHA256 string    `json:"sha256"`
	Size   int64     `json:"size"`
}

// Store is the cache of one chat.
type Store struct {
	Dir    string // <data dir>/<chat id>
	ChatID string
}

// Open opens (creating it, 0700) the store for chatID under the default data
// dir ($XDG_DATA_HOME/zcx or ~/.local/share/zcx).
func Open(chatID string) (*Store, error) {
	root, err := config.DataDir()
	if err != nil {
		return nil, err
	}
	return OpenAt(root, chatID)
}

// OpenAt is Open with an explicit data dir root (tests, --data-dir style use).
func OpenAt(root, chatID string) (*Store, error) {
	if chatID == "" || strings.ContainsAny(chatID, `/\`) || chatID == "." || chatID == ".." {
		return nil, fmt.Errorf("invalid chat id %q", chatID)
	}
	s := &Store{Dir: filepath.Join(root, chatID), ChatID: chatID}
	for _, d := range []string{root, s.Dir, s.convDir(), s.promptDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) convDir() string   { return filepath.Join(s.Dir, "conversations") }
func (s *Store) promptDir() string { return filepath.Join(s.Dir, "prompts") }
func (s *Store) statePath() string { return filepath.Join(s.Dir, "state.json") }
func (s *Store) convPath(id int64) string {
	return filepath.Join(s.convDir(), strconv.FormatInt(id, 10)+".json")
}

// PromptDir returns the directory holding prompt backups.
// CountConversations returns the number of cached conversation files.
func (s *Store) CountConversations() int {
	ents, err := os.ReadDir(s.convDir())
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n
}

func (s *Store) PromptDir() string { return s.promptDir() }

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return cliconfig.WriteFileAtomic(path, b, 0o600)
}

// SaveConversation atomically writes the full conversation.
func (s *Store) SaveConversation(c *client.Conversation) error {
	if c == nil || c.ID == 0 {
		return errors.New("conversation without id")
	}
	return writeJSON(s.convPath(c.ID), c)
}

// LoadConversation reads one cached conversation (ErrNotFound if absent).
func (s *Store) LoadConversation(id int64) (*client.Conversation, error) {
	b, err := os.ReadFile(s.convPath(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("conversation %d: %w", id, ErrNotFound)
		}
		return nil, err
	}
	var c client.Conversation
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("corrupt cache file %s: %w", s.convPath(id), err)
	}
	return &c, nil
}

// LoadAll loads every cached conversation, optionally keeping only those with
// created_at in [from, to) (a nil bound is open). Sorted by created_at, id.
func (s *Store) LoadAll(from, to *time.Time) ([]client.Conversation, error) {
	ents, err := os.ReadDir(s.convDir())
	if err != nil {
		return nil, err
	}
	var out []client.Conversation
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
		if err != nil {
			continue
		}
		c, err := s.LoadConversation(id)
		if err != nil {
			return nil, err
		}
		if from != nil && c.CreatedAt.Before(*from) {
			continue
		}
		if to != nil && !c.CreatedAt.Before(*to) {
			continue
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// State returns the sync state (zero State when none was saved yet).
func (s *Store) State() (State, error) {
	var st State
	b, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("corrupt %s: %w", s.statePath(), err)
	}
	return st, nil
}

// SaveState atomically writes state.json.
func (s *Store) SaveState(st State) error { return writeJSON(s.statePath(), st) }

// SHA256 returns the lowercase hex sha256 of text.
func SHA256(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

const backupTimeLayout = "20060102T150405.000Z"

// BackupPrompt stores text under prompts/ and returns the file path. File
// names are <UTC timestamp>-<sha8>.txt so they sort chronologically.
func (s *Store) BackupPrompt(text string) (string, error) {
	name := fmt.Sprintf("%s-%s.txt", time.Now().UTC().Format(backupTimeLayout), SHA256(text)[:8])
	path := filepath.Join(s.promptDir(), name)
	if err := cliconfig.WriteFileAtomic(path, []byte(text), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ListPromptBackups lists backups, newest first.
func (s *Store) ListPromptBackups() ([]PromptBackup, error) {
	ents, err := os.ReadDir(s.promptDir())
	if err != nil {
		return nil, err
	}
	var out []PromptBackup
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".txt") {
			continue
		}
		stamp, _, _ := strings.Cut(strings.TrimSuffix(name, ".txt"), "-")
		ts, err := time.Parse(backupTimeLayout, stamp)
		if err != nil {
			continue
		}
		path := filepath.Join(s.promptDir(), name)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, PromptBackup{Name: name, Path: path, Time: ts, SHA256: SHA256(string(b)), Size: int64(len(b))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// ReadPromptBackup reads a backup by file name or by path. The file must live
// in this store's prompts/ directory.
func (s *Store) ReadPromptBackup(nameOrPath string) (string, error) {
	name := filepath.Base(nameOrPath)
	if strings.ContainsRune(nameOrPath, filepath.Separator) {
		abs, err := filepath.Abs(nameOrPath)
		if err != nil || filepath.Dir(abs) != filepath.Clean(s.promptDir()) {
			return "", fmt.Errorf("%s is not inside %s", nameOrPath, s.promptDir())
		}
	}
	b, err := os.ReadFile(filepath.Join(s.promptDir(), name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("prompt backup %q: %w", name, ErrNotFound)
		}
		return "", err
	}
	return string(b), nil
}
